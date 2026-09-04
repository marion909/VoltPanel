package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// startDualConnAgent nimmt beliebig viele Verbindungen an (der echte Agent
// tut das auch) und beantwortet auf jeder Anfragen einzeln: OpFeatureInstall
// erst nach delay, alles andere sofort. Das genügt, um zu prüfen, ob Client
// zwei unabhängige Verbindungen benutzt statt einer gemeinsamen.
func startDualConnAgent(t *testing.T, delay time.Duration) string {
	t.Helper()

	dir, err := os.MkdirTemp("/tmp", "volt-client-dual")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	socket := filepath.Join(dir, "a.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}

	handle := func(conn net.Conn) {
		defer conn.Close()
		reader := bufio.NewReader(conn)
		writer := bufio.NewWriter(conn)

		var hello Hello
		if err := json.NewDecoder(reader).Decode(&hello); err != nil {
			return
		}
		if err := writeJSON(writer, HelloAck{Protocol: ProtocolVersion, Agent: "test", OK: true}); err != nil {
			return
		}

		for {
			var req Request
			if err := json.NewDecoder(reader).Decode(&req); err != nil {
				return
			}
			if req.Op == OpFeatureInstall {
				time.Sleep(delay)
				body, _ := json.Marshal(TextResult{Text: "redis installiert"})
				if err := writeJSON(writer, Response{ID: req.ID, OK: true, Result: body}); err != nil {
					return
				}
				continue
			}
			body, _ := json.Marshal(TextResult{})
			if err := writeJSON(writer, Response{ID: req.ID, OK: true, Result: body}); err != nil {
				return
			}
		}
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go handle(conn)
		}
	}()

	t.Cleanup(func() {
		_ = ln.Close()
		<-done
	})
	return socket
}

// TestSlowCallsNutzenEigeneVerbindung deckt den Fund ab, dass Call() die
// Verbindung für ihre gesamte Dauer sperrte: mehrere Operationen im
// Agent-Paket haben mehrminütige Zeitlimits (SystemUpdate, PHP-Erweiterungs-
// /Feature-/WordPress-/Webmail-Installation) — über eine gemeinsame,
// mutex-serialisierte Verbindung hätte eine laufende Installation jeden
// anderen Agent-Aufruf im gesamten Panel für ihre volle Dauer blockiert.
// Seit callSlow eine zweite, eigene Verbindung benutzt, muss ein gewöhnlicher
// Call() nebenher weiterlaufen, statt hinter der Installation zu warten.
func TestSlowCallsNutzenEigeneVerbindung(t *testing.T) {
	socket := startDualConnAgent(t, 300*time.Millisecond)
	client := NewClient(socket)

	slowDone := make(chan error, 1)
	go func() {
		_, err := client.InstallFeature(context.Background(), "redis")
		slowDone <- err
	}()

	// Der laufenden Installation Zeit geben, wirklich zuerst am Server
	// anzukommen, bevor der schnelle Aufruf startet.
	time.Sleep(50 * time.Millisecond)

	start := time.Now()
	if err := client.Healthy(context.Background()); err != nil {
		t.Fatalf("Healthy während laufender Installation: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("Healthy brauchte %s — blockiert offenbar hinter der laufenden Installation", elapsed)
	}

	if err := <-slowDone; err != nil {
		t.Fatalf("InstallFeature: %v", err)
	}
}
