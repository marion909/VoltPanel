package api

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"
	"github.com/marion909/voltpanel/internal/core"
)

// Git-Deploy.
//
// Alles hier braucht eine Sitzung — bis auf den Webhook. Der liegt bewusst
// woanders: außerhalb des Zugriffspfads, weil er sich über sein eigenes
// Geheimnis ausweist und seine Adresse in den Einstellungen eines fremden
// Dienstes landet.

func (s *Server) handleListDeploys(c echo.Context) error {
	list, err := s.deploys.List(c.Request().Context(), s.scopeFor(c))
	if err != nil {
		return storeError(err)
	}
	return c.JSON(http.StatusOK, list)
}

func (s *Server) handleDeploySteps(c echo.Context) error {
	return c.JSON(http.StatusOK, s.deploys.StepNames())
}

// handleConfigureDeploy legt einen Deploy an oder ändert ihn.
//
// Beim Anlegen kommt das Geheimnis für die Signatur einmal zurück. Danach nie
// wieder — es liegt verschlüsselt, und es erneut herauszugeben hieße, ein
// Geheimnis aus der Datenbank zu holen, das dort für den Server liegt und
// nicht für den Betrachter.
func (s *Server) handleConfigureDeploy(c echo.Context) error {
	var in core.DeployInput
	if err := c.Bind(&in); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}

	ctx := c.Request().Context()
	d, secret, err := s.deploys.Configure(ctx, s.scopeFor(c), in)
	if err != nil {
		return storeError(err)
	}
	// Die Route hat kein :id — das Ziel ist die Site aus dem Rumpf. Mit
	// pathParam stünde hier ein Leerstring, und das Audit-Log sagte nicht,
	// worum es ging.
	s.audit(ctx, currentUser(c), "deploy.configure", "site", strconv.FormatInt(d.SiteID, 10),
		"ok", c.RealIP(), map[string]any{"repo": d.RepoURL, "ref": d.Ref})

	out := map[string]any{"deploy": d, "hook_url": s.deploys.HookURL(d.HookID)}
	if secret != "" {
		out["hook_secret"] = secret
		out["hook_secret_note"] = "Dieses Geheimnis wird nur jetzt angezeigt."
	}
	return c.JSON(http.StatusOK, out)
}

// handleRunDeploy stößt einen Deploy an und kommt sofort zurück.
//
// Ein Build dauert Minuten. Die Anfrage darauf warten zu lassen hieße, sie in
// jeden Zeitüberlauf zwischen Browser, Proxy und Server laufen zu lassen — und
// ein abgebrochener Build hinterlässt ein halbes node_modules.
func (s *Server) handleRunDeploy(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	d, err := s.store.GetDeploy(ctx, s.scopeFor(c), id)
	if err != nil {
		return storeError(err)
	}
	if err := s.deploys.RunAsync(d); err != nil {
		if errors.Is(err, core.ErrDeployRunning) {
			return echo.NewHTTPError(http.StatusConflict, err.Error())
		}
		return storeError(err)
	}
	s.audit(ctx, currentUser(c), "deploy.run", "site", pathParam(c, "id"), "ok", c.RealIP(), nil)
	return c.JSON(http.StatusAccepted, map[string]string{"status": "running"})
}

func (s *Server) handleDeployReleases(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	d, err := s.store.GetDeploy(ctx, s.scopeFor(c), id)
	if err != nil {
		return storeError(err)
	}
	res, err := s.deploys.Releases(ctx, s.scopeFor(c), d.SiteID)
	if err != nil {
		return storeError(err)
	}
	return c.JSON(http.StatusOK, res)
}

type rollbackRequest struct {
	Release string `json:"release"`
}

func (s *Server) handleDeployRollback(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	var req rollbackRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}

	ctx := c.Request().Context()
	d, err := s.store.GetDeploy(ctx, s.scopeFor(c), id)
	if err != nil {
		return storeError(err)
	}
	if err := s.deploys.Rollback(ctx, s.scopeFor(c), d.SiteID, req.Release); err != nil {
		return storeError(err)
	}
	s.audit(ctx, currentUser(c), "deploy.rollback", "site", pathParam(c, "id"), "ok",
		c.RealIP(), map[string]string{"stand": req.Release})
	return c.JSON(http.StatusOK, map[string]string{"release": req.Release})
}

// handleDeployKey liefert den öffentlichen Schlüssel zum Eintragen beim Hoster.
// Der private Teil verlässt den Server nie.
func (s *Server) handleDeployKey(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	d, err := s.store.GetDeploy(ctx, s.scopeFor(c), id)
	if err != nil {
		return storeError(err)
	}
	key, err := s.deploys.DeployKey(ctx, s.scopeFor(c), d.SiteID)
	if err != nil {
		return storeError(err)
	}
	return c.JSON(http.StatusOK, key)
}

func (s *Server) handleDeleteDeploy(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	if err := s.store.DeleteDeploy(ctx, s.scopeFor(c), id); err != nil {
		return storeError(err)
	}
	s.audit(ctx, currentUser(c), "deploy.delete", "deploy", pathParam(c, "id"), "ok",
		c.RealIP(), nil)
	return c.NoContent(http.StatusNoContent)
}

// hookMaxBody begrenzt, was ein Webhook schicken darf. GitHub schickt für einen
// Push ein paar Kilobyte; alles darüber ist nichts, was hier gelesen werden
// müsste, und ohne Grenze wäre der Endpunkt ein Weg, Speicher zu belegen.
const hookMaxBody = 1 << 20

// hookHeaders sind die Kopfzeilen, aus denen die Signatur kommen kann.
var hookHeaders = []string{"X-Hub-Signature-256", "X-Gitea-Signature", "X-Gitlab-Token"}

// handleDeployHook nimmt den Webhook eines Hosters entgegen.
//
// Eine IP-Whitelist am Panel sperrt auch diesen Endpunkt aus, und das bleibt
// so. Ein Loch in die Whitelist für einen Endpunkt ohne Sitzung wäre genau die
// Ausnahme, wegen der die Whitelist danach nichts mehr wert ist; wer sie setzt,
// muss den Hoster hineinnehmen oder von Hand deployen.
//
// Ohne Sitzung, ohne CSRF-Token, außerhalb des Zugriffspfads — der Ausweis ist
// die Signatur über den Rumpf.
//
// Die Antwort auf jeden Fehlerfall ist dieselbe: 404, ohne Unterschied
// zwischen "diese Adresse gibt es nicht" und "die Signatur passt nicht". Sonst
// wäre der Endpunkt ein Weg, gültige Hook-Adressen durch Ausprobieren zu finden.
func (s *Server) handleDeployHook(c echo.Context) error {
	body, err := io.ReadAll(io.LimitReader(c.Request().Body, hookMaxBody))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "nicht gefunden")
	}

	headers := make(map[string]string, len(hookHeaders))
	for _, name := range hookHeaders {
		if v := c.Request().Header.Get(name); v != "" {
			headers[name] = v
		}
	}

	ctx := c.Request().Context()
	msg, err := s.deploys.HandleHook(ctx, c.Param("hook"), headers, body)
	if err != nil {
		if errors.Is(err, core.ErrHookAbgelehnt) {
			// Nicht protokolliert mit der Adresse: ein Log voller geratener
			// Hook-Adressen wäre selbst eine Liste zum Nachlesen.
			s.log.Debug("webhook abgelehnt", "ip", c.RealIP())
			return echo.NewHTTPError(http.StatusNotFound, "nicht gefunden")
		}
		if errors.Is(err, core.ErrDeployRunning) {
			return echo.NewHTTPError(http.StatusConflict, err.Error())
		}
		return storeError(err)
	}
	return c.JSON(http.StatusAccepted, map[string]string{"status": msg})
}
