package api

import (
	"net/http"
	"net/url"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
)

const (
	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
	pingPeriod = (pongWait * 9) / 10
)

// handleMetricsStream schiebt die Messwerte per WebSocket ans Dashboard.
func (s *Server) handleMetricsStream(c echo.Context) error {
	upgrader := websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 8192,
		// Ohne diese Prüfung könnte eine fremde Seite den Stream öffnen und
		// mitlesen — der Session-Cookie würde beim Upgrade mitgeschickt.
		CheckOrigin: s.checkWebsocketOrigin,
	}

	conn, err := upgrader.Upgrade(c.Response(), c.Request(), nil)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "websocket-upgrade fehlgeschlagen")
	}
	defer conn.Close()

	updates, unsubscribe := s.metrics.Subscribe()
	defer unsubscribe()

	// Der Leser verwirft eingehende Nachrichten und hält die Pong-Frist frisch.
	// Ohne ihn merkt der Server nicht, wenn der Browser weg ist.
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn.SetReadLimit(512)
		_ = conn.SetReadDeadline(time.Now().Add(pongWait))
		conn.SetPongHandler(func(string) error {
			return conn.SetReadDeadline(time.Now().Add(pongWait))
		})
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	// Sofort den letzten Stand schicken, damit das Dashboard nicht leer startet.
	_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
	if err := conn.WriteJSON(s.metrics.Latest()); err != nil {
		return nil
	}

	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()

	ctx := c.Request().Context()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-done:
			return nil
		case snap, ok := <-updates:
			if !ok {
				return nil
			}
			_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := conn.WriteJSON(snap); err != nil {
				return nil
			}
		case <-ticker.C:
			_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return nil
			}
		}
	}
}

// checkWebsocketOrigin lässt nur Verbindungen von derselben Seite zu — und im
// Entwicklungsbetrieb zusätzlich den Vite-Server.
func (s *Server) checkWebsocketOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		// Kein Origin heißt: kein Browser. Dann greift auch kein CSRF-Szenario.
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if u.Host == r.Host {
		return true
	}
	return s.devOrigin != "" && origin == s.devOrigin
}
