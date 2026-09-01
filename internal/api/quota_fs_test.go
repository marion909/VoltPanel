package api

import (
	"net/http"
	"testing"
)

// TestDateisystemStatusNurFuerAdmins: die Antwort nennt Gerätenamen und
// Einhängepunkte des Servers.
//
// Für einen Kunden ist das nichts Nützliches, aber etwas Verwertbares: es sagt
// ihm, wie die Maschine aufgebaut ist, auf der seine Site zufällig liegt — ob
// /var/www eine eigene Platte hat, wie sie heißt, welches Dateisystem darauf
// liegt. Auskunft über den Server gehört zu dem, wovon ein Mandant nichts
// erfahren soll.
func TestDateisystemStatusNurFuerAdmins(t *testing.T) {
	ts := newTestServer(t)

	ts.login(t, "bob@example.at") // Kunde
	if rec := ts.do(http.MethodGet, "/api/v1/quota/filesystem", nil); rec.Code != http.StatusForbidden {
		t.Errorf("als Kunde: Status %d, erwartet 403 — %s", rec.Code, rec.Body.String())
	}

	// Ohne Sitzung erst recht nicht.
	ts.session, ts.csrf = "", ""
	if rec := ts.do(http.MethodGet, "/api/v1/quota/filesystem", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("ohne Anmeldung: Status %d, erwartet 401", rec.Code)
	}
}
