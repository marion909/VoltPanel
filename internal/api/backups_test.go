package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/marion909/voltpanel/internal/store"
)

// seedFremdesZiel legt Alice ein Backup-Ziel an.
func seedFremdesZiel(t *testing.T, ts *testServer) int64 {
	t.Helper()
	target := &store.BackupTarget{
		TenantID: 1, Name: "Alices Speicher", Kind: "s3",
		Endpoint: "s3.example.at", Region: "eu-central-1", Bucket: "alice",
		Username: "AKIA", SecretEnc: "verschlüsselt", Enabled: true,
	}
	if err := ts.store.CreateBackupTarget(context.Background(), store.SystemScope(), target); err != nil {
		t.Fatal(err)
	}
	return target.ID
}

// TestBackupZielBleibtImMandanten: ein Ziel trägt Zugangsdaten zu fremdem
// Speicher. Für Bob darf Alices Ziel nicht von einem nicht vorhandenen zu
// unterscheiden sein — und schon gar nicht änderbar.
func TestBackupZielBleibtImMandanten(t *testing.T) {
	ts := newTestServer(t)
	id := seedFremdesZiel(t, ts)
	ts.login(t, "bob@example.at")

	pfad := "/api/v1/backup-targets/" + strconv.FormatInt(id, 10)
	fälle := []struct {
		method, path string
		body         any
	}{
		{http.MethodPatch, pfad, map[string]any{"name": "Übernommen", "kind": "s3"}},
		{http.MethodDelete, pfad, nil},
		{http.MethodPost, pfad + "/test", nil},
	}
	for _, f := range fälle {
		rec := ts.do(f.method, f.path, f.body)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s als fremder Mandant: Status %d, erwartet 404 — %s",
				f.method, f.path, rec.Code, rec.Body.String())
		}
	}

	// Und in seiner Liste taucht es nicht auf.
	rec := ts.do(http.MethodGet, "/api/v1/backup-targets", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("Liste: Status %d", rec.Code)
	}
	if body := rec.Body.String(); body != "[]\n" && body != "[]" {
		t.Errorf("Bob sieht fremde Ziele: %s", body)
	}
}

// TestArchivHochladenNurAlsAdministrator.
//
// Die Archive liegen serverweit in einem Verzeichnis und enthalten die
// Panel-Datenbank — also die Daten aller Mandanten. Sie an einen Speicher zu
// schicken, den ein Kunde selbst eingerichtet hat, wäre die vollständige
// Weitergabe des Servers an ihn.
func TestArchivHochladenNurAlsAdministrator(t *testing.T) {
	ts := newTestServer(t)
	id := seedFremdesZiel(t, ts)
	ts.login(t, "bob@example.at")

	rec := ts.do(http.MethodPost,
		"/api/v1/backup-targets/"+strconv.FormatInt(id, 10)+"/upload",
		map[string]string{"filename": "volt-20260101-000000.tar.gz"})
	if rec.Code != http.StatusForbidden {
		t.Errorf("Upload als Kunde: Status %d, erwartet 403 — %s", rec.Code, rec.Body.String())
	}

	rec = ts.do(http.MethodPost, "/api/v1/backups", map[string]any{"include_config": true})
	if rec.Code != http.StatusForbidden {
		t.Errorf("Backup erzeugen als Kunde: Status %d, erwartet 403 — %s",
			rec.Code, rec.Body.String())
	}
}

// TestBackupZielGibtDasGeheimnisNichtHeraus.
//
// Es ist entschlüsselbar in der Panel-Datenbank hinterlegt, weil eine Signatur
// es im Klartext braucht. Ausgeliefert wird es nie — sonst genügte ein Blick in
// die Antwort der Liste, um an fremden Speicher zu kommen.
func TestBackupZielGibtDasGeheimnisNichtHeraus(t *testing.T) {
	ts := newTestServer(t)
	seedFremdesZiel(t, ts)
	ts.login(t, "alice@example.at")

	rec := ts.do(http.MethodGet, "/api/v1/backup-targets", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("Liste: Status %d — %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "verschlüsselt") || strings.Contains(body, "secret_enc") {
		t.Errorf("das Geheimnis steht in der Antwort: %s", body)
	}
	// Dass eines hinterlegt ist, muss dagegen sichtbar sein — sonst kann die
	// Oberfläche "leer lassen heisst unverändert" nicht erklären.
	if !strings.Contains(body, `"has_secret":true`) {
		t.Errorf("has_secret fehlt in der Antwort: %s", body)
	}
}
