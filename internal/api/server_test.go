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
