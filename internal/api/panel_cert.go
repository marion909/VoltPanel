package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/marion909/voltpanel/internal/core"
	"github.com/marion909/voltpanel/internal/store"
)

// handlePanelCertStatus sagt, womit das Panel gerade ausliefert.
//
// Die Domain kommt aus der Konfiguration, nicht aus der Anfrage: sonst wäre
// dieser Aufruf ein Weg, sich über das Panel ein Zertifikat für eine beliebige
// fremde Domain ausstellen zu lassen.
func (s *Server) handlePanelCertStatus(c echo.Context) error {
	out := map[string]any{
		"domain":      s.cfg.PanelDomain,
		"acme_email":  s.cfg.ACMEEmail,
		"self_signed": true,
	}

	// Das Panel nimmt bei jedem Handshake das erste lesbare Paar der Kette.
	// Genau danach wird hier gefragt — nicht danach, was in der Datenbank
	// steht: ein Eintrag ohne Datei sagt nichts über die Auslieferung.
	for _, pair := range s.cfg.PanelTLSChain() {
		if _, err := os.Stat(pair.Cert); err != nil {
			continue
		}
		out["cert_path"] = pair.Cert
		out["self_signed"] = pair.Cert == s.cfg.SelfSignedPanelCert().Cert
		break
	}

	if s.cfg.PanelDomain != "" {
		if cert := s.panelCertRecord(c); cert != nil {
			out["not_after"] = cert.NotAfter
			out["days_left"] = cert.DaysLeft()
			out["challenge"] = cert.Challenge
		}
	}
	return c.JSON(http.StatusOK, out)
}

// panelCertRecord sucht den Datenbankeintrag zur Panel-Domain.
func (s *Server) panelCertRecord(c echo.Context) *store.Cert {
	certs, err := s.store.ListCerts(c.Request().Context(), currentScope(c))
	if err != nil {
		return nil
	}
	for _, cert := range certs {
		if cert.SiteID != nil {
			continue
		}
		for _, d := range cert.Domains {
			if strings.EqualFold(d, s.cfg.PanelDomain) {
				return cert
			}
		}
	}
	return nil
}

// handleIssuePanelCert holt ein Zertifikat für die Panel-Domain.
//
// Über die CLI ging das schon (`volt cert issue <panel_domain>`); wer das Panel
// installiert hat, sitzt danach aber nicht mehr zwingend auf einer Shell.
func (s *Server) handleIssuePanelCert(c echo.Context) error {
	if s.cfg.PanelDomain == "" {
		return echo.NewHTTPError(http.StatusBadRequest,
			"es ist keine panel-domain konfiguriert — panel_domain in der config.yaml setzen")
	}

	ctx, sc := c.Request().Context(), currentScope(c)
	user := currentUser(c)

	cert, err := s.certs.Issue(ctx, sc, core.IssueOptions{
		Domains: []string{s.cfg.PanelDomain}, TenantID: user.TenantID,
	})
	if err != nil {
		s.audit(ctx, user, "cert.issue_panel", "panel", s.cfg.PanelDomain, "error", c.RealIP(),
			map[string]string{"fehler": err.Error()})
		return storeError(err)
	}

	// Kein Neustart: der Webserver prüft bei jedem Handshake nach, welche
	// Datei der Kette lesbar ist, und nimmt die neue von selbst.
	s.audit(ctx, user, "cert.issue_panel", "panel", s.cfg.PanelDomain, "ok", c.RealIP(),
		map[string]any{"verfahren": cert.Challenge})
	return c.JSON(http.StatusCreated, map[string]any{
		"domain": s.cfg.PanelDomain, "not_after": cert.NotAfter,
		"days_left": cert.DaysLeft(), "challenge": cert.Challenge,
		"cert_path": filepath.Clean(cert.CertPath),
	})
}
