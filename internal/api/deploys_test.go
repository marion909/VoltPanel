package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/marion909/voltpanel/internal/store"
)

// seedDeploy legt einen Deploy direkt in der Datenbank an und gibt das
// Klartext-Geheimnis zurück.
func seedDeploy(t *testing.T, ts *testServer, site *store.Site) (*store.Deploy, string) {
	t.Helper()
	hookID, err := store.NewHookID()
	if err != nil {
		t.Fatal(err)
	}
	secret, err := store.NewHookSecret()
	if err != nil {
		t.Fatal(err)
	}
	enc, err := ts.server.secrets.Encrypt(secret)
	if err != nil {
		t.Fatal(err)
	}
	d := &store.Deploy{
		TenantID: site.TenantID, SiteID: site.ID,
		RepoURL: "https://github.com/a/b.git", Ref: "main",
		HookID: hookID, HookSecretEnc: enc, AutoDeploy: true,
	}
	if err := ts.store.CreateDeploy(t.Context(), store.SystemScope(), d); err != nil {
		t.Fatal(err)
	}
	return d, secret
}

func hookPost(ts *testServer, hookID string, headers map[string]string, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/hooks/deploy/"+hookID, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	ts.server.echo.ServeHTTP(rec, req)
	return rec
}

func hookSig(secret, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// TestWebhookBrauchtKeineSitzung: der Hoster hat keine. Der Ausweis ist die
// Signatur — und ohne sie kommt niemand durch.
func TestWebhookBrauchtKeineSitzung(t *testing.T) {
	ts := newTestServer(t)
	site := appSite(t, ts, "alice", "app.alice.example.at")
	d, secret := seedDeploy(t, ts, site)

	body := `{"ref":"refs/heads/main"}`

	// Ohne Signatur: 404, nicht 401. Der Unterschied verriete, dass es diese
	// Adresse gibt.
	if rec := hookPost(ts, d.HookID, nil, body); rec.Code != http.StatusNotFound {
		t.Errorf("ohne Signatur: Status %d, erwartet 404 — %s", rec.Code, rec.Body.String())
	}
	// Mit falscher Signatur: dieselbe Antwort.
	falsch := hookPost(ts, d.HookID, map[string]string{
		"X-Hub-Signature-256": hookSig("anderes", body)}, body)
	if falsch.Code != http.StatusNotFound {
		t.Errorf("falsche Signatur: Status %d, erwartet 404", falsch.Code)
	}
	// Und für eine Adresse, die es nicht gibt: wieder dieselbe.
	unbekannt := hookPost(ts, "0123456789abcdef0123456789abcdef", map[string]string{
		"X-Hub-Signature-256": hookSig(secret, body)}, body)
	if unbekannt.Code != falsch.Code || unbekannt.Body.String() != falsch.Body.String() {
		t.Errorf("unbekannte Adresse: %d %s — falsche Signatur: %d %s",
			unbekannt.Code, unbekannt.Body.String(), falsch.Code, falsch.Body.String())
	}

	// Mit richtiger Signatur wird angenommen.
	richtig := hookPost(ts, d.HookID, map[string]string{
		"X-Hub-Signature-256": hookSig(secret, body)}, body)
	if richtig.Code != http.StatusAccepted {
		t.Errorf("richtige Signatur: Status %d, erwartet 202 — %s",
			richtig.Code, richtig.Body.String())
	}
}

// TestWebhookLiegtAusserhalbDesZugriffspfads: GitHub kennt den Pfad des
// Betreibers nicht und soll ihn nicht erfahren.
func TestWebhookLiegtAusserhalbDesZugriffspfads(t *testing.T) {
	srv := serverWithAccessPath(t)

	req := httptest.NewRequest(http.MethodPost, "/hooks/deploy/0123456789abcdef0123456789abcdef",
		strings.NewReader("{}"))
	rec := httptest.NewRecorder()
	srv.echo.ServeHTTP(rec, req)

	// 404 wegen der unbekannten Adresse — aber nicht, weil die Route fehlt.
	// Der Unterschied zeigt sich am Rumpf: eine fehlende Route beantwortet
	// echo mit dem SPA-Fallback oder "unbekannter endpunkt".
	if rec.Code != http.StatusNotFound {
		t.Fatalf("Status %d, erwartet 404 — %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "nicht gefunden") {
		t.Errorf("die Route gibt es außerhalb des Zugriffspfads nicht: %s", rec.Body.String())
	}
}

// TestDeployBleibtImMandanten: dieselbe Zusage wie überall.
func TestDeployBleibtImMandanten(t *testing.T) {
	ts := newTestServer(t)
	site := appSite(t, ts, "alice", "app.alice.example.at")
	d, _ := seedDeploy(t, ts, site)

	ts.login(t, "bob@example.at")

	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/deploys/1/run"},
		{http.MethodGet, "/api/v1/deploys/1/releases"},
		{http.MethodGet, "/api/v1/deploys/1/key"},
		{http.MethodPost, "/api/v1/deploys/1/rollback"},
		{http.MethodDelete, "/api/v1/deploys/1"},
	} {
		rec := ts.do(tc.method, tc.path, map[string]any{"release": "20260901-120000"})
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s: Status %d, erwartet 404 — %s",
				tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}

	rec := ts.do(http.MethodGet, "/api/v1/deploys", nil)
	var liste []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &liste); err != nil {
		t.Fatal(err)
	}
	if len(liste) != 0 {
		t.Errorf("Bob sieht %d fremde Deploys", len(liste))
	}
	_ = d
}

// TestHookGeheimnisKommtNieZurueck: es liegt verschlüsselt, und es erneut
// herauszugeben hieße, ein Geheimnis aus der Datenbank zu holen, das dort für
// den Server liegt und nicht für den Betrachter.
func TestHookGeheimnisKommtNieZurueck(t *testing.T) {
	ts := newTestServer(t)
	site := appSite(t, ts, "alice", "app.alice.example.at")
	_, secret := seedDeploy(t, ts, site)

	ts.login(t, "alice@example.at")
	rec := ts.do(http.MethodGet, "/api/v1/deploys", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("Status %d — %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), secret) {
		t.Errorf("das Hook-Geheimnis steht im Klartext in der Antwort: %s", rec.Body.String())
	}
	// Auch nicht verschlüsselt: ein Geheimtext, den jeder Angemeldete abholen
	// kann, ist ein Geheimtext, den jemand offline angreifen kann. Geprüft wird
	// deshalb, dass das Feld gar nicht erst herauskommt.
	for _, feld := range []string{"hook_secret", "hook_secret_enc", "HookSecretEnc"} {
		if strings.Contains(rec.Body.String(), feld) {
			t.Errorf("%q steht in der Antwort: %s", feld, rec.Body.String())
		}
	}
	// Die Hook-Adresse gehört dazu — ohne sie kann niemand den Webhook
	// eintragen.
	if !strings.Contains(rec.Body.String(), "hook_url") {
		t.Errorf("die Hook-Adresse fehlt: %s", rec.Body.String())
	}
}
