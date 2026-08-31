package api

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/marion909/voltpanel/internal/core"
	"github.com/marion909/voltpanel/internal/store"
)

type siteSettingsRequest struct {
	Redirects  *[]store.Redirect `json:"redirects"`
	DenyIPs    *[]string         `json:"deny_ips"`
	AllowIPs   *[]string         `json:"allow_ips"`
	ExtraLines *[]string         `json:"extra_lines"`

	MaxBodySize    *string `json:"max_body_size"`
	FastCGITimeout *int    `json:"fastcgi_timeout"`

	BasicAuthUsers *[]core.AuthUser `json:"basic_auth_users"`
	BasicAuthRealm *string          `json:"basic_auth_realm"`
}

func (s *Server) handleGetSiteSettings(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	site, err := s.store.GetSite(c.Request().Context(), currentScope(c), id)
	if err != nil {
		return storeError(err)
	}
	return c.JSON(http.StatusOK, site.Settings)
}

// handleUpdateSiteSettings ändert die Vhost-Zusätze und erzeugt die Config neu.
func (s *Server) handleUpdateSiteSettings(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	var req siteSettingsRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}

	ctx := c.Request().Context()
	site, err := s.sites.UpdateSettings(ctx, currentScope(c), id, core.UpdateSettingsInput{
		Redirects: req.Redirects, DenyIPs: req.DenyIPs, AllowIPs: req.AllowIPs,
		ExtraLines: req.ExtraLines, MaxBodySize: req.MaxBodySize,
		FastCGITimeout: req.FastCGITimeout,
		BasicAuthUsers: req.BasicAuthUsers, BasicAuthRealm: req.BasicAuthRealm,
	})
	if err != nil {
		return storeError(err)
	}

	// Was genau geändert wurde, steht im Log — aber keine Passwörter.
	s.audit(ctx, currentUser(c), "site.settings", "site", site.Domain, "ok", c.RealIP(),
		map[string]any{
			"weiterleitungen": len(site.Settings.Redirects),
			"ip_regeln":       len(site.Settings.DenyIPs) + len(site.Settings.AllowIPs),
			"passwortschutz":  site.Settings.BasicAuth != nil && site.Settings.BasicAuth.Enabled,
		})
	return c.JSON(http.StatusOK, site.Settings)
}

// --- PHP je Site -----------------------------------------------------------

func (s *Server) handleGetSitePHP(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}

	ctx, sc := c.Request().Context(), currentScope(c)
	site, err := s.store.GetSite(ctx, sc, id)
	if err != nil {
		return storeError(err)
	}
	if site.Type != store.SitePHP {
		return echo.NewHTTPError(http.StatusBadRequest, "diese site ist keine php-site")
	}

	pool, err := s.store.PHPPoolBySite(ctx, sc, id)
	if err != nil {
		return storeError(err)
	}

	// Die verfügbaren Versionen kommen vom Server; fehlt der Agent, bleibt die
	// Liste leer statt die ganze Antwort zu verhindern.
	versions, err := s.agent.PHPVersions(ctx)
	if err != nil {
		s.log.Debug("php-versionen nicht abrufbar", "err", err)
		versions = []string{}
	}
	return c.JSON(http.StatusOK, map[string]any{"pool": pool, "available_versions": versions})
}

type sitePHPRequest struct {
	PHPVersion        *string `json:"php_version"`
	PM                *string `json:"pm"`
	MaxChildren       *int    `json:"max_children"`
	MemoryLimit       *string `json:"memory_limit"`
	MaxExecutionTime  *int    `json:"max_execution_time"`
	UploadMaxFilesize *string `json:"upload_max_filesize"`
	DisableFunctions  *string `json:"disable_functions"`
	ExtraINI          *string `json:"extra_ini"`
}

func (s *Server) handleUpdateSitePHP(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	var req sitePHPRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}

	// disable_functions zu leeren hebt die Isolation der Site auf — das darf
	// ein Kunde nicht für sich selbst entscheiden.
	if req.DisableFunctions != nil && !hasRoleAtLeast(c, store.RoleAdmin) {
		return echo.NewHTTPError(http.StatusForbidden,
			"gesperrte php-funktionen kann nur ein administrator ändern")
	}
	if req.ExtraINI != nil && !hasRoleAtLeast(c, store.RoleAdmin) {
		return echo.NewHTTPError(http.StatusForbidden,
			"zusätzliche ini-einstellungen kann nur ein administrator ändern")
	}

	ctx := c.Request().Context()
	pool, err := s.sites.UpdatePHP(ctx, currentScope(c), id, core.UpdatePHPInput{
		PHPVersion: req.PHPVersion, PM: req.PM, MaxChildren: req.MaxChildren,
		MemoryLimit: req.MemoryLimit, MaxExecutionTime: req.MaxExecutionTime,
		UploadMaxFilesize: req.UploadMaxFilesize,
		DisableFunctions:  req.DisableFunctions, ExtraINI: req.ExtraINI,
	})
	if err != nil {
		return storeError(err)
	}

	s.audit(ctx, currentUser(c), "site.php", "site", pool.PoolName, "ok", c.RealIP(),
		map[string]any{"version": pool.PHPVersion, "speicher": pool.MemoryLimit})
	return c.JSON(http.StatusOK, pool)
}

// --- Zertifikate -----------------------------------------------------------

func (s *Server) handleListCerts(c echo.Context) error {
	certs, err := s.store.ListCerts(c.Request().Context(), s.scopeFor(c))
	if err != nil {
		return storeError(err)
	}
	return c.JSON(http.StatusOK, certs)
}

type issueCertRequest struct {
	// Wildcard verlangt DNS-01 und damit einen Cloudflare-Token.
	Wildcard        bool     `json:"wildcard"`
	ExtraDomains    []string `json:"extra_domains"`
	CloudflareToken string   `json:"cloudflare_token"`
}

// handleIssueCert holt ein Zertifikat für eine Site.
//
// Das kann Minuten dauern (DNS-Propagation bei DNS-01), läuft aber bewusst
// synchron: ein Fehler soll dem Aufrufer direkt gemeldet werden, statt in
// einem Hintergrundjob zu verschwinden.
func (s *Server) handleIssueCert(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	var req issueCertRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}

	ctx, sc := c.Request().Context(), currentScope(c)
	site, err := s.store.GetSite(ctx, sc, id)
	if err != nil {
		return storeError(err)
	}

	domains := append([]string{site.Domain}, site.Aliases...)
	if req.Wildcard {
		domains = append(domains, "*."+site.Domain)
	}
	domains = append(domains, req.ExtraDomains...)

	cert, err := s.certs.Issue(ctx, sc, core.IssueOptions{
		Domains: domains, CloudflareToken: req.CloudflareToken,
		SiteID: &site.ID, TenantID: site.TenantID,
	})
	if err != nil {
		s.audit(ctx, currentUser(c), "cert.issue", "site", site.Domain, "error", c.RealIP(),
			map[string]string{"fehler": err.Error()})
		return storeError(err)
	}

	s.audit(ctx, currentUser(c), "cert.issue", "site", site.Domain, "ok", c.RealIP(),
		map[string]any{"domains": cert.Domains, "verfahren": cert.Challenge})
	return c.JSON(http.StatusCreated, cert)
}

func (s *Server) handleDeleteCert(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}

	ctx, sc := c.Request().Context(), currentScope(c)
	cert, err := s.store.GetCert(ctx, sc, id)
	if err != nil {
		return storeError(err)
	}
	if err := s.store.DeleteCert(ctx, sc, id); err != nil {
		return storeError(err)
	}

	// Die Dateien bleiben liegen: sie werden noch vom laufenden Nginx benutzt,
	// bis die Site neu erzeugt wird.
	s.audit(ctx, currentUser(c), "cert.delete", "cert", strings.Join(cert.Domains, ","),
		"ok", c.RealIP(), nil)
	return c.NoContent(http.StatusNoContent)
}

// --- Cloudflare-Token je Mandant -------------------------------------------

type cloudflareRequest struct {
	Token string `json:"token"`
}

// handleSetCloudflareToken speichert den Token verschlüsselt. Ein leerer Wert
// entfernt ihn. Der Token wird nie wieder herausgegeben — nur die Angabe, ob
// einer hinterlegt ist.
func (s *Server) handleSetCloudflareToken(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	var req cloudflareRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}

	ctx := c.Request().Context()
	if err := s.certs.SetCloudflareToken(ctx, currentScope(c), id, req.Token); err != nil {
		return storeError(err)
	}

	s.audit(ctx, currentUser(c), "tenant.cloudflare_token", "tenant", pathParam(c, "id"),
		"ok", c.RealIP(), map[string]bool{"hinterlegt": strings.TrimSpace(req.Token) != ""})
	return c.JSON(http.StatusOK, map[string]bool{
		"has_cloudflare_token": strings.TrimSpace(req.Token) != "",
	})
}

// hasRoleAtLeast prüft die Rolle innerhalb eines Handlers — für Fälle, in
// denen nicht der ganze Endpunkt, sondern nur ein Feld beschränkt ist.
func hasRoleAtLeast(c echo.Context, min store.Role) bool {
	user := currentUser(c)
	return user != nil && roleRank(user.Role) >= roleRank(min)
}

func pathParam(c echo.Context, name string) string { return c.Param(name) }
