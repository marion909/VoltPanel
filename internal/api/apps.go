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

// handleAppStats liefert den Verbrauch der eigenen Container.
//
// Im Scope des Anfragenden, nicht als Serverauskunft: ein Kunde sieht, was
// seine Apps ziehen, und sonst nichts. Ein Server ohne Docker liefert eine
// leere Liste statt eines Fehlers — die Übersicht soll auch dort stehen.
func (s *Server) handleAppStats(c echo.Context) error {
	st, err := s.apps.ContainerStats(c.Request().Context(), s.scopeFor(c))
	if err != nil {
		return storeError(err)
	}
	return c.JSON(http.StatusOK, st)
}

// handleImages listet die Images des Servers.
//
// Administratoren vorbehalten. Ein Image trägt keinen Mandanten — es liegt
// einmal auf der Platte und wird von jedem benutzt, der es angibt. Die Liste
// aufzuteilen ginge nicht ehrlich, also bekommt sie, wen sie angeht.
func (s *Server) handleImages(c echo.Context) error {
	list, err := s.apps.Images(c.Request().Context())
	if err != nil {
		return storeError(err)
	}
	return c.JSON(http.StatusOK, list)
}

// handleRemoveImage entfernt ein Image, an dem keine App hängt.
func (s *Server) handleRemoveImage(c echo.Context) error {
	var req struct {
		Ref string `json:"ref"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}
	ctx := c.Request().Context()
	out, err := s.apps.RemoveImage(ctx, req.Ref)
	if err != nil {
		s.audit(ctx, currentUser(c), "app.image.remove", "image", req.Ref,
			"error", c.RealIP(), map[string]string{"fehler": err.Error()})
		return storeError(err)
	}
	s.audit(ctx, currentUser(c), "app.image.remove", "image", req.Ref, "ok", c.RealIP(), nil)
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

// handleNodeVersions sagt, welche Node-Fassungen installiert sind.
func (s *Server) handleNodeVersions(c echo.Context) error {
	list, err := s.apps.NodeVersions(c.Request().Context())
	if err != nil {
		return storeError(err)
	}
	return c.JSON(http.StatusOK, list)
}

// handleInstallNode holt eine Fassung.
//
// Nur für Administratoren: eine Node-Fassung liegt systemweit und gilt für
// alle Mandanten. Wer sie installiert, belegt Platte für den ganzen Server.
func (s *Server) handleInstallNode(c echo.Context) error {
	var req struct {
		Version string `json:"version"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}
	ctx := c.Request().Context()
	v, err := s.apps.InstallNode(ctx, req.Version)
	if err != nil {
		return storeError(err)
	}
	s.audit(ctx, currentUser(c), "node.install", "node", req.Version, "ok", c.RealIP(), nil)
	return c.JSON(http.StatusCreated, v)
}

func (s *Server) handleRemoveNode(c echo.Context) error {
	major, err := strconv.Atoi(c.Param("major"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "keine hauptversion")
	}
	ctx := c.Request().Context()
	if err := s.apps.RemoveNode(ctx, s.scopeFor(c), major); err != nil {
		return storeError(err)
	}
	s.audit(ctx, currentUser(c), "node.remove", "node", c.Param("major"), "ok", c.RealIP(), nil)
	return c.NoContent(http.StatusNoContent)
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
