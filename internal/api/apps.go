package api

import (
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"
	"github.com/marion909/voltpanel/internal/core"
)

// Apps: eine Anwendung ist eine systemd-Unit plus Reverse-Proxy.
//
// Der Scope entscheidet überall, was sichtbar ist — eine App gehört zu einer
// Site, und die gehört einem Mandanten.

func (s *Server) handleListApps(c echo.Context) error {
	apps, err := s.apps.ListApps(c.Request().Context(), s.scopeFor(c))
	if err != nil {
		return storeError(err)
	}
	return c.JSON(http.StatusOK, apps)
}

// handleAppRuntimes sagt, welche Laufzeitumgebungen der Server hat.
//
// Für jeden Angemeldeten, nicht nur für Administratoren: ein Kunde, der eine
// App anlegen will, muss wissen, ob Node überhaupt installiert ist. Die Antwort
// nennt einen Pfad wie /usr/bin/node und eine Versionsnummer — das steht auf
// jedem Server der Welt so und sagt nichts über diesen.
func (s *Server) handleAppRuntimes(c echo.Context) error {
	runtimes, err := s.apps.Runtimes(c.Request().Context())
	if err != nil {
		return storeError(err)
	}
	return c.JSON(http.StatusOK, runtimes)
}

// handleDockerStatus sagt, ob Docker läuft und wie sicher es steht.
//
// Nur für Administratoren: die Antwort nennt die Fassung des Daemons und ob er
// mit Benutzernamensraum-Abbildung läuft. Das ist Auskunft über die Maschine,
// nicht über die Site — und der Hinweis, was daran zu ändern wäre, richtet sich
// ohnehin an den Betreiber.
func (s *Server) handleDockerStatus(c echo.Context) error {
	st, err := s.apps.DockerStatus(c.Request().Context())
	if err != nil {
		return storeError(err)
	}
	return c.JSON(http.StatusOK, st)
}

// handlePullImage holt ein Image vorab.
func (s *Server) handlePullImage(c echo.Context) error {
	var req struct {
		Image string `json:"image"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}
	ctx := c.Request().Context()
	out, err := s.apps.PullImage(ctx, req.Image)
	if err != nil {
		return storeError(err)
	}
	s.audit(ctx, currentUser(c), "app.pull", "image", req.Image, "ok", c.RealIP(), nil)
	return c.JSON(http.StatusOK, map[string]string{"log": out})
}

// handleAppLogs liefert die letzten Zeilen eines Containers.
func (s *Server) handleAppLogs(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	lines, _ := strconv.Atoi(c.QueryParam("lines"))
	out, err := s.apps.ContainerLogs(c.Request().Context(), s.scopeFor(c), id, lines)
	if err != nil {
		return storeError(err)
	}
	return c.JSON(http.StatusOK, map[string]string{"log": out})
}

func (s *Server) handleCreateApp(c echo.Context) error {
	var in core.AppInput
	if err := c.Bind(&in); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}

	ctx := c.Request().Context()
	app, err := s.apps.CreateApp(ctx, s.scopeFor(c), in)
	if err != nil {
		return storeError(err)
	}
	s.audit(ctx, currentUser(c), "app.create", "app", app.Name, "ok", c.RealIP(),
		map[string]any{"runtime": app.Runtime, "port": app.Port})
	return c.JSON(http.StatusCreated, app)
}

func (s *Server) handleUpdateApp(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	var in core.AppInput
	if err := c.Bind(&in); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}

	ctx := c.Request().Context()
	app, err := s.apps.UpdateApp(ctx, s.scopeFor(c), id, in)
	if err != nil {
		return storeError(err)
	}
	s.audit(ctx, currentUser(c), "app.update", "app", app.Name, "ok", c.RealIP(),
		map[string]any{"runtime": app.Runtime, "aktiv": app.Enabled})
	return c.JSON(http.StatusOK, app)
}

func (s *Server) handleDeleteApp(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	if err := s.apps.DeleteApp(ctx, s.scopeFor(c), id); err != nil {
		return storeError(err)
	}
	s.audit(ctx, currentUser(c), "app.delete", "app", pathParam(c, "id"), "ok", c.RealIP(), nil)
	return c.NoContent(http.StatusNoContent)
}
