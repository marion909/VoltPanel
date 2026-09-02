package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// Plugins betreffen den ganzen Server, nicht einen Mandanten — dieselbe Regel
// wie bei Docker, der Firewall und Mail. Ein Kunde darf sie weder sehen noch
// anfassen.
func TestPluginRoutenNurFuerAdmins(t *testing.T) {
	ts := newTestServer(t)
	ts.login(t, "bob@example.at") // Kunde

	faelle := []struct {
		method, path string
	}{
		{http.MethodGet, "/api/v1/plugins"},
		{http.MethodPost, "/api/v1/plugins/redis/install"},
		{http.MethodPost, "/api/v1/plugins/redis/uninstall"},
		{http.MethodPost, "/api/v1/plugins/redis/set"},
	}
	for _, tc := range faelle {
		rec := ts.do(tc.method, tc.path, map[string]any{"enabled": true})
		if rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
			t.Errorf("%s %s: Status %d, erwartet 403 — %s",
				tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}
}

// Ein Administrator sieht den ganzen Katalog, auch ohne dass je etwas
// installiert wurde — sonst gäbe es nichts zum Anklicken.
func TestPluginListeFuerAdmin(t *testing.T) {
	ts := newTestServer(t)
	ts.login(t, "alice@example.at") // Owner, zählt als Administrator

	rec := ts.do(http.MethodGet, "/api/v1/plugins", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("Status %d — %s", rec.Code, rec.Body.String())
	}
	var liste []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &liste); err != nil {
		t.Fatal(err)
	}
	if len(liste) == 0 {
		t.Fatal("der katalog ist leer")
	}
	var gefunden bool
	for _, p := range liste {
		if p["id"] == "redis" {
			gefunden = true
			if installed, _ := p["installed"].(bool); installed {
				t.Error("redis gilt als installiert, ohne dass etwas geschah")
			}
		}
	}
	if !gefunden {
		t.Error(`"redis" fehlt in der liste`)
	}
}

// Ein Plugin-Name, der nicht im Katalog steht, wird abgelehnt — bevor
// irgendein Prozess auf dem Server startet.
func TestPluginInstallUnbekanntUeberHTTP(t *testing.T) {
	ts := newTestServer(t)
	ts.login(t, "alice@example.at")

	rec := ts.do(http.MethodPost, "/api/v1/plugins/nicht-im-katalog/install", nil)
	if rec.Code == http.StatusOK {
		t.Fatal("ein unbekanntes plugin wurde angenommen")
	}
	if !strings.Contains(rec.Body.String(), "kein bekanntes plugin") {
		t.Errorf("abgelehnt, aber aus dem falschen grund: %s", rec.Body.String())
	}
}
