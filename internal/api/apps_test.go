package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/marion909/voltpanel/internal/store"
)

// appSite legt eine Proxy-Site für einen Mandanten an.
func appSite(t *testing.T, ts *testServer, slug, domain string) *store.Site {
	t.Helper()
	ctx, sys := t.Context(), store.SystemScope()

	tenants, err := ts.store.ListTenants(ctx, sys)
	if err != nil {
		t.Fatal(err)
	}
	var tenantID int64
	for _, tenant := range tenants {
		if tenant.Slug == slug {
			tenantID = tenant.ID
		}
	}
	if tenantID == 0 {
		t.Fatalf("mandant %q fehlt", slug)
	}

	site := &store.Site{
		TenantID: tenantID, Domain: domain, Type: store.SiteProxy,
		SystemUser: "site_" + slug, RootPath: "/var/www/" + domain,
		DocumentRoot: "public", ProxyTarget: "http://127.0.0.1:1",
	}
	if err := ts.store.CreateSite(ctx, sys, site); err != nil {
		t.Fatal(err)
	}
	return site
}

// TestAppBleibtImMandantenUeberHTTP: dieselbe Zusage wie überall, diesmal auf
// der Ebene, auf der sie zählt — jeder Endpunkt mit einer fremden ID.
func TestAppBleibtImMandantenUeberHTTP(t *testing.T) {
	ts := newTestServer(t)
	alice := appSite(t, ts, "alice", "app.alice.example.at")

	// Die App direkt in der Datenbank anlegen: der Weg über den Endpunkt
	// bräuchte einen laufenden Agent, und geprüft wird hier der Zugriff.
	app := &store.App{
		TenantID: alice.TenantID, SiteID: alice.ID,
		Name: store.AppNameForDomain(alice.Domain), Runtime: "node",
	}
	if err := ts.store.CreateApp(t.Context(), store.SystemScope(), app); err != nil {
		t.Fatal(err)
	}

	ts.login(t, "bob@example.at") // anderer Mandant

	for _, tc := range []struct{ method, path string }{
		{http.MethodPatch, "/api/v1/apps/1"},
		{http.MethodDelete, "/api/v1/apps/1"},
	} {
		rec := ts.do(tc.method, tc.path, map[string]any{"runtime": "node"})
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s: Status %d, erwartet 404 — %s",
				tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}

	// Und in der Liste taucht sie nicht auf.
	rec := ts.do(http.MethodGet, "/api/v1/apps", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("Liste: Status %d — %s", rec.Code, rec.Body.String())
	}
	var liste []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &liste); err != nil {
		t.Fatal(err)
	}
	if len(liste) != 0 {
		t.Errorf("Bob sieht %d fremde Apps: %v", len(liste), liste)
	}
}

// TestUmgebungKommtNieZurueck: in einer App-Umgebung stehen regelmäßig
// Datenbankpasswörter. Wer sie einmal gesetzt hat, braucht sie nicht
// zurückgelesen zu bekommen — und ein übernommenes Panel-Konto erst recht
// nicht.
func TestUmgebungKommtNieZurueck(t *testing.T) {
	ts := newTestServer(t)
	site := appSite(t, ts, "alice", "app.alice.example.at")

	svc := ts.server.apps
	enc, err := svcEncode(t, ts, map[string]string{"DB_PASSWORD": "streng-geheim"})
	if err != nil {
		t.Fatal(err)
	}
	app := &store.App{
		TenantID: site.TenantID, SiteID: site.ID, Name: "alice-app",
		Runtime: "node", EnvEnc: enc,
	}
	if err := ts.store.CreateApp(t.Context(), store.SystemScope(), app); err != nil {
		t.Fatal(err)
	}

	// Die verschlüsselte Fassung darf schon in der Datenbank nicht im Klartext
	// stehen — die Datenbank des Panels ist eine Datei.
	if enc == "" || strings.Contains(enc, "streng-geheim") {
		t.Errorf("die Umgebung liegt unverschlüsselt: %q", enc)
	}

	views, err := svc.ListApps(t.Context(), store.SystemScope())
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 {
		t.Fatalf("%d Apps in der Liste", len(views))
	}
	raw, err := json.Marshal(views[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "streng-geheim") {
		t.Errorf("das Passwort steht in der Antwort: %s", raw)
	}
	// Die Namen dürfen dastehen: ohne sie sähe niemand, was gesetzt ist.
	if !strings.Contains(string(raw), "DB_PASSWORD") {
		t.Errorf("die Namen der Variablen fehlen: %s", raw)
	}

	ts.login(t, "alice@example.at")
	rec := ts.do(http.MethodGet, "/api/v1/apps", nil)
	if strings.Contains(rec.Body.String(), "streng-geheim") {
		t.Errorf("das Passwort steht in der HTTP-Antwort: %s", rec.Body.String())
	}
}

func svcEncode(t *testing.T, ts *testServer, env map[string]string) (string, error) {
	t.Helper()
	raw, err := json.Marshal(env)
	if err != nil {
		return "", err
	}
	return ts.server.secrets.Encrypt(string(raw))
}

// TestAppNurAufProxySite: bei einer PHP- oder Static-Site zeigte der Vhost
// weiter auf das Verzeichnis, und die App liefe für niemanden.
func TestAppNurAufProxySite(t *testing.T) {
	ts := newTestServer(t)
	ctx, sys := t.Context(), store.SystemScope()

	tenants, _ := ts.store.ListTenants(ctx, sys)
	static := &store.Site{
		TenantID: tenants[0].ID, Domain: "statisch.example.at", Type: store.SiteStatic,
		SystemUser: "site_alice", RootPath: "/var/www/statisch", DocumentRoot: "public",
	}
	if err := ts.store.CreateSite(ctx, sys, static); err != nil {
		t.Fatal(err)
	}

	ts.login(t, "alice@example.at")
	rec := ts.do(http.MethodPost, "/api/v1/apps", map[string]any{
		"site_id": static.ID, "runtime": "node", "args": []string{"server.js"},
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("Status %d, erwartet 400 — %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "proxy-site") {
		t.Errorf("die Meldung sagt nicht warum: %s", rec.Body.String())
	}
}
