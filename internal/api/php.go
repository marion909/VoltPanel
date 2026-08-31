package api

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// handlePHPExtensions listet die Module einer PHP-Version.
//
// Erweiterungen gelten systemweit je Version, nicht je Site: wer imagick
// installiert, installiert es für alle Sites derselben Version. Deshalb liegt
// die Verwaltung bei den Server-Diensten und nicht in der Site-Ansicht.
func (s *Server) handlePHPExtensions(c echo.Context) error {
	version := c.Param("version")

	exts, err := s.agent.PHPExtensions(c.Request().Context(), version)
	if err != nil {
		return storeError(err)
	}
	return c.JSON(http.StatusOK, exts)
}

type phpExtRequest struct {
	Name   string `json:"name"`
	Enable *bool  `json:"enable"`
}

// handlePHPExtensionInstall holt ein Modul über die Paketverwaltung nach.
func (s *Server) handlePHPExtensionInstall(c echo.Context) error {
	version := c.Param("version")

	var req phpExtRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "unlesbare anfrage")
	}

	ctx := c.Request().Context()
	if err := s.agent.InstallPHPExtension(ctx, version, req.Name); err != nil {
		return storeError(err)
	}
	s.audit(ctx, currentUser(c), "php.extension_install", "php", version+"/"+req.Name,
		"ok", c.RealIP(), nil)

	exts, err := s.agent.PHPExtensions(ctx, version)
	if err != nil {
		return storeError(err)
	}
	return c.JSON(http.StatusOK, exts)
}

// handlePHPExtensionToggle schaltet ein installiertes Modul an oder ab. Das
// Paket bleibt dabei liegen — die Umstellung ist umkehrbar.
func (s *Server) handlePHPExtensionToggle(c echo.Context) error {
	version := c.Param("version")

	var req phpExtRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "unlesbare anfrage")
	}
	if req.Enable == nil {
		return echo.NewHTTPError(http.StatusBadRequest, "enable fehlt")
	}

	ctx := c.Request().Context()
	if err := s.agent.TogglePHPExtension(ctx, version, req.Name, *req.Enable); err != nil {
		return storeError(err)
	}

	action := "php.extension_disable"
	if *req.Enable {
		action = "php.extension_enable"
	}
	s.audit(ctx, currentUser(c), action, "php", version+"/"+req.Name, "ok", c.RealIP(), nil)

	exts, err := s.agent.PHPExtensions(ctx, version)
	if err != nil {
		return storeError(err)
	}
	return c.JSON(http.StatusOK, exts)
}
