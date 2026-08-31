package api

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
)

// terminalRead ist die Frist für eine einzelne Nachricht aus dem Browser. Sie
// ist großzügig: zwischen zwei Tastendrücken darf lange nichts passieren. Die
// eigentliche Leerlaufgrenze zieht der Agent.
const terminalRead = 60 * time.Minute

// terminalControl ist eine Steuernachricht des Browsers. Tastatureingaben
// kommen als Binärnachricht, alles andere als JSON — so lässt sich beides auf
// derselben Verbindung unterscheiden, ohne ein eigenes Rahmenformat zu bauen.
type terminalControl struct {
	Resize *struct {
		Cols int `json:"cols"`
		Rows int `json:"rows"`
	} `json:"resize"`
}

// handleTerminal verbindet den Browser mit einer Shell auf dem Server.
//
// Die Shell läuft als Systembenutzer der Site — nie als root und nie als ein
// Benutzer, den der Browser benennen könnte. Beides ergibt sich aus der Site,
// die im Namen des angemeldeten Kontos aufgelöst wurde.
func (s *Server) handleTerminal(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}

	ctx := c.Request().Context()
	site, err := s.store.GetSite(ctx, currentScope(c), id)
	if err != nil {
		return storeError(err)
	}
	if site.SystemUser == "" {
		return echo.NewHTTPError(http.StatusBadRequest,
			"diese site hat keinen systembenutzer — bitte neu erzeugen lassen")
	}

	cols, rows := queryInt(c, "cols", 80), queryInt(c, "rows", 24)
	session, err := s.agent.OpenTerminal(ctx, site.SystemUser, site.RootPath, cols, rows)
	if err != nil {
		return agentError(err)
	}

	// Ab hier steht eine Shell. Was auch immer schiefgeht, sie muss wieder weg.
	defer func() {
		if err := s.agent.CloseTerminal(context.WithoutCancel(ctx), session.Session); err != nil {
			s.log.Warn("terminal nicht geschlossen", "sitzung", session.Session, "err", err)
		}
	}()

	pipe, err := net.DialTimeout("unix", session.Socket, 5*time.Second)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadGateway, "die sitzung ist nicht erreichbar")
	}
	defer pipe.Close()

	upgrader := websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		// Ohne die Prüfung könnte eine fremde Seite eine Shell öffnen — der
		// Session-Cookie ginge beim Upgrade automatisch mit.
		CheckOrigin: s.checkWebsocketOrigin,
	}
	conn, err := upgrader.Upgrade(c.Response(), c.Request(), nil)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "websocket-upgrade fehlgeschlagen")
	}
	defer conn.Close()

	s.audit(ctx, currentUser(c), "terminal.open", "site", site.Domain, "ok", c.RealIP(),
		map[string]string{"benutzer": site.SystemUser})

	done := make(chan struct{}, 2)
	go s.terminalToBrowser(conn, pipe, done)
	go s.browserToTerminal(conn, pipe, session.Session, done)
	<-done
	return nil
}

// terminalToBrowser schiebt die Ausgabe der Shell als Binärnachrichten weiter.
func (s *Server) terminalToBrowser(conn *websocket.Conn, pipe net.Conn, done chan<- struct{}) {
	defer func() { done <- struct{}{} }()

	buf := make([]byte, 4096)
	for {
		n, err := pipe.Read(buf)
		if n > 0 {
			_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := conn.WriteMessage(websocket.BinaryMessage, buf[:n]); err != nil {
				return
			}
		}
		if err != nil {
			// Die Shell ist beendet. Der Browser soll das sehen und nicht auf
			// eine Verbindung starren, hinter der nichts mehr ist.
			_ = conn.WriteMessage(websocket.TextMessage,
				[]byte(`{"closed":"die sitzung wurde beendet"}`))
			return
		}
	}
}

// browserToTerminal reicht Tastatureingaben durch und führt Steuernachrichten
// aus. Text ist immer Steuerung, Binär ist immer Eingabe.
func (s *Server) browserToTerminal(conn *websocket.Conn, pipe net.Conn, session string, done chan<- struct{}) {
	defer func() { done <- struct{}{} }()

	// Eine Tastatureingabe ist klein. Die Grenze verhindert, dass ein
	// eingefügter Text von Megabytegröße den Web-Prozess belegt.
	conn.SetReadLimit(64 << 10)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(terminalRead))
		kind, data, err := conn.ReadMessage()
		if err != nil {
			return
		}

		if kind == websocket.BinaryMessage {
			if _, err := pipe.Write(data); err != nil {
				return
			}
			continue
		}

		var msg terminalControl
		if err := json.Unmarshal(data, &msg); err != nil || msg.Resize == nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = s.agent.ResizeTerminal(ctx, session, msg.Resize.Cols, msg.Resize.Rows)
		cancel()
		if err != nil {
			s.log.Debug("fenstergröße nicht übernommen", "sitzung", session, "err", err)
		}
	}
}

func queryInt(c echo.Context, name string, fallback int) int {
	v, err := strconv.Atoi(c.QueryParam(name))
	if err != nil {
		return fallback
	}
	return v
}
