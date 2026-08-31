package api

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/marion909/voltpanel/internal/agent"
	"github.com/marion909/voltpanel/internal/store"
)

type stopProcessRequest struct {
	PID    int    `json:"pid"`
	Signal string `json:"signal"`
}

// handleProcesses zeigt die laufenden Prozesse.
//
// Ein Administrator sieht alle, ein Kunde nur die seiner eigenen Sites. Der
// Grund ist nicht die Liste selbst, sondern was in den Kommandozeilen steht:
// Domainnamen, Pfade, gelegentlich Argumente eines Skripts. Das ist die
// Tätigkeit anderer Mandanten.
func (s *Server) handleProcesses(c echo.Context) error {
	ctx := c.Request().Context()
	procs, err := s.agent.Processes(ctx)
	if err != nil {
		return agentError(err)
	}

	if isPrivileged(currentUser(c)) {
		return c.JSON(http.StatusOK, procs)
	}

	own, err := s.siteUsers(c)
	if err != nil {
		return storeError(err)
	}
	visible := make([]agent.ProcessInfo, 0, 16)
	for _, p := range procs {
		if own[p.User] {
			visible = append(visible, p)
		}
	}
	return c.JSON(http.StatusOK, visible)
}

// handleStopProcess beendet einen Prozess einer eigenen Site.
//
// Der Eigentümer wird hier bestimmt und nicht übernommen: der Aufrufer schickt
// nur die PID. Welcher Benutzer dazu passen muss, ergibt sich aus den Sites
// seines Mandanten — und der Agent prüft anschließend, dass der Prozess
// wirklich diesem Benutzer gehört.
func (s *Server) handleStopProcess(c echo.Context) error {
	var req stopProcessRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}

	ctx := c.Request().Context()
	procs, err := s.agent.Processes(ctx)
	if err != nil {
		return agentError(err)
	}

	owner := ""
	for _, p := range procs {
		if p.PID == req.PID {
			owner = p.User
			break
		}
	}
	if owner == "" {
		return echo.NewHTTPError(http.StatusNotFound, "diesen prozess gibt es nicht")
	}

	own, err := s.siteUsers(c)
	if err != nil {
		return storeError(err)
	}
	// Auch für Administratoren gilt die Beschränkung auf Site-Prozesse. Wer
	// nginx oder mariadb anfassen will, nimmt die Dienstverwaltung — dort ist
	// der Neustart sauber und die Unit weiß danach noch, was sie tun soll.
	if !own[owner] {
		return echo.NewHTTPError(http.StatusForbidden,
			"nur prozesse einer eigenen site lassen sich hier beenden — "+
				"für Systemdienste ist die Dienstverwaltung zuständig")
	}

	if err := s.agent.StopProcess(ctx, req.PID, owner, strings.ToUpper(req.Signal)); err != nil {
		return agentError(err)
	}
	s.audit(ctx, currentUser(c), "process.stop", "process", owner, "ok", c.RealIP(),
		map[string]any{"pid": req.PID, "signal": req.Signal})
	return c.NoContent(http.StatusNoContent)
}

// siteUsers sammelt die Systembenutzer aller Sites im Zugriffsbereich.
//
// Für Administratoren über alle Mandanten hinweg — sie sehen ohnehin jede
// Site. Elevate() gibt einer Rolle, die das nicht darf, ihren unveränderten
// Zugriffsbereich zurück; die Erweiterung lässt sich hier also nicht
// versehentlich erschleichen.
func (s *Server) siteUsers(c echo.Context) (map[string]bool, error) {
	sites, err := s.store.ListSites(c.Request().Context(), currentScope(c).Elevate())
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(sites))
	for _, site := range sites {
		if site.SystemUser != "" {
			out[site.SystemUser] = true
		}
	}
	return out, nil
}

func isPrivileged(u *store.User) bool {
	return u != nil && roleRank(u.Role) >= roleRank(store.RoleAdmin)
}
