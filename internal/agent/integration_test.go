package agent

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// startTestAgent bringt einen echten Agent auf einem Unix-Socket hoch und gibt
// einen verbundenen Client zurück.
func startTestAgent(t *testing.T) (*Client, string) {
	t.Helper()

	// Unix-Socket-Pfade sind auf ~104 Zeichen begrenzt; das Verzeichnis von
	// t.TempDir() ist dafür auf manchen Systemen zu tief verschachtelt.
	dir, err := os.MkdirTemp("/tmp", "volt-agent-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	sitesDir := filepath.Join(dir, "www")
	if err := os.MkdirAll(sitesDir, 0o755); err != nil {
		t.Fatal(err)
	}

	srv, err := NewServer(ServerOptions{
		SocketPath: filepath.Join(dir, "a.sock"),
		PeerUser:   "", // im Test keine Peer-Prüfung: der Testprozess ist nicht "volt"
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		SitesDir:   sitesDir,
		NginxDir:   filepath.Join(dir, "nginx"),
		PHPDir:     filepath.Join(dir, "php"),
		CertDir:    filepath.Join(dir, "certs"),
		LogDir:     filepath.Join(dir, "log"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Listen(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = srv.Serve(ctx)
	}()

	client := NewClient(filepath.Join(dir, "a.sock"))
	t.Cleanup(func() {
		client.Close()
		cancel()
		wg.Wait()
	})
	return client, sitesDir
}

func TestAgentRoundTrip(t *testing.T) {
	client, sitesDir := startTestAgent(t)
	ctx := t.Context()

	t.Run("ping", func(t *testing.T) {
		if err := client.Healthy(ctx); err != nil {
			t.Fatalf("Healthy: %v", err)
		}
	})

	t.Run("datei schreiben und lesen", func(t *testing.T) {
		path := filepath.Join(sitesDir, "example.at", "public", "index.html")
		if err := client.WriteFile(ctx, path, "<h1>hallo</h1>", 0o644, ""); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		got, err := client.ReadFile(ctx, path)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		if got != "<h1>hallo</h1>" {
			t.Fatalf("ReadFile = %q", got)
		}

		// Die Datei muss auch wirklich auf der Platte liegen, nicht nur im Protokoll.
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("Datei fehlt auf der Platte: %v", err)
		}
	})

	t.Run("verzeichnis auflisten", func(t *testing.T) {
		entries, err := client.ListDir(ctx, filepath.Join(sitesDir, "example.at"))
		if err != nil {
			t.Fatalf("ListDir: %v", err)
		}
		if len(entries) != 1 || entries[0].Name != "public" || !entries[0].IsDir {
			t.Fatalf("ListDir lieferte %+v", entries)
		}
	})

	t.Run("log-tail", func(t *testing.T) {
		path := filepath.Join(sitesDir, "test.log")
		lines := make([]string, 300)
		for i := range lines {
			lines[i] = "zeile " + string(rune('a'+i%26))
		}
		if err := client.WriteFile(ctx, path, strings.Join(lines, "\n"), 0o644, ""); err != nil {
			t.Fatal(err)
		}

		got, err := client.TailLog(ctx, path, 10)
		if err != nil {
			t.Fatalf("TailLog: %v", err)
		}
		if n := len(strings.Split(got, "\n")); n != 10 {
			t.Fatalf("TailLog lieferte %d Zeilen, erwartet 10", n)
		}
		if !strings.HasSuffix(got, lines[299]) {
			t.Fatalf("TailLog endet nicht auf der letzten Zeile: %q", got)
		}
	})

	t.Run("datei entfernen", func(t *testing.T) {
		path := filepath.Join(sitesDir, "example.at", "public", "index.html")
		if err := client.RemovePath(ctx, path, false); err != nil {
			t.Fatalf("RemovePath: %v", err)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("Datei existiert noch")
		}
	})
}

// TestAgentRejectsUnknownOp belegt, dass die Registry die Whitelist ist:
// was nicht drinsteht, wird nicht ausgeführt.
func TestAgentRejectsUnknownOp(t *testing.T) {
	client, _ := startTestAgent(t)

	for _, op := range []Op{"shell.exec", "system.run", "file.write.raw", ""} {
		var out TextResult
		err := client.Call(t.Context(), op, map[string]string{"cmd": "id"}, &out)
		if err == nil {
			t.Fatalf("Operation %q wurde ausgeführt", op)
		}
		if !strings.Contains(err.Error(), "unbekannte operation") {
			t.Fatalf("Operation %q: unerwarteter Fehler %v", op, err)
		}
	}
}

// TestFehlermeldungNenntDieOperationEinmal: der Agent schickt nur die Meldung,
// den Namen der Operation setzt der Client davor. Schickte der Agent seinen
// eigenen Error()-Text, stünde er zweimal da — "mysql.import: mysql.import: …",
// und das landet unverändert in der Oberfläche.
func TestFehlermeldungNenntDieOperationEinmal(t *testing.T) {
	client, sitesDir := startTestAgent(t)

	// Ein Pfad innerhalb der Wurzel, den es nicht gibt: der scheitert erst
	// hinter jail() und damit in einem OpError — genau der Fall, in dem der
	// Name sonst doppelt entsteht.
	_, err := client.ReadFile(t.Context(), filepath.Join(sitesDir, "gibtesnicht.txt"))
	if err == nil {
		t.Fatal("eine nicht vorhandene Datei wurde gelesen")
	}
	if n := strings.Count(err.Error(), string(OpFileRead)); n != 1 {
		t.Errorf("die Operation steht %dmal in der Meldung: %v", n, err)
	}
}

// TestAgentEnforcesJailOverSocket: das Pfad-Gefängnis muss auch über die
// Socket-Schnittstelle greifen, nicht nur in der internen Funktion.
func TestAgentEnforcesJailOverSocket(t *testing.T) {
	client, _ := startTestAgent(t)
	ctx := t.Context()

	escapes := []string{
		"/etc/passwd",
		"/tmp/../etc/shadow",
		"relativ/pfad",
	}
	for _, path := range escapes {
		if err := client.WriteFile(ctx, path, "pwned", 0o644, ""); err == nil {
			t.Fatalf("Schreiben nach %q wurde erlaubt", path)
		}
		if _, err := client.ReadFile(ctx, path); err == nil {
			t.Fatalf("Lesen von %q wurde erlaubt", path)
		}
	}
}

// TestAgentSocketPermissions: der Socket darf nicht für alle offen sein.
func TestAgentSocketPermissions(t *testing.T) {
	client, _ := startTestAgent(t)
	if err := client.Healthy(t.Context()); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(client.socketPath)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm&0o007 != 0 {
		t.Fatalf("Socket hat Rechte %o — andere Benutzer dürfen ihn nicht ansprechen", perm)
	}
}

// TestAgentProtocolMismatch: ein Client mit anderer Protokollversion darf
// nicht durchkommen. Ein halb aktualisiertes Paar aus Web und Agent würde sonst
// Operationen mit falsch verstandenen Parametern ausführen.
func TestAgentProtocolMismatch(t *testing.T) {
	client, _ := startTestAgent(t)

	// Gegenprobe: die richtige Version kommt durch.
	if err := client.Healthy(t.Context()); err != nil {
		t.Fatalf("gültiger Handshake abgelehnt: %v", err)
	}

	conn, err := net.Dial("unix", client.socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	hello, _ := json.Marshal(Hello{Protocol: ProtocolVersion + 1, Client: "veraltet"})
	if _, err := conn.Write(append(hello, '\n')); err != nil {
		t.Fatal(err)
	}

	var ack HelloAck
	if err := json.NewDecoder(conn).Decode(&ack); err != nil {
		t.Fatalf("Handshake-Antwort: %v", err)
	}
	if ack.OK {
		t.Fatal("Agent hat eine falsche Protokollversion akzeptiert")
	}
	if !strings.Contains(ack.Error, "protokoll") {
		t.Fatalf("unerwartete Fehlermeldung: %q", ack.Error)
	}

	// Nach der Ablehnung darf keine Operation mehr durchgehen.
	req, _ := json.Marshal(Request{ID: "1", Op: OpPing})
	_, _ = conn.Write(append(req, '\n'))

	var resp Response
	if err := json.NewDecoder(conn).Decode(&resp); err == nil && resp.OK {
		t.Fatal("Agent hat nach abgelehntem Handshake weiter geantwortet")
	}
}

func TestAgentConcurrentCalls(t *testing.T) {
	client, sitesDir := startTestAgent(t)
	ctx := t.Context()

	// Die Verbindung trägt eine Anfrage zur Zeit; parallele Aufrufer dürfen
	// sich dabei nicht gegenseitig die Antworten wegnehmen.
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			path := filepath.Join(sitesDir, "parallel", string(rune('a'+i))+".txt")
			content := strings.Repeat(string(rune('a'+i)), 64)
			if err := client.WriteFile(ctx, path, content, 0o644, ""); err != nil {
				errs <- err
				return
			}
			got, err := client.ReadFile(ctx, path)
			if err != nil {
				errs <- err
				return
			}
			if got != content {
				errs <- io.ErrUnexpectedEOF
			}
		}(i)
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("parallele Aufrufe sind hängen geblieben")
	}

	close(errs)
	for err := range errs {
		t.Fatalf("paralleler Aufruf fehlgeschlagen: %v", err)
	}
}
