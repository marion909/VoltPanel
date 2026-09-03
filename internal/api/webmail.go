package api

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/marion909/voltpanel/internal/core"
	"github.com/marion909/voltpanel/internal/store"
)

// Webmail: die eine, server-weite Roundcube-Installation. Administratoren
// vorbehalten wie Docker, die Firewall oder ein Plugin — sie betrifft den
// ganzen Server, nicht einen Mandanten.

// handleWebmailStatus sagt, ob Webmail installiert ist.
//
// "Nicht installiert" ist ein gewöhnlicher, erwarteter Zustand — kein Fehler
// und kein 404. Ein Kunde, der die Seite lädt, bevor ein Administrator
// Webmail eingerichtet hat, soll eine leere Auskunft sehen, keine
// Fehlermeldung im Log.
func (s *Server) handleWebmailStatus(c echo.Context) error {
	w, err := s.webmail.Status(c.Request().Context())
	if errors.Is(err, store.ErrNotFound) {
		return c.JSON(http.StatusOK, map[string]any{"installed": false})
	}
	if err != nil {
		return storeError(err)
	}
	return c.JSON(http.StatusOK, map[string]any{
		"installed": true, "hostname": w.Hostname, "php_version": w.PHPVersion,
		"installed_at": w.InstalledAt,
	})
}

type webmailInstallRequest struct {
	PHPVersion string `json:"php_version"`
}

// handleWebmailInstall richtet Roundcube ein.
//
// Das Zertifikat für webmail.<panel_domain> holt derselbe Weg wie das der
// Panel-Domain selbst: der Cloudflare-Token des anfragenden Administrators,
// nicht ein serverweiter, den niemand hinterlegt hätte.
func (s *Server) handleWebmailInstall(c echo.Context) error {
	var req webmailInstallRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}
	ctx, user := c.Request().Context(), currentUser(c)

	w, err := s.webmail.Install(ctx, currentScope(c), core.InstallWebmailInput{
		PHPVersion: req.PHPVersion, TenantID: user.TenantID,
	})
	if err != nil {
		s.audit(ctx, user, "webmail.install", "webmail", "", "fehler", c.RealIP(),
			map[string]string{"fehler": err.Error()})
		return storeError(err)
	}
	s.audit(ctx, user, "webmail.install", "webmail", w.Hostname, "ok", c.RealIP(), nil)
	return c.JSON(http.StatusCreated, map[string]any{"hostname": w.Hostname})
}

// handleWebmailUninstall nimmt die Installation wieder herunter.
func (s *Server) handleWebmailUninstall(c echo.Context) error {
	ctx, user := c.Request().Context(), currentUser(c)
	if err := s.webmail.Uninstall(ctx); err != nil {
		s.audit(ctx, user, "webmail.uninstall", "webmail", "", "fehler", c.RealIP(),
			map[string]string{"fehler": err.Error()})
		return storeError(err)
	}
	s.audit(ctx, user, "webmail.uninstall", "webmail", "", "ok", c.RealIP(), nil)
	return c.NoContent(http.StatusNoContent)
}
