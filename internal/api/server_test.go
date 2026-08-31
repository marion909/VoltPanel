package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marion909/voltpanel/internal/agent"
	"github.com/marion909/voltpanel/internal/authn"
	"github.com/marion909/voltpanel/internal/config"
	"github.com/marion909/voltpanel/internal/metrics"
	"github.com/marion909/voltpanel/internal/store"
)

const testPassword = "Test-Panel-Passwort-9"

type testServer struct {
	server  *Server
	store   *store.Store
	session string
	csrf    string
}

// newTestServer baut die API mit echter Datenbank auf. Der Agent zeigt auf
// einen Socket, den es nicht gibt — Endpunkte, die ihn brauchen, sollen 503
// liefern und nicht abstürzen.
func newTestServer(t *testing.T) *testServer {
	t.Helper()
	dir := t.TempDir()

	st, err := store.Open(filepath.Join(dir, "volt.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if _, _, err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}

	secrets, err := authn.LoadSecretBox(filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.DataDir, cfg.SitesDir, cfg.LogDir = dir, filepath.Join(dir, "www"), dir
	cfg.SocketPath = filepath.Join(dir, "nicht-vorhanden.sock")

	srv, err := New(Options{
		Config: cfg, Store: st, Agent: agent.NewClient(cfg.SocketPath),
		Metrics: metrics.New(time.Second, slog.New(slog.NewTextHandler(io.Discard, nil))),
		Secrets: secrets, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}

	ts := &testServer{server: srv, store: st}
	ts.seed(t)
	return ts
}

func (ts *testServer) seed(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	sys := store.SystemScope()

	hash, err := authn.HashPassword(testPassword)
	if err != nil {
		t.Fatal(err)
	}

	for _, slug := range []string{"alice", "bob"} {
		tenant := &store.Tenant{Name: slug, Slug: slug}
		if err := ts.store.CreateTenant(ctx, sys, tenant); err != nil {
			t.Fatal(err)
		}
		role := store.RoleOwner
		if slug == "bob" {
			role = store.RoleCustomer
		}
		if err := ts.store.CreateUser(ctx, sys, &store.User{
			TenantID: tenant.ID, Email: slug + "@example.at",
			PasswordHash: hash, Role: role,
		}); err != nil {
			t.Fatal(err)
		}
		if err := ts.store.CreateSite(ctx, sys, &store.Site{
			TenantID: tenant.ID, Domain: slug + ".example.at", Type: store.SiteStatic,
			SystemUser: "site_" + slug, RootPath: "/var/www/" + slug, DocumentRoot: "public",
		}); err != nil {
			t.Fatal(err)
		}
	}
}

// do schickt eine Anfrage mit den Cookies der aktuellen Sitzung.
func (ts *testServer) do(method, path string, body any) *httptest.ResponseRecorder {
	var reader io.Reader
	if body != nil {
		encoded, _ := json.Marshal(body)
		reader = strings.NewReader(string(encoded))
	}

	req := httptest.NewRequest(method, path, reader)
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

func (ts *testServer) login(t *testing.T, email string) {
	t.Helper()
	ts.session, ts.csrf = "", ""

	rec := ts.do(http.MethodPost, "/api/v1/auth/login",
		map[string]string{"email": email, "password": testPassword})
	if rec.Code != http.StatusOK {
		t.Fatalf("Login als %s: Status %d — %s", email, rec.Code, rec.Body.String())
	}

	for _, cookie := range rec.Result().Cookies() {
		switch cookie.Name {
		case sessionCookie:
			ts.session = cookie.Value
		case csrfCookie:
			ts.csrf = cookie.Value
		}
	}
	if ts.session == "" || ts.csrf == "" {
		t.Fatal("Login lieferte keine Cookies")
	}
}

// TestRoutesAreWired hakt jede Route einmal ab.
//
// Der Test existiert, weil ein nicht verdrahteter Dienst im Server-Konstruktor
// beim Kompilieren nicht auffällt: das Feld ist dann schlicht nil, und der
// erste Aufruf stürzt mit einer Nil-Dereferenzierung ab. Ein 500 hier heißt
// immer, dass etwas nicht zusammengesteckt wurde.
func TestRoutesAreWired(t *testing.T) {
	ts := newTestServer(t)
	ts.login(t, "alice@example.at")

	routes := []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodGet, "/api/v1/auth/me", nil},
		{http.MethodGet, "/api/v1/sites", nil},
		{http.MethodGet, "/api/v1/sites/1", nil},
		{http.MethodGet, "/api/v1/tenants", nil},
		{http.MethodGet, "/api/v1/users", nil},
		{http.MethodGet, "/api/v1/audit", nil},
		{http.MethodGet, "/api/v1/cronjobs", nil},
		{http.MethodGet, "/api/v1/databases", nil},
		{http.MethodGet, "/api/v1/system/metrics", nil},
		{http.MethodGet, "/api/v1/system/info", nil},
		{http.MethodGet, "/api/v1/system/services", nil},
		{http.MethodGet, "/api/v1/sites/1/files", nil},
		{http.MethodGet, "/api/v1/sites/1/files/read?path=x", nil},
		{http.MethodGet, "/api/v1/sites/1/logs", nil},
		{http.MethodPost, "/api/v1/sites/1/files/write", map[string]string{"path": "x", "content": "y"}},
		{http.MethodPost, "/api/v1/sites/1/files/mkdir", map[string]string{"path": "x"}},
		{http.MethodPost, "/api/v1/sites/1/files/delete", map[string]string{"path": "x"}},
		{http.MethodPost, "/api/v1/sites/1/files/move", map[string]string{"from": "a", "to": "b"}},
		{http.MethodPost, "/api/v1/sites/1/files/copy", map[string]string{"from": "a", "to": "b"}},
		{http.MethodPost, "/api/v1/sites/1/files/chmod", map[string]string{"path": "x", "mode": "644"}},
		{http.MethodPost, "/api/v1/databases", map[string]any{"name": "testdb"}},
		{http.MethodPost, "/api/v1/cronjobs", map[string]any{
			"name": "Testjob", "schedule": "0 3 * * *", "command": "echo hallo", "site_id": 1,
		}},
	}

	for _, r := range routes {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			rec := ts.do(r.method, r.path, r.body)
			if rec.Code >= 500 && rec.Code != http.StatusServiceUnavailable &&
				rec.Code != http.StatusBadGateway {
				t.Fatalf("Status %d — %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestUnauthenticatedAccessDenied: ohne Sitzung geht nichts.
func TestUnauthenticatedAccessDenied(t *testing.T) {
	ts := newTestServer(t)

	for _, path := range []string{
		"/api/v1/sites", "/api/v1/databases", "/api/v1/cronjobs",
		"/api/v1/users", "/api/v1/audit", "/api/v1/sites/1/files",
	} {
		rec := ts.do(http.MethodGet, path, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s ohne Sitzung: Status %d, erwartet 401", path, rec.Code)
		}
	}
}

// TestCSRFRequired: schreibende Anfragen ohne Header werden abgewiesen.
func TestCSRFRequired(t *testing.T) {
	ts := newTestServer(t)
	ts.login(t, "alice@example.at")

	writes := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/sites"},
		{http.MethodPost, "/api/v1/databases"},
		{http.MethodPost, "/api/v1/cronjobs"},
		{http.MethodPost, "/api/v1/sites/1/files/write"},
		{http.MethodDelete, "/api/v1/sites/1"},
	}
	for _, w := range writes {
		req := httptest.NewRequest(w.method, w.path, strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		// Session-Cookie ja, CSRF-Header nein — genau das Szenario, das eine
		// fremde Seite herstellen könnte.
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: ts.session})
		req.AddCookie(&http.Cookie{Name: csrfCookie, Value: ts.csrf})

		rec := httptest.NewRecorder()
		ts.server.echo.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s ohne CSRF-Header: Status %d, erwartet 403", w.method, w.path, rec.Code)
		}
	}
}

// TestCrossTenantReturnsNotFound: eine fremde ID sieht aus wie "gibt es nicht".
//
// Bewusst 404 und nicht 403: eine 403 würde bestätigen, dass die ID existiert.
func TestCrossTenantReturnsNotFound(t *testing.T) {
	ts := newTestServer(t)
	ts.login(t, "bob@example.at")

	// Site 1 gehört Alice.
	for _, path := range []string{
		"/api/v1/sites/1",
		"/api/v1/sites/1/files",
		"/api/v1/sites/1/files/read?path=index.html",
		"/api/v1/sites/1/logs",
	} {
		rec := ts.do(http.MethodGet, path, nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s als fremder Tenant: Status %d, erwartet 404 — %s",
				path, rec.Code, rec.Body.String())
		}
	}

	rec := ts.do(http.MethodDelete, "/api/v1/sites/1", nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("DELETE einer fremden Site: Status %d, erwartet 404", rec.Code)
	}
}

// TestDatenbankExportUndImportSindEingegrenzt: beide Wege gehen über
// GetDatabase mit dem Scope des Aufrufers. Eine fremde oder nicht vorhandene
// Datenbank ist deshalb nicht unterscheidbar — beides ist 404.
func TestDatenbankExportUndImportSindEingegrenzt(t *testing.T) {
	ts := newTestServer(t)
	ts.login(t, "bob@example.at")

	rec := ts.do(http.MethodGet, "/api/v1/databases/999/dump/download", nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("Export einer fremden Datenbank: Status %d, erwartet 404 — %s",
			rec.Code, rec.Body.String())
	}

	// Ohne Datei ist die Anfrage unbrauchbar, aber kein Serverfehler.
	rec = ts.do(http.MethodPost, "/api/v1/databases/999/import", nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("Import ohne Datei: Status %d, erwartet 400 — %s", rec.Code, rec.Body.String())
	}
}

// TestCustomerCannotReachAdminRoutes prüft die Rollenprüfung.
func TestCustomerCannotReachAdminRoutes(t *testing.T) {
	ts := newTestServer(t)
	ts.login(t, "bob@example.at")

	rec := ts.do(http.MethodPost, "/api/v1/tenants", map[string]string{"name": "neu", "slug": "neu"})
	if rec.Code != http.StatusForbidden {
		t.Errorf("Kunde legt Tenant an: Status %d, erwartet 403", rec.Code)
	}

	rec = ts.do(http.MethodPost, "/api/v1/system/services/nginx/restart", nil)
	if rec.Code != http.StatusForbidden {
		t.Errorf("Kunde startet Dienst neu: Status %d, erwartet 403", rec.Code)
	}
}

// TestValidationErrorsAreClientErrors: eine unbrauchbare Eingabe ist 4xx,
// nicht 500. Ein 500 hier hieße, dass ein Fehler unbehandelt durchschlägt.
func TestValidationErrorsAreClientErrors(t *testing.T) {
	ts := newTestServer(t)
	ts.login(t, "alice@example.at")

	cases := []struct {
		name string
		path string
		body any
	}{
		{"cronjob mit sechstem feld", "/api/v1/cronjobs", map[string]any{
			"name": "böse", "schedule": "* * * * * root", "command": "echo", "site_id": 1,
		}},
		{"cronjob mit zeilenumbruch", "/api/v1/cronjobs", map[string]any{
			"name": "böse", "schedule": "* * * * *",
			"command": "echo\n* * * * * root rm -rf /", "site_id": 1,
		}},
		{"site mit ungültiger domain", "/api/v1/sites", map[string]any{
			"domain": "nicht valide", "type": "static",
		}},
		{"site mit unbekanntem typ", "/api/v1/sites", map[string]any{
			"domain": "neu.example.at", "type": "wordpress",
		}},
		{"datenbank mit sql im namen", "/api/v1/databases", map[string]any{
			"name": "x; DROP DATABASE mysql",
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := ts.do(http.MethodPost, tc.path, tc.body)
			if rec.Code < 400 || rec.Code >= 500 {
				t.Fatalf("Status %d, erwartet 4xx — %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestLoginRejectsWrongCredentials prüft, dass unbekannte Adresse und falsches
// Passwort dieselbe Antwort ergeben — sonst verrät das Panel seine Benutzer.
func TestLoginRejectsWrongCredentials(t *testing.T) {
	ts := newTestServer(t)

	wrongPassword := ts.do(http.MethodPost, "/api/v1/auth/login",
		map[string]string{"email": "alice@example.at", "password": "falsch"})
	unknownUser := ts.do(http.MethodPost, "/api/v1/auth/login",
		map[string]string{"email": "niemand@example.at", "password": "falsch"})

	if wrongPassword.Code != http.StatusUnauthorized || unknownUser.Code != http.StatusUnauthorized {
		t.Fatalf("Status: falsches Passwort %d, unbekannte Adresse %d — erwartet je 401",
			wrongPassword.Code, unknownUser.Code)
	}
	if wrongPassword.Body.String() != unknownUser.Body.String() {
		t.Fatalf("unterschiedliche Antworten:\n  falsches Passwort:   %s\n  unbekannte Adresse:  %s",
			wrongPassword.Body.String(), unknownUser.Body.String())
	}
}

func TestSecurityHeadersPresent(t *testing.T) {
	ts := newTestServer(t)
	rec := ts.do(http.MethodGet, "/api/v1/auth/state", nil)

	required := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
	}
	for header, want := range required {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s = %q, erwartet %q", header, got, want)
		}
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("CSP ohne frame-ancestors: %q", csp)
	}
}

// --- Pakete, Mandanten und Quota ------------------------------------------

func TestPlanAndTenantRoutes(t *testing.T) {
	ts := newTestServer(t)
	ts.login(t, "alice@example.at") // Owner

	var plan struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	t.Run("paket anlegen", func(t *testing.T) {
		rec := ts.do(http.MethodPost, "/api/v1/plans", map[string]any{
			"name": "Klein", "max_sites": 2, "disk_quota_mb": 100, "is_default": true,
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("Status %d — %s", rec.Code, rec.Body.String())
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &plan); err != nil {
			t.Fatal(err)
		}
		if plan.ID == 0 {
			t.Fatal("Paket ohne ID")
		}
	})

	t.Run("paket zuordnen", func(t *testing.T) {
		rec := ts.do(http.MethodPatch, "/api/v1/tenants/1", map[string]any{"plan_id": plan.ID})
		if rec.Code != http.StatusOK {
			t.Fatalf("Status %d — %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("quota spiegelt das paket", func(t *testing.T) {
		rec := ts.do(http.MethodGet, "/api/v1/quota", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("Status %d — %s", rec.Code, rec.Body.String())
		}

		var status struct {
			PlanName string `json:"plan_name"`
			Entries  []struct {
				Resource string  `json:"resource"`
				Used     int64   `json:"used"`
				Limit    int64   `json:"limit"`
				Percent  float64 `json:"percent"`
			} `json:"entries"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
			t.Fatal(err)
		}
		if status.PlanName != "Klein" {
			t.Fatalf("Paketname %q, erwartet Klein", status.PlanName)
		}

		for _, e := range status.Entries {
			if e.Resource != "sites" {
				continue
			}
			if e.Limit != 2 || e.Used != 1 {
				t.Fatalf("sites: %d/%d, erwartet 1/2", e.Used, e.Limit)
			}
			if e.Percent < 0 {
				t.Fatal("Grenze gesetzt, aber Auslastung unbekannt")
			}
			return
		}
		t.Fatal("keine Zeile für sites in der Quota-Übersicht")
	})

	t.Run("zuordnung wieder loesen", func(t *testing.T) {
		// 0 muss die Zuordnung entfernen — sonst ließe sie sich nie lösen.
		rec := ts.do(http.MethodPatch, "/api/v1/tenants/1", map[string]any{"plan_id": 0})
		if rec.Code != http.StatusOK {
			t.Fatalf("Status %d — %s", rec.Code, rec.Body.String())
		}
		rec = ts.do(http.MethodGet, "/api/v1/quota", nil)
		if !strings.Contains(rec.Body.String(), "ohne Paket") {
			t.Fatalf("Paket weiterhin zugeordnet: %s", rec.Body.String())
		}
	})

	t.Run("mandant mit inhalt laesst sich nicht loeschen", func(t *testing.T) {
		// Tenant 2 (Bob) hat eine Site — das Löschen würde Vhost und
		// Linux-Benutzer verwaist zurücklassen.
		rec := ts.do(http.MethodDelete, "/api/v1/tenants/2", nil)
		if rec.Code != http.StatusConflict {
			t.Fatalf("Status %d, erwartet 409 — %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("eigener mandant ist geschuetzt", func(t *testing.T) {
		rec := ts.do(http.MethodDelete, "/api/v1/tenants/1", nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("Status %d, erwartet 400", rec.Code)
		}
	})

	t.Run("ungueltiger status wird abgelehnt", func(t *testing.T) {
		rec := ts.do(http.MethodPatch, "/api/v1/tenants/2", map[string]any{"status": "geloescht"})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("Status %d, erwartet 400", rec.Code)
		}
	})
}

// TestCustomerCannotManagePlans: Pakete und Mandanten sind Admin-Sache.
func TestCustomerCannotManagePlans(t *testing.T) {
	ts := newTestServer(t)
	ts.login(t, "bob@example.at") // Kunde

	forbidden := []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodPost, "/api/v1/plans", map[string]any{"name": "Frei", "max_sites": 999}},
		{http.MethodPatch, "/api/v1/plans/1", map[string]any{"max_sites": 999}},
		{http.MethodDelete, "/api/v1/plans/1", nil},
		{http.MethodPatch, "/api/v1/tenants/1", map[string]any{"plan_id": 0}},
		{http.MethodDelete, "/api/v1/tenants/1", nil},
	}
	for _, r := range forbidden {
		rec := ts.do(r.method, r.path, r.body)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s als Kunde: Status %d, erwartet 403", r.method, r.path, rec.Code)
		}
	}

	// Den eigenen Verbrauch darf ein Kunde sehen.
	if rec := ts.do(http.MethodGet, "/api/v1/quota", nil); rec.Code != http.StatusOK {
		t.Errorf("eigener Verbrauch: Status %d, erwartet 200", rec.Code)
	}
	// Den eines fremden Mandanten nicht.
	if rec := ts.do(http.MethodGet, "/api/v1/tenants/1/quota", nil); rec.Code == http.StatusOK {
		t.Errorf("Kunde konnte fremden Verbrauch abrufen: %s", rec.Body.String())
	}
}

// TestQuotaBlocksSiteCreation prüft die Durchsetzung über die HTTP-Ebene.
func TestQuotaBlocksSiteCreation(t *testing.T) {
	ts := newTestServer(t)
	ts.login(t, "alice@example.at")

	// Ein Paket, das genau die eine bereits vorhandene Site erlaubt.
	rec := ts.do(http.MethodPost, "/api/v1/plans", map[string]any{"name": "Winzig", "max_sites": 1})
	if rec.Code != http.StatusCreated {
		t.Fatalf("Paket anlegen: %d — %s", rec.Code, rec.Body.String())
	}
	var plan struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if rec := ts.do(http.MethodPatch, "/api/v1/tenants/1", map[string]any{"plan_id": plan.ID}); rec.Code != http.StatusOK {
		t.Fatalf("Paket zuordnen: %d", rec.Code)
	}

	rec = ts.do(http.MethodPost, "/api/v1/sites", map[string]any{
		"domain": "zweite.example.at", "type": "static",
	})
	if rec.Code < 400 || rec.Code >= 500 {
		t.Fatalf("Site über der Quota: Status %d, erwartet 4xx — %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Winzig") {
		t.Fatalf("Meldung nennt das Paket nicht: %s", rec.Body.String())
	}
}

// --- Site-Einstellungen ----------------------------------------------------

func TestSiteSettingsRoutes(t *testing.T) {
	ts := newTestServer(t)
	ts.login(t, "alice@example.at")

	t.Run("gueltige einstellungen", func(t *testing.T) {
		rec := ts.do(http.MethodPatch, "/api/v1/sites/1/settings", map[string]any{
			"redirects":     []map[string]any{{"from": "/alt", "to": "/neu", "code": 301}},
			"deny_ips":      []string{"203.0.113.5", "198.51.100.0/24"},
			"extra_lines":   []string{"add_header X-Robots-Tag noindex;"},
			"max_body_size": "128M",
		})
		// Ohne laufenden Agent scheitert der Vhost-Neubau — die Prüfung der
		// Eingabe muss aber davor stattgefunden haben.
		if rec.Code == http.StatusBadRequest {
			t.Fatalf("gültige Einstellungen abgelehnt: %s", rec.Body.String())
		}
	})

	invalid := []struct {
		name string
		body map[string]any
	}{
		{"zusatzzeile bricht block auf", map[string]any{
			"extra_lines": []string{"} server { listen 8080;"},
		}},
		{"zusatzzeile mit include", map[string]any{
			"extra_lines": []string{"include /etc/passwd;"},
		}},
		{"weiterleitung mit unerlaubtem code", map[string]any{
			"redirects": []map[string]any{{"from": "/x", "to": "/y", "code": 200}},
		}},
		{"unsinnige ip", map[string]any{"deny_ips": []string{"kein.host"}}},
		{"anfragegroesse unsinnig", map[string]any{"max_body_size": "viel"}},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			rec := ts.do(http.MethodPatch, "/api/v1/sites/1/settings", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("Status %d, erwartet 400 — %s", rec.Code, rec.Body.String())
			}
		})
	}

	t.Run("fremde site", func(t *testing.T) {
		ts.login(t, "bob@example.at")
		rec := ts.do(http.MethodGet, "/api/v1/sites/1/settings", nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("Status %d, erwartet 404", rec.Code)
		}
	})
}

// TestCustomerCannotWeakenPHPIsolation: disable_functions leer zu setzen würde
// der Site erlauben, Shell-Kommandos abzusetzen. Das darf ein Kunde nicht für
// sich selbst entscheiden.
func TestCustomerCannotWeakenPHPIsolation(t *testing.T) {
	ts := newTestServer(t)
	ts.login(t, "bob@example.at") // Kunde, Site 2 gehört ihm

	for _, body := range []map[string]any{
		{"disable_functions": ""},
		{"extra_ini": "disable_functions ="},
	} {
		rec := ts.do(http.MethodPatch, "/api/v1/sites/2/php", body)
		if rec.Code != http.StatusForbidden {
			t.Errorf("PATCH mit %v als Kunde: Status %d, erwartet 403 — %s",
				body, rec.Code, rec.Body.String())
		}
	}

	// Die harmlosen Werte darf er ändern (Site 2 ist static, deshalb 400 —
	// aber nicht 403).
	rec := ts.do(http.MethodPatch, "/api/v1/sites/2/php", map[string]any{"memory_limit": "512M"})
	if rec.Code == http.StatusForbidden {
		t.Errorf("Kunde darf memory_limit nicht ändern: %s", rec.Body.String())
	}
}

func TestCertRoutes(t *testing.T) {
	ts := newTestServer(t)
	ts.login(t, "alice@example.at")

	if rec := ts.do(http.MethodGet, "/api/v1/certs", nil); rec.Code != http.StatusOK {
		t.Fatalf("GET /certs: Status %d — %s", rec.Code, rec.Body.String())
	}

	// Ohne acme_email fehlt die Voraussetzung — das muss ein Client- oder
	// Gateway-Fehler sein, kein Absturz.
	rec := ts.do(http.MethodPost, "/api/v1/sites/1/certificate", map[string]any{"wildcard": false})
	if rec.Code >= 500 && rec.Code != http.StatusServiceUnavailable && rec.Code != http.StatusBadGateway {
		t.Fatalf("Zertifikat anfordern: Status %d — %s", rec.Code, rec.Body.String())
	}

	// Ein Wildcard ohne Token muss mit einer erklärenden Meldung scheitern.
	rec = ts.do(http.MethodPost, "/api/v1/sites/1/certificate", map[string]any{"wildcard": true})
	if !strings.Contains(strings.ToLower(rec.Body.String()), "dns-01") &&
		!strings.Contains(strings.ToLower(rec.Body.String()), "acme_email") {
		t.Fatalf("wenig hilfreiche Meldung: %s", rec.Body.String())
	}
}

func TestCloudflareTokenNeverReturned(t *testing.T) {
	ts := newTestServer(t)
	ts.login(t, "alice@example.at")

	const token = "cf_geheimes_token_1234567890"
	rec := ts.do(http.MethodPut, "/api/v1/tenants/1/cloudflare", map[string]string{"token": token})
	if rec.Code != http.StatusOK {
		t.Fatalf("Token setzen: Status %d — %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), token) {
		t.Fatalf("der Token steht in der Antwort: %s", rec.Body.String())
	}

	// Auch die Mandantenliste darf ihn nicht enthalten — nur die Angabe,
	// dass einer hinterlegt ist.
	rec = ts.do(http.MethodGet, "/api/v1/tenants", nil)
	if strings.Contains(rec.Body.String(), token) {
		t.Fatalf("der Token steht in der Mandantenliste: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"has_cloudflare_token":true`) {
		t.Fatalf("das abgeleitete Feld fehlt: %s", rec.Body.String())
	}

	// Leerer Wert entfernt ihn.
	rec = ts.do(http.MethodPut, "/api/v1/tenants/1/cloudflare", map[string]string{"token": ""})
	if !strings.Contains(rec.Body.String(), "false") {
		t.Fatalf("Token wurde nicht entfernt: %s", rec.Body.String())
	}
}

// --- Update über die Oberfläche --------------------------------------------

// TestUpdateStatusNeedsSession: die Version des Panels und der Hinweis auf ein
// Update sind nichts für Unangemeldete.
func TestUpdateStatusNeedsSession(t *testing.T) {
	ts := newTestServer(t)

	if rec := ts.do(http.MethodGet, "/api/v1/system/update", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("Status %d, erwartet 401", rec.Code)
	}
}

// TestUpdateStartNeedsAdmin hält die Rollengrenze fest. Ein Update tauscht
// beide Binaries und startet das Panel neu — das darf kein Kunde auslösen,
// auch nicht für seinen eigenen Mandanten.
func TestUpdateStartNeedsAdmin(t *testing.T) {
	ts := newTestServer(t)
	ts.login(t, "bob@example.at") // bob ist Kunde

	rec := ts.do(http.MethodPost, "/api/v1/system/update", map[string]string{})
	if rec.Code != http.StatusForbidden {
		t.Errorf("Status %d, erwartet 403 — ein Kunde darf das Panel nicht aktualisieren", rec.Code)
	}
}

// TestPHPExtensionsNeedAdmin: ein Modul zu installieren heißt, ein Paket auf
// den Server zu holen. Das ist nichts, was ein Kunde für seine Site entscheidet
// — es gilt für alle Sites derselben PHP-Version.
func TestPHPExtensionsNeedAdmin(t *testing.T) {
	ts := newTestServer(t)
	ts.login(t, "bob@example.at") // Kunde

	paths := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/system/php/8.3/extensions"},
		{http.MethodPost, "/api/v1/system/php/8.3/extensions/install"},
		{http.MethodPost, "/api/v1/system/php/8.3/extensions/toggle"},
	}
	for _, p := range paths {
		rec := ts.do(p.method, p.path, map[string]string{"name": "redis"})
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s: Status %d, erwartet 403", p.method, p.path, rec.Code)
		}
	}
}
