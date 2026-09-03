package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Nicht installiert heißt: gar nicht erst nachfragen. rspamdDir ist die
// einzige Stelle, die das entscheidet — ohne sie käme jede Anfrage bei einem
// Server ohne Rspamd als "nicht erreichbar" zurück, statt ehrlich zu sagen,
// dass es dort gar nicht installiert ist.
func TestOpMailSpamStatsOhneRspamd(t *testing.T) {
	srv, _ := testServer(t)
	res, err := srv.opMailSpamStats(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	stats, ok := res.(RspamdStats)
	if !ok {
		t.Fatalf("unerwarteter typ: %T", res)
	}
	if stats.Installed {
		t.Error("gilt als installiert, obwohl rspamdDir in diesem test nicht existiert")
	}
	if stats.Reachable {
		t.Error("gilt als erreichbar, obwohl gar nicht installiert")
	}
}

// parseRspamdStats liest genau die vier Zahlen, die das Panel zeigt — und
// lässt ein zusätzliches, unbekanntes Feld in Rspamds Antwort unberührt.
// Rspamds /stat trägt deutlich mehr (Version, Uptime, Fuzzy-Hashes); ein
// Decoder, der bei einem unbekannten Feld scheitert, bräche bei der
// nächsten Rspamd-Fassung, ohne dass sich an den vier Zahlen etwas geändert
// hätte.
func TestParseRspamdStats(t *testing.T) {
	roh := []byte(`{
		"version": "3.8.1",
		"uptime": 123456,
		"scanned": 1000,
		"spam_count": 150,
		"ham_count": 800,
		"actions": {"no action": 800, "add header": 100, "greylist": 30, "reject": 70},
		"fuzzy_hashes": {"whatever": 1}
	}`)
	var res RspamdStats
	if err := parseRspamdStats(roh, &res); err != nil {
		t.Fatal(err)
	}
	if res.Scanned != 1000 || res.SpamCount != 150 || res.HamCount != 800 {
		t.Errorf("gelesen: scanned=%d spam=%d ham=%d", res.Scanned, res.SpamCount, res.HamCount)
	}
	if res.Actions["reject"] != 70 {
		t.Errorf(`actions["reject"] = %d, erwartet 70`, res.Actions["reject"])
	}
}

// Eine Antwort, die kein JSON ist — eine HTML-Fehlerseite etwa, wenn dort in
// Wahrheit ein ganz anderer Dienst horcht — darf nicht als "0 gescannt, 0
// Spam" durchgehen. Das sähe wie eine echte Auskunft aus und wäre erfunden.
func TestParseRspamdStatsLehntUnlesbaresAb(t *testing.T) {
	var res RspamdStats
	if err := parseRspamdStats([]byte(`<html>nicht rspamd</html>`), &res); err == nil {
		t.Error("unlesbare antwort wurde als gültige statistik angenommen")
	}
}

// Der volle Weg: Rspamd "installiert" (rspamdDir existiert in diesem Test),
// eine lokale Gegenstelle antwortet wie der echte Controller.
func TestOpMailSpamStatsRundgang(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/stat", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"scanned": 42, "spam_count": 7, "ham_count": 35,
			"actions": map[string]int64{"no action": 35, "reject": 7},
		})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	altURL := rspamdControllerURL
	rspamdControllerURL = ts.URL + "/stat"
	t.Cleanup(func() { rspamdControllerURL = altURL })

	altDir := rspamdDir
	rspamdDir = t.TempDir()
	t.Cleanup(func() { rspamdDir = altDir })

	srv, _ := testServer(t)
	res, err := srv.opMailSpamStats(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	stats := res.(RspamdStats)
	if !stats.Installed || !stats.Reachable {
		t.Fatalf("installed=%v reachable=%v hinweis=%q", stats.Installed, stats.Reachable, stats.Hinweis)
	}
	if stats.Scanned != 42 || stats.SpamCount != 7 {
		t.Errorf("scanned=%d spam=%d", stats.Scanned, stats.SpamCount)
	}
}

// Installiert, aber der Dienst antwortet nicht (gestoppt, oder ein eigenes
// Controller-Passwort verlangt) — das ist kein Fehler des Panels und keine
// "0"-Statistik, sondern ein eigener, benannter Zustand.
func TestOpMailSpamStatsNichtErreichbar(t *testing.T) {
	altURL := rspamdControllerURL
	rspamdControllerURL = "http://127.0.0.1:1/nirgendwo"
	t.Cleanup(func() { rspamdControllerURL = altURL })

	altDir := rspamdDir
	rspamdDir = t.TempDir()
	t.Cleanup(func() { rspamdDir = altDir })

	srv, _ := testServer(t)
	res, err := srv.opMailSpamStats(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	stats := res.(RspamdStats)
	if !stats.Installed {
		t.Error("gilt nicht als installiert, obwohl rspamdDir existiert")
	}
	if stats.Reachable {
		t.Error("gilt als erreichbar, obwohl niemand auf port 1 antwortet")
	}
	if stats.Hinweis == "" {
		t.Error("kein hinweis, warum der controller nicht erreichbar ist")
	}
}
