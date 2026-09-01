package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/marion909/voltpanel/internal/store"
)

// doHost ist do() mit gesetztem Host — so kommt eine Anfrage an, die über die
// Anmeldedomain eines Mandanten läuft.
func (ts *testServer) doHost(host, method, path string, body any) *httptest.ResponseRecorder {
	var reader io.Reader
	if body != nil {
		encoded, _ := json.Marshal(body)
		reader = strings.NewReader(string(encoded))
	}
	req := httptest.NewRequest(method, path, reader)
	req.Host = host
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if ts.session != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: ts.session})
		req.AddCookie(&http.Cookie{Name: csrfCookie, Value: ts.csrf})
		req.Header.Set(csrfHeader, ts.csrf)
	}
	rec := httptest.NewRecorder()
	ts.server.echo.ServeHTTP(rec, req)
	return rec
}

// setLoginDomain trägt die Domain direkt in der Datenbank ein.
func setLoginDomain(t *testing.T, ts *testServer, slug, domain string) *store.Tenant {
	t.Helper()
	tenants, err := ts.store.ListTenants(t.Context(), store.SystemScope())
	if err != nil {
		t.Fatal(err)
	}
	for _, tenant := range tenants {
		if tenant.Slug != slug {
			continue
		}
		if err := ts.store.SetTenantLoginDomain(t.Context(), store.SystemScope(),
			tenant.ID, domain); err != nil {
			t.Fatal(err)
		}
		ts.server.logins.invalidate()
		return tenant
	}
	t.Fatalf("mandant %q nicht gefunden", slug)
	return nil
}

// TestFremderMandantKommtNichtDurchDieKundendomain ist die Zusage, an der die
// ganze Funktion hängt.
//
// Ohne sie wäre die Domain des Kunden ein zweiter Eingang zum Konto des
// Betreibers — unter einem Namen, den der Kunde selbst bestimmt, und an einer
// Stelle, an der niemand ihn vermutet.
func TestFremderMandantKommtNichtDurchDieKundendomain(t *testing.T) {
	ts := newTestServer(t)
	setLoginDomain(t, ts, "bob", "panel.bob.example.at")

	// Alice gehört zu einem anderen Mandanten. Ihre Zugangsdaten stimmen.
	rec := ts.doHost("panel.bob.example.at", http.MethodPost, "/api/v1/auth/login",
		map[string]string{"email": "alice@example.at", "password": testPassword})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("Alice kam über die Domain von Bob herein: Status %d — %s",
			rec.Code, rec.Body.String())
	}

	// Und Bob selbst kommt herein — sonst prüfte der Test nur, dass gar nichts
	// mehr geht.
	rec = ts.doHost("panel.bob.example.at", http.MethodPost, "/api/v1/auth/login",
		map[string]string{"email": "bob@example.at", "password": testPassword})
	if rec.Code != http.StatusOK {
		t.Errorf("Bob kam über seine eigene Domain nicht herein: Status %d — %s",
			rec.Code, rec.Body.String())
	}
}

// TestKundendomainVerraetKeineKonten: die Antwort auf ein fremdes, aber
// richtiges Konto muss dieselbe sein wie auf ein erfundenes. Sonst wäre die
// Anmeldeseite eines Kunden ein Werkzeug, um herauszufinden, wer sonst noch
// ein Konto auf diesem Server hat.
func TestKundendomainVerraetKeineKonten(t *testing.T) {
	ts := newTestServer(t)
	setLoginDomain(t, ts, "bob", "panel.bob.example.at")

	fremd := ts.doHost("panel.bob.example.at", http.MethodPost, "/api/v1/auth/login",
		map[string]string{"email": "alice@example.at", "password": testPassword})
	erfunden := ts.doHost("panel.bob.example.at", http.MethodPost, "/api/v1/auth/login",
		map[string]string{"email": "gibtsnicht@example.at", "password": testPassword})

	if fremd.Code != erfunden.Code || fremd.Body.String() != erfunden.Body.String() {
		t.Errorf("fremdes Konto: %d %s\nerfundenes:     %d %s",
			fremd.Code, fremd.Body.String(), erfunden.Code, erfunden.Body.String())
	}
}

// TestPanelDomainBleibtOffen: das Panel des Betreibers nimmt weiterhin jeden.
// Die Schranke gilt nur auf der Domain eines Mandanten.
func TestPanelDomainBleibtOffen(t *testing.T) {
	ts := newTestServer(t)
	setLoginDomain(t, ts, "bob", "panel.bob.example.at")

	for _, email := range []string{"alice@example.at", "bob@example.at"} {
		rec := ts.doHost("panel.example.at", http.MethodPost, "/api/v1/auth/login",
			map[string]string{"email": email, "password": testPassword})
		if rec.Code != http.StatusOK {
			t.Errorf("%s kam am Panel nicht herein: Status %d — %s",
				email, rec.Code, rec.Body.String())
		}
	}
}

// TestGefaelschterHostKopfWirdNichtGeglaubt: X-Forwarded-Host setzt, wer die
// Anfrage schickt. Würde er hier gelten, bestimmte der Absender, als welcher
// Mandant die Anmeldeseite auftritt — und damit, wer sich anmelden darf.
func TestGefaelschterHostKopfWirdNichtGeglaubt(t *testing.T) {
	ts := newTestServer(t)
	setLoginDomain(t, ts, "bob", "panel.bob.example.at")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/branding", nil)
	req.Host = "panel.example.at"
	req.Header.Set("X-Forwarded-Host", "panel.bob.example.at")

	rec := httptest.NewRecorder()
	ts.server.echo.ServeHTTP(rec, req)

	if strings.Contains(rec.Body.String(), "bob") {
		t.Errorf("der gefälschte Kopf hat gewirkt: %s", rec.Body.String())
	}
}

// TestBrandingSagtNurDenNamen: die Auskunft geht an jeden, der die Seite
// aufruft — vor der Anmeldung gibt es keine Sitzung. Sie darf deshalb nichts
// enthalten, was über den Namen hinausgeht.
func TestBrandingSagtNurDenNamen(t *testing.T) {
	ts := newTestServer(t)
	setLoginDomain(t, ts, "bob", "panel.bob.example.at")

	rec := ts.doHost("panel.bob.example.at", http.MethodGet, "/api/v1/auth/branding", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("Status %d — %s", rec.Code, rec.Body.String())
	}

	var out struct {
		Tenant map[string]any `json:"tenant"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Tenant["name"] != "bob" {
		t.Errorf("Name fehlt: %v", out.Tenant)
	}
	for _, verboten := range []string{"id", "plan_id", "cloudflare_token", "status", "login_domain"} {
		if _, da := out.Tenant[verboten]; da {
			t.Errorf("%q steht in der öffentlichen Auskunft: %v", verboten, out.Tenant)
		}
	}

	// Auf dem Panel des Betreibers gibt es nichts zu branden.
	rec = ts.doHost("panel.example.at", http.MethodGet, "/api/v1/auth/branding", nil)
	if !strings.Contains(rec.Body.String(), `"tenant":null`) {
		t.Errorf("das Panel meldet einen Mandanten: %s", rec.Body.String())
	}
}

// TestKundeBrauchtDenZugriffspfadNicht: der Zugriffspfad ist das Geheimnis des
// Betreibers — install.sh würfelt ihn genau deshalb aus. Ein Kunde soll ihn
// nicht brauchen und ihn erst recht nicht erfahren.
//
// Auf seiner Domain liegt das Panel deshalb unter "/", und zwar nach innen
// ergänzt statt nach außen umgeleitet: eine Weiterleitung verriete ihm mit der
// neuen Adresse genau den Pfad, den er nicht kennen soll.
func TestKundeBrauchtDenZugriffspfadNicht(t *testing.T) {
	srv := serverWithAccessPath(t)

	sys := store.SystemScope()
	tenant := &store.Tenant{Name: "bob", Slug: "bob"}
	if err := srv.store.CreateTenant(t.Context(), sys, tenant); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SetTenantLoginDomain(t.Context(), sys, tenant.ID,
		"panel.bob.example.at"); err != nil {
		t.Fatal(err)
	}
	srv.logins.invalidate()

	ruf := func(host, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Host = host
		rec := httptest.NewRecorder()
		srv.echo.ServeHTTP(rec, req)
		return rec
	}

	// Auf der Kundendomain trifft "/api/v1/auth/state" die Route, obwohl der
	// Pfad des Betreibers fehlt.
	if rec := ruf("panel.bob.example.at", "/api/v1/auth/state"); rec.Code != http.StatusOK {
		t.Errorf("auf der Kundendomain: Status %d — %s", rec.Code, rec.Body.String())
	}

	// Auf jedem anderen Host bleibt der Pfad Pflicht. Sonst hätte der
	// Zugriffspfad seinen Zweck verloren.
	if rec := ruf("panel.example.at", "/api/v1/auth/state"); rec.Code == http.StatusOK {
		t.Errorf("ohne Zugriffspfad erreichbar: Status %d — %s", rec.Code, rec.Body.String())
	}
	if rec := ruf("fremd.example.at", "/api/v1/auth/state"); rec.Code == http.StatusOK {
		t.Errorf("ein beliebiger Host kam am Zugriffspfad vorbei: Status %d", rec.Code)
	}
}

// TestGesperrterMandantKommtNichtHerein: bis hierher setzte "sperren" nur ein
// Feld. Die Oberfläche zeigte den Mandanten als gesperrt an, und seine Leute
// meldeten sich weiter an, als wäre nichts — eine Sperre, die niemanden sperrt.
func TestGesperrterMandantKommtNichtHerein(t *testing.T) {
	ts := newTestServer(t)
	sys := store.SystemScope()

	tenants, err := ts.store.ListTenants(t.Context(), sys)
	if err != nil {
		t.Fatal(err)
	}
	var bob *store.Tenant
	for _, tenant := range tenants {
		if tenant.Slug == "bob" {
			bob = tenant
		}
	}
	if bob == nil {
		t.Fatal("mandant bob fehlt")
	}

	// Vorher kommt er herein — sonst prüfte der Test nur, dass gar nichts geht.
	rec := ts.do(http.MethodPost, "/api/v1/auth/login",
		map[string]string{"email": "bob@example.at", "password": testPassword})
	if rec.Code != http.StatusOK {
		t.Fatalf("vor der Sperre: Status %d — %s", rec.Code, rec.Body.String())
	}

	bob.Status = "suspended"
	if err := ts.store.UpdateTenant(t.Context(), sys, bob); err != nil {
		t.Fatal(err)
	}

	ts.session, ts.csrf = "", ""
	rec = ts.do(http.MethodPost, "/api/v1/auth/login",
		map[string]string{"email": "bob@example.at", "password": testPassword})
	if rec.Code != http.StatusForbidden {
		t.Errorf("nach der Sperre: Status %d — %s", rec.Code, rec.Body.String())
	}

	// Der andere Mandant bleibt davon unberührt.
	rec = ts.do(http.MethodPost, "/api/v1/auth/login",
		map[string]string{"email": "alice@example.at", "password": testPassword})
	if rec.Code != http.StatusOK {
		t.Errorf("Alice wurde mitgesperrt: Status %d — %s", rec.Code, rec.Body.String())
	}
}

// TestEigenenMandantenNichtSperren: seit eine Sperre die Anmeldung wirklich
// verhindert, wäre das der kürzeste Weg, sich selbst aus dem Panel
// auszusperren — und niemand wäre mehr da, der sie zurücknimmt.
func TestEigenenMandantenNichtSperren(t *testing.T) {
	ts := newTestServer(t)
	ts.login(t, "alice@example.at") // Owner von Mandant 1

	rec := ts.do(http.MethodPatch, "/api/v1/tenants/1",
		map[string]string{"status": "suspended"})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("Status %d, erwartet 400 — %s", rec.Code, rec.Body.String())
	}

	// Und danach kommt sie noch herein.
	ts.session, ts.csrf = "", ""
	if rec := ts.do(http.MethodPost, "/api/v1/auth/login",
		map[string]string{"email": "alice@example.at", "password": testPassword}); rec.Code != http.StatusOK {
		t.Errorf("Alice hat sich selbst ausgesperrt: Status %d", rec.Code)
	}
}
