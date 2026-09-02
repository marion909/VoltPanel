package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/marion909/voltpanel/internal/store"
)

// mailDomainFuer legt eine Maildomäne samt Postfach für einen Mandanten an.
func mailDomainFuer(t *testing.T, ts *testServer, slug string) (*store.MailDomain, *store.Mailbox) {
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

	dom := &store.MailDomain{TenantID: tenantID, Domain: slug + ".mail.example.at", Active: true}
	if err := ts.store.CreateMailDomain(ctx, sys, dom); err != nil {
		t.Fatal(err)
	}
	enc, err := ts.server.secrets.Encrypt("ein-langes-passwort")
	if err != nil {
		t.Fatal(err)
	}
	box := &store.Mailbox{
		TenantID: tenantID, DomainID: dom.ID, LocalPart: "post",
		PasswordEnc: enc, Active: true,
	}
	if err := ts.store.CreateMailbox(ctx, sys, box); err != nil {
		t.Fatal(err)
	}
	return dom, box
}

// Dieselbe Zusage wie überall, diesmal für Mail: mit einer fremden ID kommt
// niemand an eine fremde Domäne, ein fremdes Postfach — und schon gar nicht
// an ein fremdes Passwort.
func TestMailBleibtImMandantenUeberHTTP(t *testing.T) {
	ts := newTestServer(t)
	dom, box := mailDomainFuer(t, ts, "alice")

	ts.login(t, "bob@example.at") // anderer Mandant

	domID := strconv.FormatInt(dom.ID, 10)
	boxID := strconv.FormatInt(box.ID, 10)

	for _, tc := range []struct{ method, path string }{
		{http.MethodPatch, "/api/v1/mail/domains/" + domID},
		{http.MethodDelete, "/api/v1/mail/domains/" + domID},
		{http.MethodPatch, "/api/v1/mail/mailboxes/" + boxID},
		{http.MethodDelete, "/api/v1/mail/mailboxes/" + boxID},
		// Der wichtigste: das Passwort eines fremden Postfachs.
		{http.MethodGet, "/api/v1/mail/mailboxes/" + boxID + "/password"},
	} {
		rec := ts.do(tc.method, tc.path, map[string]any{"quota_mb": 10})
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s: Status %d, erwartet 404 — %s",
				tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}

	// Und in den Listen taucht nichts davon auf.
	for _, pfad := range []string{"/api/v1/mail/domains", "/api/v1/mail/mailboxes", "/api/v1/mail/aliases"} {
		rec := ts.do(http.MethodGet, pfad, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: Status %d — %s", pfad, rec.Code, rec.Body.String())
		}
		var liste []map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &liste); err != nil {
			t.Fatal(err)
		}
		if len(liste) != 0 {
			t.Errorf("%s: Bob sieht %d fremde Einträge: %v", pfad, len(liste), liste)
		}
	}

	// Ein Postfach in Alices Domäne anzulegen muss ebenfalls scheitern.
	rec := ts.do(http.MethodPost, "/api/v1/mail/mailboxes", map[string]any{
		"domain_id":  dom.ID,
		"local_part": "eingeschmuggelt",
		"password":   "ein-langes-passwort",
	})
	if rec.Code == http.StatusCreated {
		t.Error("bob hat ein postfach in alices domäne angelegt")
	}
}

// Das Passwort steht nie in der Liste — nur beim ausdrücklichen Abruf, und der
// steht im Audit-Log.
func TestMailPasswortStehtNichtInDerListe(t *testing.T) {
	ts := newTestServer(t)
	mailDomainFuer(t, ts, "alice")
	ts.login(t, "alice@example.at")

	rec := ts.do(http.MethodGet, "/api/v1/mail/mailboxes", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("Status %d — %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, verboten := range []string{"password_enc", "ein-langes-passwort", "PasswordEnc"} {
		if strings.Contains(body, verboten) {
			t.Errorf("%q steht in der liste:\n%s", verboten, body)
		}
	}
}

// Nachinstallieren ist eine Serversache. Wer keine Dienste starten darf, darf
// erst recht keine Pakete installieren — apt führt Postinst-Skripte als root
// aus.
func TestFeatureInstallNurFuerAdmins(t *testing.T) {
	ts := newTestServer(t)
	ts.login(t, "bob@example.at") // Kunde

	for _, pfad := range []string{"/api/v1/system/features", "/api/v1/system/features/docker"} {
		methode := http.MethodGet
		if strings.HasSuffix(pfad, "/docker") {
			methode = http.MethodPost
		}
		rec := ts.do(methode, pfad, nil)
		if rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
			t.Errorf("%s %s: Status %d, erwartet 403 — %s",
				methode, pfad, rec.Code, rec.Body.String())
		}
	}
}
