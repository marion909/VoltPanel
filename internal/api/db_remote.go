package api

import (
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"
)

// Herkunftsliste eines Datenbankbenutzers: von welchen Adressen aus er sich
// anmelden darf.

func (s *Server) handleListRemoteHosts(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	hosts, err := s.databases.ListRemoteHosts(c.Request().Context(), currentScope(c), id)
	if err != nil {
		return storeError(err)
	}
	return c.JSON(http.StatusOK, hosts)
}

type remoteHostRequest struct {
	Host string `json:"host"`
	Note string `json:"note"`
}

func (s *Server) handleAddRemoteHost(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	var req remoteHostRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}

	ctx := c.Request().Context()
	entry, err := s.databases.AddRemoteHost(ctx, currentScope(c), id, req.Host, req.Note)
	user := currentUser(c)
	if err != nil {
		s.audit(ctx, user, "database.remote_add", "db_user", strconv.FormatInt(id, 10),
			"error", c.RealIP(), map[string]string{"herkunft": req.Host, "fehler": err.Error()})
		return storeError(err)
	}

	// Die Herkunft steht im Audit-Log: sie ist die Antwort auf die Frage, wer
	// von wo an die Datenbank durfte — und ab wann.
	s.audit(ctx, user, "database.remote_add", "db_user", strconv.FormatInt(id, 10),
		"ok", c.RealIP(), map[string]string{"herkunft": entry.Host, "notiz": entry.Note})
	return c.JSON(http.StatusCreated, entry)
}

func (s *Server) handleRemoveRemoteHost(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	ctx, sc := c.Request().Context(), currentScope(c)

	entry, err := s.store.GetRemoteHost(ctx, sc, id)
	if err != nil {
		return storeError(err)
	}
	if err := s.databases.RemoveRemoteHost(ctx, sc, id); err != nil {
		return storeError(err)
	}
	s.audit(ctx, currentUser(c), "database.remote_remove", "db_user",
		strconv.FormatInt(entry.DBUserID, 10), "ok", c.RealIP(),
		map[string]string{"herkunft": entry.Host})
	return c.NoContent(http.StatusNoContent)
}

// handleRemoteStatus sagt, ob MariaDB überhaupt Verbindungen von außen annimmt.
//
// Für jeden Angemeldeten lesbar: ohne diese Auskunft wäre in der Oberfläche
// nicht zu erklären, warum ein eingetragener Zugang nicht funktioniert. Sie
// verrät nichts, was nicht ein Verbindungsversuch auf Port 3306 auch zeigt.
func (s *Server) handleRemoteStatus(c echo.Context) error {
	status, err := s.databases.RemoteStatus(c.Request().Context())
	if err != nil {
		return agentError(err)
	}
	return c.JSON(http.StatusOK, status)
}

type remoteAccessRequest struct {
	Enabled bool `json:"enabled"`
}

// handleSetRemoteAccess stellt den Datenbankserver ins Netz — oder zurück.
//
// Nur für Administratoren, und das ist keine Formsache: die Entscheidung gilt
// für alle Mandanten auf diesem Server gleichzeitig, sie startet MariaDB neu,
// und sie öffnet einen Port nach außen.
func (s *Server) handleSetRemoteAccess(c echo.Context) error {
	var req remoteAccessRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}

	ctx := c.Request().Context()
	status, err := s.databases.SetRemoteAccess(ctx, req.Enabled)
	zustand := "aus"
	if req.Enabled {
		zustand = "an"
	}
	if err != nil {
		s.audit(ctx, currentUser(c), "database.remote_access", "service", "mariadb",
			"error", c.RealIP(), map[string]string{"ziel": zustand, "fehler": err.Error()})
		return agentError(err)
	}
	s.audit(ctx, currentUser(c), "database.remote_access", "service", "mariadb",
		"ok", c.RealIP(), map[string]any{"ziel": zustand, "bind_address": status.BindAddress})
	return c.JSON(http.StatusOK, status)
}
