package api

import (
	"context"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/marion909/voltpanel/internal/core"
)

// handleUpdateStatus meldet, ob der Kanal etwas Neueres führt.
//
// Antwortet auch dann mit 200, wenn der Kanal nicht erreichbar war: dass das
// Panel nicht weiß, ob es ein Update gibt, ist keine fehlgeschlagene Anfrage.
// Der Grund steht im Feld error und gehört in die Anzeige.
func (s *Server) handleUpdateStatus(c echo.Context) error {
	force := c.QueryParam("refresh") == "1"

	ctx, cancel := context.WithTimeout(c.Request().Context(), 20*time.Second)
	defer cancel()

	return c.JSON(http.StatusOK, s.updater().UpdateStatus(ctx, force))
}

// handleUpdateStart stößt das Update an.
//
// Der Web-Prozess tauscht nichts selbst — er darf es nicht: die Binaries
// liegen root gehören. Er ruft die typisierte Operation beim Agent auf, und
// die nimmt keine Quelle entgegen. Welche Version kommt, steht im Kanal.
func (s *Server) handleUpdateStart(c echo.Context) error {
	user := currentUser(c)
	status := s.updater().UpdateStatus(c.Request().Context(), true)
	if status.Error != "" {
		return echo.NewHTTPError(http.StatusServiceUnavailable,
			"update-kanal nicht erreichbar: "+status.Error)
	}
	if !status.Available {
		return echo.NewHTTPError(http.StatusConflict,
			"es liegt keine neuere version vor ("+status.Current+")")
	}

	// Vor dem Aufruf ins Protokoll: reißt die Verbindung beim Neustart ab,
	// steht wenigstens fest, wer das Update ausgelöst hat und worauf.
	ctx := c.Request().Context()
	s.audit(ctx, user, "system.update", "panel", status.Latest, "ok", c.RealIP(),
		map[string]string{"von": status.Current, "auf": status.Latest})

	res, err := s.agent.SystemUpdate(ctx)
	if err != nil {
		return storeError(err)
	}
	return c.JSON(http.StatusOK, res)
}

func (s *Server) updater() *core.Updater {
	return core.NewUpdater(s.cfg, s.store, s.log)
}
