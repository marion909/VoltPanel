package core

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/marion909/voltpanel/internal/config"
	"github.com/marion909/voltpanel/internal/store"
)

// TestNurArchiveAusDemBackupVerzeichnis ist die Kernzusage des Uploads.
//
// Der Aufrufer nennt einen Dateinamen, und die Datei wandert an einen fremden
// Speicher, den er selbst eingerichtet hat. Ohne diese Prüfung wäre "lade
// dieses Backup hoch" der Weg, jede Datei des Servers in einen fremden Bucket
// zu schieben — /etc/shadow, den Panel-Schlüssel, die Datenbank eines anderen
// Mandanten.
func TestNurArchiveAusDemBackupVerzeichnis(t *testing.T) {
	dir := t.TempDir()
	svc := &BackupService{cfg: &config.Config{BackupDir: dir}}

	abgelehnt := []string{
		"../../etc/shadow",
		"/etc/shadow",
		"../../../root/.ssh/id_ed25519",
		"..",
		".",
		"",
		"volt.tar",      // kein Archiv
		"volt.sql",      // kein Archiv
		"unterordner/x", // kein Archiv, und ein Pfad
	}
	for _, name := range abgelehnt {
		if got, err := svc.archivePath(name); err == nil {
			t.Errorf("%q wurde angenommen und ergab %q", name, got)
		}
	}

	// Ein gültiger Name ergibt genau den Pfad im Backup-Verzeichnis.
	want := filepath.Join(dir, "volt-20260101-000000.tar.gz")
	got, err := svc.archivePath("volt-20260101-000000.tar.gz")
	if err != nil {
		t.Fatalf("ein gültiger Name wurde abgelehnt: %v", err)
	}
	if got != want {
		t.Errorf("archivePath = %q, erwartet %q", got, want)
	}

	// Und ein Verzeichnisanteil wird abgeworfen, nicht mitgenommen: aus
	// "a/b/volt.tar.gz" darf nie ein Zugriff auf a/b werden.
	got, err = svc.archivePath("beliebig/tief/volt-x.tar.gz")
	if err != nil {
		t.Fatalf("unerwartet abgelehnt: %v", err)
	}
	if filepath.Dir(got) != filepath.Clean(dir) {
		t.Errorf("archivePath führte aus dem Verzeichnis heraus: %q", got)
	}
}

// TestB2EndpunktWirdAbgeleitet: der Kunde kennt seine Region als
// "eu-central-003" und soll den Hostnamen von Backblaze nicht auswendig
// können müssen.
func TestB2EndpunktWirdAbgeleitet(t *testing.T) {
	if got := b2Endpoint("eu-central-003"); got != "s3.eu-central-003.backblazeb2.com" {
		t.Errorf("b2Endpoint = %q", got)
	}
}

// TestGeheimnisBleibtBeiLeeremFeldStehen.
//
// Das Formular schickt das Geheimnis nicht mit, wenn es unverändert bleiben
// soll — anders ginge es nicht, denn es wird nie ausgeliefert. Ohne diese
// Unterscheidung löschte jedes Speichern die Zugangsdaten, und das Ziel
// scheiterte ab dann still.
func TestGeheimnisBleibtBeiLeeremFeldStehen(t *testing.T) {
	svc := &BackupService{cfg: &config.Config{}}

	t.Run("bestehendes ziel", func(t *testing.T) {
		ziel := &store.BackupTarget{}
		ziel.SecretEnc = "verschlüsselt"
		if err := svc.apply(ziel, TargetInput{
			Name: "Sicherung", Kind: "s3", Endpoint: "s3.example.at",
			Region: "eu", Bucket: "b",
		}, false); err != nil {
			t.Fatal(err)
		}
		if ziel.SecretEnc != "verschlüsselt" {
			t.Errorf("das Geheimnis wurde überschrieben: %q", ziel.SecretEnc)
		}
	})

	t.Run("neues ziel ohne geheimnis", func(t *testing.T) {
		ziel := &store.BackupTarget{}
		ziel.SecretEnc = ""
		err := svc.apply(ziel, TargetInput{Name: "Neu", Kind: "s3"}, true)
		if err == nil {
			t.Fatal("ein neues S3-Ziel ohne Geheimnis wurde angelegt")
		}
		if !strings.Contains(err.Error(), "geheimnis") {
			t.Errorf("abgelehnt, aber aus dem falschen Grund: %v", err)
		}
	})
}
