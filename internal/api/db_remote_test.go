package api

import (
	"context"
	"net/http"
	"strconv"
	"testing"

	"github.com/marion909/voltpanel/internal/store"
)

// seedFremdeDatenbank legt Alice eine Datenbank mit Benutzer und einer
// Herkunft an. Ohne echte Zeilen liefe der Test unten ins Leere: eine
// nicht vorhandene ID ergibt auch bei kaputter Mandantentrennung ein 404.
func seedFremdeDatenbank(t *testing.T, ts *testServer) (userID, hostID int64) {
	t.Helper()
	ctx, sys := context.Background(), store.SystemScope()

	db := &store.Database{TenantID: 1, Name: "alice_shop", Charset: "utf8mb4"}
	if err := ts.store.CreateDatabase(ctx, sys, db); err != nil {
		t.Fatal(err)
	}
	user := &store.DBUser{
		TenantID: 1, DatabaseID: db.ID, Username: "alice_shop",
		HostPattern: "localhost", Grants: "ALL", PasswordEnc: "x",
	}
	if err := ts.store.CreateDBUser(ctx, sys, user); err != nil {
		t.Fatal(err)
	}
	host := &store.DBRemoteHost{TenantID: 1, DBUserID: user.ID, Host: "203.0.113.5"}
	if err := ts.store.CreateRemoteHost(ctx, sys, host); err != nil {
		t.Fatal(err)
	}
	return user.ID, host.ID
}

// TestHerkunftsListeBleibtImMandanten: die Liste sagt, von welchen Adressen aus
// jemand an eine fremde Datenbank kommt. Für Bob darf Alices Benutzer nicht von
// einem nicht vorhandenen zu unterscheiden sein.
func TestHerkunftsListeBleibtImMandanten(t *testing.T) {
	ts := newTestServer(t)
	userID, hostID := seedFremdeDatenbank(t, ts)
	ts.login(t, "bob@example.at")

	prüfe := func(method, path string, body any) {
		t.Helper()
		rec := ts.do(method, path, body)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s als fremder Mandant: Status %d, erwartet 404 — %s",
				method, path, rec.Code, rec.Body.String())
		}
	}

	base := "/api/v1/db-users/" + strconv.FormatInt(userID, 10)
	prüfe(http.MethodGet, base+"/hosts", nil)
	prüfe(http.MethodPost, base+"/hosts", map[string]string{"host": "198.51.100.7"})
	prüfe(http.MethodDelete, "/api/v1/db-hosts/"+strconv.FormatInt(hostID, 10), nil)

	// Und die Zeile steht danach noch da.
	hosts, err := ts.store.ListRemoteHosts(context.Background(), store.SystemScope(), userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 1 {
		t.Errorf("Alices Herkunftsliste hat jetzt %d Einträge, erwartet 1", len(hosts))
	}
}

// TestServerInsNetzStellenNurAlsAdministrator: der Schalter startet MariaDB neu
// und öffnet einen Port. Er gilt für alle Mandanten auf dem Server, nicht für
// den, der ihn drückt.
func TestServerInsNetzStellenNurAlsAdministrator(t *testing.T) {
	ts := newTestServer(t)
	ts.login(t, "bob@example.at")

	rec := ts.do(http.MethodPost, "/api/v1/databases-remote", map[string]bool{"enabled": true})
	if rec.Code != http.StatusForbidden {
		t.Errorf("Netzzugang einschalten als Kunde: Status %d, erwartet 403 — %s",
			rec.Code, rec.Body.String())
	}
}

// TestHerkunftsListeBrauchtEineSitzung: ohne Anmeldung gar nichts.
func TestHerkunftsListeBrauchtEineSitzung(t *testing.T) {
	ts := newTestServer(t)

	for _, path := range []string{"/api/v1/db-users/1/hosts", "/api/v1/databases-remote"} {
		rec := ts.do(http.MethodGet, path, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("GET %s ohne Sitzung: Status %d, erwartet 401", path, rec.Code)
		}
	}
}
