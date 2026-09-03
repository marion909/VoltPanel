package api

import (
	"net/http"
	"strings"
	"testing"
)

// Webmail betrifft den ganzen Server, nicht einen Mandanten — dieselbe Regel
// wie bei Docker, der Firewall oder einem Plugin. Ein Kunde darf weder den
// Stand sehen noch eine Installation auslösen oder entfernen.
func TestWebmailNurFuerAdmins(t *testing.T) {
	ts := newTestServer(t)
	ts.login(t, "bob@example.at") // Kunde

	faelle := []struct{ methode, pfad string }{
		{http.MethodGet, "/api/v1/webmail"},
		{http.MethodPost, "/api/v1/webmail"},
		{http.MethodDelete, "/api/v1/webmail"},
	}
	for _, f := range faelle {
		rec := ts.do(f.methode, f.pfad, map[string]any{"php_version": "8.3"})
		if rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
			t.Errorf("%s %s: Status %d, erwartet 403 — %s",
				f.methode, f.pfad, rec.Code, rec.Body.String())
		}
	}
}

// Ohne Installation meldet der Status das ehrlich — kein Fehler, kein 404,
// nur "installed": false. Ein Administrator, der die Seite vor der
// Einrichtung öffnet, soll keine Fehlermeldung sehen.
func TestWebmailStatusOhneInstallation(t *testing.T) {
	ts := newTestServer(t)
	// alice ist als RoleOwner geseedet — Owner steht über Admin (roleRank),
	// besteht requireRole(RoleAdmin) also mit.
	ts.login(t, "alice@example.at")

	rec := ts.do(http.MethodGet, "/api/v1/webmail", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("Status %d — %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"installed":false`) {
		t.Errorf("antwort meldet nicht installed:false: %s", rec.Body.String())
	}
}
