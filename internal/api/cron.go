package api

import (
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"
	"github.com/marion909/voltpanel/internal/core"
)

func (s *Server) handleListCronjobs(c echo.Context) error {
	jobs, err := s.store.ListCronjobs(c.Request().Context(), s.scopeFor(c))
	if err != nil {
		return storeError(err)
	}
	return c.JSON(http.StatusOK, jobs)
}

type createCronjobRequest struct {
	Name     string `json:"name"`
	Schedule string `json:"schedule"`
	Command  string `json:"command"`
	SiteID   *int64 `json:"site_id"`
	Enabled  *bool  `json:"enabled"`
	TenantID int64  `json:"tenant_id"`
}

func (s *Server) handleCreateCronjob(c echo.Context) error {
	var req createCronjobRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}
	// Ein neuer Job ist standardmäßig aktiv — wer ihn erst später starten will,
	// schickt enabled:false mit.
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	ctx := c.Request().Context()
	job, err := s.cron.CreateCronjob(ctx, currentScope(c), core.CreateCronjobInput{
		Name: req.Name, Schedule: req.Schedule, Command: req.Command,
		SiteID: req.SiteID, Enabled: enabled, TenantID: req.TenantID,
	})
	if err != nil {
		s.audit(ctx, currentUser(c), "cron.create", "cronjob", req.Name, "error", c.RealIP(),
			map[string]string{"fehler": err.Error()})
		return storeError(err)
	}

	s.audit(ctx, currentUser(c), "cron.create", "cronjob", job.Name, "ok", c.RealIP(),
		map[string]any{"zeitplan": job.Schedule, "benutzer": job.RunAs})
	return c.JSON(http.StatusCreated, job)
}

type updateCronjobRequest struct {
	Name     *string `json:"name"`
	Schedule *string `json:"schedule"`
	Command  *string `json:"command"`
	Enabled  *bool   `json:"enabled"`
}

func (s *Server) handleUpdateCronjob(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	var req updateCronjobRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}

	ctx, sc := c.Request().Context(), currentScope(c)
	job, err := s.store.GetCronjob(ctx, sc, id)
	if err != nil {
		return storeError(err)
	}

	applyIf(req.Name, &job.Name)
	applyIf(req.Schedule, &job.Schedule)
	applyIf(req.Command, &job.Command)
	applyIf(req.Enabled, &job.Enabled)

	if err := s.cron.UpdateCronjob(ctx, sc, job); err != nil {
		return storeError(err)
	}
	s.audit(ctx, currentUser(c), "cron.update", "cronjob", job.Name, "ok", c.RealIP(),
		map[string]bool{"aktiv": job.Enabled})
	return c.JSON(http.StatusOK, job)
}

func (s *Server) handleDeleteCronjob(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}

	ctx, sc := c.Request().Context(), currentScope(c)
	job, err := s.store.GetCronjob(ctx, sc, id)
	if err != nil {
		return storeError(err)
	}
	if err := s.cron.DeleteCronjob(ctx, sc, id); err != nil {
		return storeError(err)
	}
	s.audit(ctx, currentUser(c), "cron.delete", "cronjob", job.Name, "ok", c.RealIP(), nil)
	return c.NoContent(http.StatusNoContent)
}

func (s *Server) handleCronjobLog(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	lines, _ := strconv.Atoi(c.QueryParam("lines"))

	text, err := s.cron.Log(c.Request().Context(), currentScope(c), id, lines)
	if err != nil {
		return storeError(err)
	}
	return c.JSON(http.StatusOK, map[string]string{"content": text})
}
