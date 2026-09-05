package api

import (
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"
	"github.com/marion909/voltpanel/internal/agent"
)

// Firewall und Fail2ban.
//
// Alles hier ist Administratoren vorbehalten, und zwar ohne Ausnahme. Eine
// Regel in der Firewall betrifft den ganzen Server, nicht eine Site; und die
// Liste der gesperrten Adressen sagt einem Kunden nichts über seinen Mandanten,
// aber allerlei über die anderen.

func (s *Server) handleFirewallStatus(c echo.Context) error {
	st, err := s.agent.FirewallStatusOf(c.Request().Context())
	if err != nil {
		return storeError(err)
	}
	return c.JSON(http.StatusOK, st)
}

// handleFirewallRule setzt oder entfernt eine Regel.
//
// Die Regel kommt in Teilen, nicht als Zeichenkette — der Agent baut daraus die
// Kommandozeile. Ein Textfeld hieße, ufws eigene Sprache durchzureichen.
func (s *Server) handleFirewallRule(c echo.Context) error {
	var p agent.FirewallRuleParams
	if err := c.Bind(&p); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}

	ctx := c.Request().Context()
	out, err := s.agent.SetFirewallRule(ctx, p)
	if err != nil {
		return storeError(err)
	}
	s.audit(ctx, currentUser(c), "firewall.rule", "port", ruleLabel(p), "ok", c.RealIP(),
		map[string]any{"aktion": p.Action, "entfernt": p.Remove})
	return c.JSON(http.StatusOK, map[string]string{"log": out})
}

// ruleLabel ist die Regel für das Audit-Log, kurz und lesbar.
func ruleLabel(p agent.FirewallRuleParams) string {
	label := strconv.Itoa(p.Port)
	if p.PortTo > 0 {
		label += ":" + strconv.Itoa(p.PortTo)
	}
	return label + "/" + p.Proto
}

func (s *Server) handleFail2banStatus(c echo.Context) error {
	st, err := s.agent.Fail2banStatusOf(c.Request().Context())
	if err != nil {
		return storeError(err)
	}
	return c.JSON(http.StatusOK, st)
}

// handlePortScanStatus sagt, ob die Port-Scan-Erkennung steht.
func (s *Server) handlePortScanStatus(c echo.Context) error {
	st, err := s.agent.PortScanStatusOf(c.Request().Context())
	if err != nil {
		return storeError(err)
	}
	return c.JSON(http.StatusOK, st)
}

// handlePortScanSet schaltet sie ein oder aus.
//
// Die Whitelist kommt aus der config.yaml und nicht aus der Anfrage. Wer sie
// über die Leitung setzen dürfte, könnte sich selbst von der Sperre ausnehmen
// — und das ist genau die Zeile, die ein Angreifer als erste schriebe.
func (s *Server) handlePortScanSet(c echo.Context) error {
	var req struct {
		Enabled bool   `json:"enabled"`
		Level   string `json:"level"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}

	ctx := c.Request().Context()
	out, err := s.agent.SetPortScan(ctx, agent.PortScanParams{
		Enabled:   req.Enabled,
		Level:     req.Level,
		IgnoreIPs: s.cfg.IPWhitelist,
	})
	if err != nil {
		return storeError(err)
	}
	s.audit(ctx, currentUser(c), "firewall.portscan", "stufe", req.Level, "ok", c.RealIP(),
		map[string]any{"aktiv": req.Enabled})
	return c.JSON(http.StatusOK, map[string]string{"log": out})
}

// handleUnban hebt eine Sperre auf.
//
// Der häufigste Fall im Betrieb: jemand hat sein Passwort dreimal falsch
// eingegeben und kommt jetzt gar nicht mehr an den Server.
func (s *Server) handleUnban(c echo.Context) error {
	var req struct {
		Jail string `json:"jail"`
		IP   string `json:"ip"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}

	ctx := c.Request().Context()
	out, err := s.agent.Unban(ctx, req.Jail, req.IP)
	if err != nil {
		return storeError(err)
	}
	s.audit(ctx, currentUser(c), "fail2ban.unban", "ip", req.IP, "ok", c.RealIP(),
		map[string]string{"jail": req.Jail})
	return c.JSON(http.StatusOK, map[string]string{"log": out})
}
