package api

import (
	"net/http"
	"strings"
	"testing"
)

// TestTerminalNurFuerAdministratoren: eine Shell ist keine Kundenfunktion.
//
// Sie gibt zwar nichts her, was ein Cronjob derselben Site nicht auch könnte —
// beides läuft unter demselben unprivilegierten Konto. Aber sie macht das
// Umsehen auf dem Server so bequem, dass die Freigabe eine eigene Entscheidung
// sein soll und kein Nebeneffekt.
func TestTerminalNurFuerAdministratoren(t *testing.T) {
	ts := newTestServer(t)
	ts.login(t, "bob@example.at")

	// Site 2 gehört Bob selbst — es scheitert an der Rolle, nicht am Mandanten.
	rec := ts.do(http.MethodGet, "/api/v1/sites/2/terminal", nil)
	if rec.Code != http.StatusForbidden {
		t.Errorf("Kunde öffnet ein Terminal: Status %d, erwartet 403 — %s", rec.Code, rec.Body.String())
	}
}

// TestPanelZertifikatNurFuerAdministratoren: das Zertifikat gehört dem Server,
// nicht einem Mandanten.
func TestPanelZertifikatNurFuerAdministratoren(t *testing.T) {
	ts := newTestServer(t)
	ts.login(t, "bob@example.at")

	for _, r := range []struct {
		method, path string
	}{
		{http.MethodGet, "/api/v1/system/panel-certificate"},
		{http.MethodPost, "/api/v1/system/panel-certificate"},
	} {
		rec := ts.do(r.method, r.path, nil)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s als Kunde: Status %d, erwartet 403", r.method, r.path, rec.Code)
		}
	}
}

// TestProzesslisteBrauchtEineSitzung: die Kommandozeilen anderer Mandanten sind
// nichts, was ohne Anmeldung sichtbar sein darf.
func TestProzesslisteBrauchtEineSitzung(t *testing.T) {
	ts := newTestServer(t)

	rec := ts.do(http.MethodGet, "/api/v1/system/processes", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("Prozessliste ohne Sitzung: Status %d, erwartet 401", rec.Code)
	}
}

// TestHSTSBrauchtEinZertifikat: eingeschaltet ohne https ist die Site für jeden
// Browser, der sie einmal besucht hat, ein Jahr lang nicht mehr erreichbar.
// Zurücknehmen lässt sich das nicht — der Kopf kommt ja nicht mehr an.
func TestHSTSBrauchtEinZertifikat(t *testing.T) {
	ts := newTestServer(t)
	ts.login(t, "alice@example.at")

	rec := ts.do(http.MethodPatch, "/api/v1/sites/1", map[string]any{"hsts": true})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("HSTS ohne Zertifikat: Status %d, erwartet 400 — %s", rec.Code, rec.Body.String())
	}

	// Die Site darf dabei nicht halb umgestellt worden sein.
	rec = ts.do(http.MethodGet, "/api/v1/sites/1", nil)
	if got := rec.Body.String(); !strings.Contains(got, `"hsts":false`) {
		t.Errorf("die Site steht danach auf: %s", got)
	}
}
