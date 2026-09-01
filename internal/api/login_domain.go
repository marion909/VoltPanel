package api

import (
	"net/http"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/marion909/voltpanel/internal/core"
	"github.com/marion909/voltpanel/internal/store"
)

type loginDomainRequest struct {
	Domain string `json:"domain"`
}

// handleSetLoginDomain trägt die Anmeldedomain eines Mandanten ein oder
// entfernt sie. Ein leerer Wert entfernt sie.
func (s *Server) handleSetLoginDomain(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	var req loginDomainRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}

	domain, err := store.NormalizeLoginDomain(req.Domain)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	// Die Domain des Panels gehört dem Betreiber. Sie einem Mandanten zu geben
	// hieße, dass sich am eigenen Panel nur noch dessen Leute anmelden können —
	// der Betreiber schlösse sich damit selbst aus.
	if domain != "" && strings.EqualFold(domain, s.cfg.PanelDomain) {
		return echo.NewHTTPError(http.StatusBadRequest,
			"das ist die domain des panels — sie kann keinem mandanten gehören")
	}

	ctx := c.Request().Context()
	if err := s.store.SetTenantLoginDomain(ctx, currentScope(c), id, domain); err != nil {
		return storeError(err)
	}
	// Ohne das dauerte es bis zu einer halben Minute, bis die Domain wirkt, und
	// der Betreiber hielte sie für kaputt.
	s.logins.invalidate()

	s.audit(ctx, currentUser(c), "tenant.login_domain", "tenant", pathParam(c, "id"),
		"ok", c.RealIP(), map[string]string{"domain": domain})
	return c.JSON(http.StatusOK, map[string]string{"login_domain": domain})
}

// handleIssueLoginDomainCert holt ein Zertifikat für die Anmeldedomain.
//
// Ohne eines zeigt der Browser des Kunden eine Warnung — das Panel liefert dann
// das Zertifikat des Betreibers aus, das auf einen anderen Namen lautet. Eine
// Anmeldeseite, die mit einer Zertifikatswarnung beginnt, erzieht genau zu der
// Gewohnheit, die eine gefälschte später ausnutzt.
//
// Die Domain kommt aus der Datenbank, nicht aus der Anfrage: sonst wäre dieser
// Aufruf ein Weg, sich über das Panel ein Zertifikat für eine beliebige fremde
// Domain ausstellen zu lassen.
func (s *Server) handleIssueLoginDomainCert(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	ctx, sc := c.Request().Context(), currentScope(c)

	tenant, err := s.store.GetTenant(ctx, sc, id)
	if err != nil {
		return storeError(err)
	}
	if tenant.LoginDomain == "" {
		return echo.NewHTTPError(http.StatusBadRequest,
			"für diesen mandanten ist keine anmeldedomain eingetragen")
	}

	user := currentUser(c)
	cert, err := s.certs.Issue(ctx, sc, core.IssueOptions{
		Domains: []string{tenant.LoginDomain}, TenantID: tenant.ID,
	})
	if err != nil {
		s.audit(ctx, user, "cert.issue_login_domain", "tenant", tenant.Slug, "error",
			c.RealIP(), map[string]string{"fehler": err.Error()})
		return storeError(err)
	}

	// Kein Neustart: das Panel sucht bei jedem Handshake nach dem Zertifikat
	// zum angefragten Namen und nimmt das neue von selbst.
	s.audit(ctx, user, "cert.issue_login_domain", "tenant", tenant.Slug, "ok",
		c.RealIP(), map[string]any{"domain": tenant.LoginDomain, "verfahren": cert.Challenge})
	return c.JSON(http.StatusCreated, map[string]any{
		"domain": tenant.LoginDomain, "not_after": cert.NotAfter,
		"days_left": cert.DaysLeft(), "challenge": cert.Challenge,
		"cert_path": filepath.Clean(cert.CertPath),
	})
}
