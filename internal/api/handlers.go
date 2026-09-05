package api

import (
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/marion909/voltpanel/internal/agent"
	"github.com/marion909/voltpanel/internal/authn"
	"github.com/marion909/voltpanel/internal/core"
	"github.com/marion909/voltpanel/internal/store"
	"github.com/marion909/voltpanel/internal/version"
)

func (s *Server) versionInfo() map[string]string {
	return map[string]string{
		"version": version.Version, "commit": version.Commit,
		"channel": version.Channel, "built": version.Date,
	}
}

// --- System ----------------------------------------------------------------

func (s *Server) handleSystemInfo(c echo.Context) error {
	info, err := s.agent.SystemInfo(c.Request().Context())
	if err != nil {
		return agentError(err)
	}

	sc := currentScope(c)
	sites, err := s.store.CountSites(c.Request().Context(), sc)
	if err != nil {
		return err
	}
	certs, err := s.store.ListCerts(c.Request().Context(), sc)
	if err != nil {
		return err
	}

	// Ablaufende Zertifikate gehören auf die Startseite, nicht in ein Untermenü.
	expiring := 0
	for _, cert := range certs {
		if cert.DaysLeft() <= 14 {
			expiring++
		}
	}
	return c.JSON(http.StatusOK, map[string]any{
		"system": info, "version": s.versionInfo(),
		"counts": map[string]int{"sites": sites, "certs": len(certs), "certs_expiring": expiring},
	})
}

func (s *Server) handleMetricsSnapshot(c echo.Context) error {
	return c.JSON(http.StatusOK, map[string]any{
		"latest": s.metrics.Latest(), "series": s.metrics.Series(),
	})
}

func (s *Server) handleServices(c echo.Context) error {
	services, err := s.agent.Services(c.Request().Context())
	if err != nil {
		return agentError(err)
	}
	return c.JSON(http.StatusOK, services)
}

func (s *Server) handleServiceAction(c echo.Context) error {
	name, action := c.Param("name"), c.Param("action")
	switch action {
	case "start", "stop", "restart", "reload", "enable", "disable":
	default:
		return echo.NewHTTPError(http.StatusBadRequest, "unbekannte aktion "+action)
	}

	ctx := c.Request().Context()
	status, err := s.agent.ServiceAction(ctx, action, name)
	user := currentUser(c)
	if err != nil {
		s.audit(ctx, user, "service."+action, "service", name, "error", c.RealIP(),
			map[string]string{"fehler": err.Error()})
		return agentError(err)
	}

	s.audit(ctx, user, "service."+action, "service", name, "ok", c.RealIP(), nil)
	return c.JSON(http.StatusOK, status)
}

// --- Sites -----------------------------------------------------------------

func (s *Server) handleListSites(c echo.Context) error {
	sites, err := s.store.ListSites(c.Request().Context(), s.scopeFor(c))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, sites)
}

func (s *Server) handleGetSite(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	site, err := s.store.GetSite(c.Request().Context(), s.scopeFor(c), id)
	if err != nil {
		return storeError(err)
	}
	return c.JSON(http.StatusOK, site)
}

type createSiteRequest struct {
	Domain       string   `json:"domain"`
	Aliases      []string `json:"aliases"`
	Type         string   `json:"type"`
	PHPVersion   string   `json:"php_version"`
	ProxyTarget  string   `json:"proxy_target"`
	DocumentRoot string   `json:"document_root"`
	TenantID     int64    `json:"tenant_id"`
}

func (s *Server) handleCreateSite(c echo.Context) error {
	var req createSiteRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}

	sc := currentScope(c)
	// Eine fremde tenant_id ist nur für Rollen erlaubt, die das dürfen —
	// ForTenant prüft das und liefert sonst ErrForbidden. Die zurückgegebene,
	// elevierte Scope muss danach auch tatsächlich verwendet werden — sonst
	// bliebe CreateSite mit dem ursprünglichen sc unterwegs und scheiterte an
	// sc.owns(req.TenantID), obwohl ForTenant den Zugriff gerade erlaubt hat.
	if req.TenantID != 0 && req.TenantID != sc.TenantID {
		elevated, err := sc.ForTenant(req.TenantID)
		if err != nil {
			return storeError(err)
		}
		sc = elevated
	}

	ctx := c.Request().Context()
	site, err := s.sites.CreateSite(ctx, sc, core.CreateSiteInput{
		Domain: req.Domain, Aliases: req.Aliases, Type: store.SiteType(req.Type),
		PHPVersion: req.PHPVersion, ProxyTarget: req.ProxyTarget,
		DocumentRoot: req.DocumentRoot, TenantID: req.TenantID,
	})

	user := currentUser(c)
	if err != nil {
		s.audit(ctx, user, "site.create", "site", req.Domain, "error", c.RealIP(),
			map[string]string{"fehler": err.Error()})
		return storeError(err)
	}

	s.audit(ctx, user, "site.create", "site", site.Domain, "ok", c.RealIP(),
		map[string]any{"typ": site.Type, "php": site.PHPVersion})
	return c.JSON(http.StatusCreated, site)
}

type updateSiteRequest struct {
	Aliases      *[]string `json:"aliases"`
	PHPVersion   *string   `json:"php_version"`
	ProxyTarget  *string   `json:"proxy_target"`
	DocumentRoot *string   `json:"document_root"`
	ForceHTTPS   *bool     `json:"force_https"`
	HSTS         *bool     `json:"hsts"`
	Status       *string   `json:"status"`
}

func (s *Server) handleUpdateSite(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	var req updateSiteRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}

	ctx, sc := c.Request().Context(), currentScope(c)
	site, err := s.store.GetSite(ctx, sc, id)
	if err != nil {
		return storeError(err)
	}

	// Nur gesetzte Felder ändern — ein PATCH ohne Feld darf nichts leeren.
	applyIf(req.Aliases, &site.Aliases)
	applyIf(req.PHPVersion, &site.PHPVersion)
	applyIf(req.ProxyTarget, &site.ProxyTarget)
	applyIf(req.DocumentRoot, &site.DocumentRoot)
	applyIf(req.ForceHTTPS, &site.ForceHTTPS)
	applyIf(req.HSTS, &site.HSTS)
	applyIf(req.Status, &site.Status)

	if err := s.store.UpdateSite(ctx, sc, site); err != nil {
		return storeError(err)
	}
	// Die Datenbank ist die Quelle der Wahrheit; die Config wird daraus neu erzeugt.
	if err := s.sites.Rebuild(ctx, sc, site.ID); err != nil {
		return storeError(err)
	}

	s.audit(ctx, currentUser(c), "site.update", "site", site.Domain, "ok", c.RealIP(), nil)
	return c.JSON(http.StatusOK, site)
}

func (s *Server) handleDeleteSite(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	// Dateien bleiben, außer der Aufrufer verlangt ausdrücklich etwas anderes.
	purge := c.QueryParam("purge") == "true"

	ctx, sc := c.Request().Context(), currentScope(c)
	site, err := s.store.GetSite(ctx, sc, id)
	if err != nil {
		return storeError(err)
	}

	if err := s.sites.DeleteSite(ctx, sc, id, !purge); err != nil {
		s.audit(ctx, currentUser(c), "site.delete", "site", site.Domain, "error", c.RealIP(),
			map[string]string{"fehler": err.Error()})
		return storeError(err)
	}

	s.audit(ctx, currentUser(c), "site.delete", "site", site.Domain, "ok", c.RealIP(),
		map[string]bool{"dateien_geloescht": purge})
	return c.NoContent(http.StatusNoContent)
}

func (s *Server) handleRebuildSite(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	ctx, sc := c.Request().Context(), currentScope(c)
	if err := s.sites.Rebuild(ctx, sc, id); err != nil {
		return storeError(err)
	}
	s.audit(ctx, currentUser(c), "site.rebuild", "site", strconv.FormatInt(id, 10), "ok", c.RealIP(), nil)
	return c.JSON(http.StatusOK, map[string]string{"status": "neu erzeugt"})
}

// handleSiteLogs liefert die letzten Zeilen des Access- oder Error-Logs.
func (s *Server) handleSiteLogs(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	ctx, sc := c.Request().Context(), currentScope(c)
	site, err := s.store.GetSite(ctx, sc, id)
	if err != nil {
		return storeError(err)
	}

	// Der Dateiname wird aus der Domain gebildet, nicht aus einem Query-Wert —
	// so kann über diesen Endpunkt kein beliebiges Log gelesen werden.
	suffix := "access"
	if c.QueryParam("type") == "error" {
		suffix = "error"
	}
	path := filepath.Join(s.cfg.LogDir, "sites", site.Domain+"."+suffix+".log")

	lines, _ := strconv.Atoi(c.QueryParam("lines"))
	text, err := s.agent.TailLog(ctx, path, lines)
	if err != nil {
		return agentError(err)
	}
	return c.JSON(http.StatusOK, map[string]string{"path": path, "content": text})
}

// --- Tenants, Benutzer, Audit ----------------------------------------------

func (s *Server) handleListTenants(c echo.Context) error {
	tenants, err := s.store.ListTenants(c.Request().Context(), s.scopeFor(c))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, tenants)
}

type createTenantRequest struct {
	Name   string `json:"name"`
	Slug   string `json:"slug"`
	PlanID *int64 `json:"plan_id"`
}

func (s *Server) handleCreateTenant(c echo.Context) error {
	var req createTenantRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}
	if req.Slug == "" {
		req.Slug = slugify(req.Name)
	}

	tenant := &store.Tenant{Name: req.Name, Slug: req.Slug, PlanID: req.PlanID}
	ctx := c.Request().Context()
	if err := s.store.CreateTenant(ctx, currentScope(c).Elevate(), tenant); err != nil {
		return storeError(err)
	}

	s.audit(ctx, currentUser(c), "tenant.create", "tenant", tenant.Slug, "ok", c.RealIP(), nil)
	return c.JSON(http.StatusCreated, tenant)
}

func (s *Server) handleListUsers(c echo.Context) error {
	users, err := s.store.ListUsers(c.Request().Context(), s.scopeFor(c))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, users)
}

type createUserRequest struct {
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Password    string `json:"password"`
	Role        string `json:"role"`
	TenantID    int64  `json:"tenant_id"`
}

func (s *Server) handleCreateUser(c echo.Context) error {
	var req createUserRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}
	if err := authn.DefaultPolicy().Check(req.Password); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	actor, sc := currentUser(c), currentScope(c)
	role := store.Role(req.Role)
	// Niemand darf jemanden anlegen, der mehr darf als er selbst.
	if roleRank(role) > roleRank(actor.Role) {
		return echo.NewHTTPError(http.StatusForbidden,
			"eine rolle über der eigenen kann nicht vergeben werden")
	}

	tenantID := req.TenantID
	if tenantID == 0 {
		tenantID = sc.TenantID
	}
	targetScope, err := sc.ForTenant(tenantID)
	if err != nil {
		return storeError(err)
	}

	hash, err := authn.HashPassword(req.Password)
	if err != nil {
		return err
	}
	user := &store.User{
		TenantID: tenantID, Email: req.Email, DisplayName: req.DisplayName,
		PasswordHash: hash, Role: role, MustChangePW: true,
	}

	ctx := c.Request().Context()
	if err := s.store.CreateUser(ctx, targetScope, user); err != nil {
		return storeError(err)
	}
	s.audit(ctx, actor, "user.create", "user", user.Email, "ok", c.RealIP(),
		map[string]any{"rolle": user.Role, "tenant": tenantID})
	return c.JSON(http.StatusCreated, user)
}

func (s *Server) handleDeleteUser(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	actor := currentUser(c)
	if actor.ID == id {
		return echo.NewHTTPError(http.StatusBadRequest, "das eigene konto kann nicht gelöscht werden")
	}

	ctx, sc := c.Request().Context(), currentScope(c)
	target, err := s.store.GetUser(ctx, sc, id)
	if err != nil {
		return storeError(err)
	}
	if roleRank(target.Role) > roleRank(actor.Role) {
		return echo.NewHTTPError(http.StatusForbidden,
			"ein konto mit höherer rolle kann nicht gelöscht werden")
	}

	if err := s.store.DeleteUser(ctx, sc, id); err != nil {
		return storeError(err)
	}
	s.audit(ctx, actor, "user.delete", "user", target.Email, "ok", c.RealIP(), nil)
	return c.NoContent(http.StatusNoContent)
}

func (s *Server) handleAudit(c echo.Context) error {
	limit, _ := strconv.Atoi(c.QueryParam("limit"))
	entries, err := s.store.ListAudit(c.Request().Context(), s.scopeFor(c), limit)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, entries)
}

// --- Hilfsfunktionen -------------------------------------------------------

// scopeFor liefert den Scope einer Leseanfrage. Owner und Admin sehen per
// ?all=true alle Tenants; für alle anderen bleibt der Parameter wirkungslos,
// weil Elevate() ihre Rolle prüft.
func (s *Server) scopeFor(c echo.Context) store.Scope {
	sc := currentScope(c)
	if c.QueryParam("all") == "true" {
		return sc.Elevate()
	}
	return sc
}

func pathID(c echo.Context) (int64, error) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, echo.NewHTTPError(http.StatusBadRequest, "ungültige id")
	}
	return id, nil
}

// applyIf übernimmt einen Wert nur, wenn das Feld im PATCH gesetzt war.
func applyIf[T any](src *T, dst *T) {
	if src != nil {
		*dst = *src
	}
}

// storeError übersetzt Fehler des store-Pakets in HTTP-Status.
//
// ErrForbidden wird bewusst zu 404, nicht zu 403: eine 403 würde bestätigen,
// dass die fremde ID existiert.
// handleInstallFeature holt nach, was das Panel verwaltet.
//
// Administratoren vorbehalten: es installiert Pakete auf dem Server. Und es
// nimmt keinen Paketnamen entgegen, sondern eine Fähigkeit aus der festen
// Liste des Agents — `apt-get install` mit fremder Eingabe wäre eine
// Rootshell mit Umweg.
func (s *Server) handleInstallFeature(c echo.Context) error {
	feature := c.Param("name")
	ctx := c.Request().Context()

	out, err := s.agent.InstallFeature(ctx, feature)
	if err != nil {
		s.audit(ctx, currentUser(c), "feature.install", "feature", feature,
			"error", c.RealIP(), map[string]string{"fehler": err.Error()})
		return storeError(err)
	}
	s.audit(ctx, currentUser(c), "feature.install", "feature", feature, "ok", c.RealIP(), nil)
	return c.JSON(http.StatusOK, map[string]string{"log": out})
}

// handleFeatures sagt, was sich nachinstallieren lässt.
func (s *Server) handleFeatures(c echo.Context) error {
	return c.JSON(http.StatusOK, agent.FeatureNames())
}

func storeError(err error) error {
	switch {
	case err == nil:
		return nil
	case isAgentFailure(err):
		// Ein nicht laufender oder ablehnender Agent ist kein Eingabefehler.
		return agentError(err)
	case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrForbidden):
		return echo.NewHTTPError(http.StatusNotFound, "nicht gefunden")
	case errors.Is(err, store.ErrConflict):
		return echo.NewHTTPError(http.StatusConflict, err.Error())
	case errors.Is(err, store.ErrNoTenant):
		return echo.NewHTTPError(http.StatusForbidden, "kein gültiger zugriffsbereich")
	}
	var he *echo.HTTPError
	if errors.As(err, &he) {
		return err
	}
	// Validierungsfehler aus core/store sind für den Nutzer gedacht.
	return echo.NewHTTPError(http.StatusBadRequest, err.Error())
}

// isAgentFailure erkennt Fehler, die aus der Agent-Verbindung stammen — auch
// wenn sie unterwegs mit fmt.Errorf umhüllt wurden.
func isAgentFailure(err error) bool {
	var unavailable *agent.UnavailableError
	var opErr *agent.OpError
	return errors.As(err, &unavailable) || errors.As(err, &opErr)
}

func agentError(err error) error {
	if err == nil {
		return nil
	}

	var unavailable *agent.UnavailableError
	if errors.As(err, &unavailable) {
		return echo.NewHTTPError(http.StatusServiceUnavailable,
			"der volt-agent läuft nicht — systemaktionen sind derzeit nicht möglich")
	}

	// Eine abgelehnte Eingabe ist kein Gateway-Fehler. Als 502 stünde da eine
	// Meldung, die den Server verdächtigt, obwohl der Text im Eingabefeld
	// steht.
	var opErr *agent.OpError
	if errors.As(err, &opErr) && opErr.Input {
		return echo.NewHTTPError(http.StatusBadRequest, opErr.Message)
	}
	return echo.NewHTTPError(http.StatusBadGateway, "agent: "+err.Error())
}

func slugify(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}
