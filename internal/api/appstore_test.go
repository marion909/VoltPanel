package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/marion909/voltpanel/internal/store"
)

// Den Katalog sieht jeder Angemeldete — er nennt keine fremden Daten, nur was
// sich installieren lässt.
func TestAppStoreCatalogFuerJedenAngemeldeten(t *testing.T) {
	ts := newTestServer(t)
	ts.login(t, "bob@example.at") // Kunde

	rec := ts.do(http.MethodGet, "/api/v1/appstore", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("Status %d — %s", rec.Code, rec.Body.String())
	}
	var katalog []map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &katalog); err != nil {
		t.Fatal(err)
	}
	var gefunden bool
	for _, e := range katalog {
		if e["id"] == "wordpress" {
			gefunden = true
		}
	}
	if !gefunden {
		t.Error(`"wordpress" fehlt im katalog`)
	}
}

// Dieselbe Zusage wie bei /sites direkt: eine fremde tenant_id in der Anfrage
// darf nicht dazu führen, dass eine Site (und eine Datenbank) bei einem
// anderen Mandanten entsteht. Der Handler übernimmt den Vergleich aus
// handleCreateSite — geprüft wird hier, dass die Kopie wirklich wirkt und
// nicht nur syntaktisch danebensteht.
//
// Verlangt wird ausdrücklich der Status 404 ("nicht gefunden" — ForTenant
// scheitert mit ErrForbidden, und storeError macht daraus bewusst keine 403,
// die einem Angreifer bestätigte, dass die fremde ID existiert). Nicht nur
// "nicht 201": in dieser Testumgebung zeigt SocketPath auf keinen laufenden
// Agent, und *jeder* Versuch, wirklich eine Site anzulegen, scheitert
// ohnehin mit 503 "agent läuft nicht" — ganz unabhängig davon, ob die
// Mandantenprüfung greift. Ein Test, der nur auf "kein 201" prüft, bliebe
// deshalb grün, selbst wenn der Vergleich versehentlich herausfiele; das ist
// mir beim ersten Anlauf genau so passiert.
func TestInstallWordPressLehntFremdeTenantIDAb(t *testing.T) {
	ts := newTestServer(t)
	ts.login(t, "bob@example.at") // Kunde bei "bob"

	tenants, err := ts.store.ListTenants(t.Context(), store.SystemScope())
	if err != nil {
		t.Fatal(err)
	}
	var aliceID int64
	for _, tn := range tenants {
		if tn.Slug == "alice" {
			aliceID = tn.ID
		}
	}
	if aliceID == 0 {
		t.Fatal(`mandant "alice" fehlt in der testumgebung`)
	}

	rec := ts.do(http.MethodPost, "/api/v1/appstore/wordpress", map[string]any{
		"domain": "gekapert.example.at", "php_version": "8.3", "tenant_id": aliceID,
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("Status %d, erwartet 404 — %s", rec.Code, rec.Body.String())
	}

	// Und bei alice steht wirklich keine Site mit dieser Domain.
	sites, err := ts.store.ListSites(t.Context(), store.SystemScope())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sites {
		if s.Domain == "gekapert.example.at" {
			t.Error("die site wurde trotz abgelehnter anfrage angelegt")
		}
	}
}
