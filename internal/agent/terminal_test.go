package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testServer(t *testing.T) (*Server, string) {
	t.Helper()

	dir := t.TempDir()
	sites := filepath.Join(dir, "www")
	// Das Verzeichnis muss wirklich existieren: jail() löst seine Wurzeln über
	// EvalSymlinks auf und lehnt sonst jeden Pfad ab — die Prüfungen dahinter
	// kämen nie an die Reihe, und der Test wäre grün, ohne etwas zu zeigen.
	if err := os.MkdirAll(sites, 0o755); err != nil {
		t.Fatal(err)
	}

	srv, err := NewServer(ServerOptions{
		SocketPath: filepath.Join(dir, "a.sock"),
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		SitesDir:   sites,
		NginxDir:   filepath.Join(dir, "nginx"),
		PHPDir:     filepath.Join(dir, "php"),
		CertDir:    filepath.Join(dir, "certs"),
		LogDir:     filepath.Join(dir, "log"),
		BackupDir:  filepath.Join(dir, "backups"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return srv, sites
}

// TestTerminalNurAlsSiteBenutzer ist die Kernzusage des Web-Terminals: eine
// Shell im Browser darf niemals als root laufen.
//
// Der Web-Prozess bestimmt den Benutzer aus der Site. Diese Prüfung ist die
// zweite Schranke — der Agent verlässt sich nicht darauf, dass der Aufrufer
// nichts Falsches schickt, denn genau der Aufrufer ist der Teil, der bei einem
// XSS im Panel als erster übernommen wird.
func TestTerminalNurAlsSiteBenutzer(t *testing.T) {
	srv, sites := testServer(t)

	abgelehnt := []string{"root", "www-data", "volt", "mysql", "alice", "admin", ""}
	for _, name := range abgelehnt {
		t.Run("benutzer_"+name, func(t *testing.T) {
			raw, _ := json.Marshal(TerminalParams{User: name, Dir: sites, Cols: 80, Rows: 24})
			_, err := srv.opTerminalOpen(context.Background(), raw)
			if err == nil {
				t.Fatalf("eine shell als %q wurde erlaubt", name)
			}
			// Nicht nur "irgendein Fehler": auf einem Entwicklungsrechner ohne
			// Pseudoterminals scheitert jeder Aufruf, und der Test wäre grün,
			// auch wenn die Prüfung fehlte. Es muss die Eingabe sein, an der
			// es liegt.
			if !rejectedInput(err) {
				t.Fatalf("abgelehnt, aber nicht wegen des Benutzers: %v", err)
			}
		})
	}
}

// rejectedInput sagt, ob der Agent die Anfrage selbst verworfen hat — im
// Unterschied zu einem Fehler, der beim Ausführen entstanden ist.
func rejectedInput(err error) bool {
	if errors.Is(err, errBadInput) || errors.Is(err, errNotAllow) {
		return true
	}
	var opE *OpError
	return errors.As(err, &opE) && opE.Input
}

// TestTerminalBleibtInDenWurzeln: das Arbeitsverzeichnis geht durch jail().
func TestTerminalBleibtInDenWurzeln(t *testing.T) {
	srv, _ := testServer(t)

	raw, _ := json.Marshal(TerminalParams{User: "site_example_at", Dir: "/etc", Cols: 80, Rows: 24})
	_, err := srv.opTerminalOpen(context.Background(), raw)
	if err == nil {
		t.Fatal("/etc wurde als Arbeitsverzeichnis akzeptiert")
	}
	if !strings.Contains(err.Error(), "außerhalb") {
		t.Errorf("abgelehnt, aber aus dem falschen Grund: %v", err)
	}
}

// TestProzessBeendenNurFuerSites: dieselbe Schranke auf der anderen Operation.
// Ohne sie wäre "prozess beenden" ein Weg, sshd oder den Agent abzuschießen.
func TestProzessBeendenNurFuerSites(t *testing.T) {
	srv, _ := testServer(t)

	cases := []struct {
		name  string
		param ProcessKillParams
	}{
		{"root", ProcessKillParams{PID: 4242, User: "root"}},
		{"fremder benutzer", ProcessKillParams{PID: 4242, User: "alice"}},
		{"init", ProcessKillParams{PID: 1, User: "site_example_at"}},
		{"pid 0", ProcessKillParams{PID: 0, User: "site_example_at"}},
		{"unbekanntes signal", ProcessKillParams{PID: 4242, User: "site_example_at", Signal: "STOP"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := json.Marshal(tc.param)
			_, err := srv.opSystemProcessKill(context.Background(), raw)
			if err == nil {
				t.Fatal("wurde erlaubt")
			}
			if !rejectedInput(err) {
				t.Fatalf("abgelehnt, aber nicht wegen der Eingabe: %v", err)
			}
		})
	}
}

// TestFenstergroesseWirdEingegrenzt: die Werte kommen aus dem Browser und gehen
// als uint16 in einen ioctl.
func TestFenstergroesseWirdEingegrenzt(t *testing.T) {
	cases := []struct{ cols, rows, wantCols, wantRows int }{
		{100, 40, 100, 40},
		{0, 0, 80, 24},
		{-5, -5, 80, 24},
		{99999, 99999, 80, 24},
	}
	for _, tc := range cases {
		cols, rows := clampWindow(tc.cols, tc.rows)
		if cols != tc.wantCols || rows != tc.wantRows {
			t.Errorf("clampWindow(%d, %d) = %d, %d — erwartet %d, %d",
				tc.cols, tc.rows, cols, rows, tc.wantCols, tc.wantRows)
		}
	}
}
