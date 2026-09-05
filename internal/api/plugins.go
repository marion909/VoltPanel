package api

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// Plugins: server-weite Fähigkeiten aus dem festen Katalog
// (internal/core/plugins.go). Administratoren vorbehalten — ein Plugin
// betrifft den ganzen Server, nicht einen Mandanten, genau wie Docker oder
// die Firewall.

// handleListPlugins zeigt den Katalog samt Zustand auf diesem Server.
func (s *Server) handleListPlugins(c echo.Context) error {
	list, err := s.plugins.List(c.Request().Context())
	if err != nil {
		return storeError(err)
	}
	return c.JSON(http.StatusOK, list)
}

// handleInstallPlugin installiert ein Plugin aus dem Katalog.
func (s *Server) handleInstallPlugin(c echo.Context) error {
	id := c.Param("id")
	ctx := c.Request().Context()

	out, err := s.plugins.Install(ctx, id)
	if err != nil {
		s.audit(ctx, currentUser(c), "plugin.install", "plugin", id, "error", c.RealIP(),
			map[string]string{"fehler": err.Error()})
		return storeError(err)
	}
	s.audit(ctx, currentUser(c), "plugin.install", "plugin", id, "ok", c.RealIP(), nil)
	return c.JSON(http.StatusOK, map[string]string{"log": out})
}

// handleUninstallPlugin entfernt ein Plugin wieder.
func (s *Server) handleUninstallPlugin(c echo.Context) error {
	id := c.Param("id")
	ctx := c.Request().Context()

	out, err := s.plugins.Uninstall(ctx, id)
	if err != nil {
		s.audit(ctx, currentUser(c), "plugin.uninstall", "plugin", id, "error", c.RealIP(),
			map[string]string{"fehler": err.Error()})
		return storeError(err)
	}
	s.audit(ctx, currentUser(c), "plugin.uninstall", "plugin", id, "ok", c.RealIP(), nil)
	return c.JSON(http.StatusOK, map[string]string{"log": out})
}

// handleSetPlugin schaltet den Dienst eines installierten Plugins ein oder aus.
func (s *Server) handleSetPlugin(c echo.Context) error {
	id := c.Param("id")
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}
	ctx := c.Request().Context()

	if err := s.plugins.SetEnabled(ctx, id, req.Enabled); err != nil {
		s.audit(ctx, currentUser(c), "plugin.set", "plugin", id, "error", c.RealIP(),
			map[string]string{"fehler": err.Error()})
		return storeError(err)
	}
	s.audit(ctx, currentUser(c), "plugin.set", "plugin", id, "ok", c.RealIP(),
		map[string]bool{"enabled": req.Enabled})
	return c.NoContent(http.StatusNoContent)
}
