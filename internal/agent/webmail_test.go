package agent

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func roundcubeTarGz(t *testing.T, inhalt map[string]string) []byte {
	t.Helper()
	eintraege := make([]*tar.Header, 0, len(inhalt))
	for name := range inhalt {
		eintraege = append(eintraege, &tar.Header{
			Name: "roundcubemail-1.7.3/" + name, Mode: 0o644, Typeflag: tar.TypeReg,
		})
	}
	daten := make(map[string]string, len(inhalt))
	for name, wert := range inhalt {
		daten["roundcubemail-1.7.3/"+name] = wert
	}
	return tarGz(t, eintraege, daten)
}

func mitTestArchiv(t *testing.T, archiv []byte) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/roundcube.tar.gz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archiv)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	altURL, altSum := roundcubeURL, roundcubeSHA256
	roundcubeURL = ts.URL + "/roundcube.tar.gz"
	roundcubeSHA256 = sha256Hex(archiv)
	t.Cleanup(func() { roundcubeURL, roundcubeSHA256 = altURL, altSum })
}

// installRoundcubeFiles ist der Kern: laden, gegen die Summe halten,
// auspacken, das Wurzelverzeichnis des Archivs abschneiden.
//
// Ohne echten Systembenutzer geprüft — das braucht applyOwner, das hier
// nicht aufgerufen wird. Genau deshalb ist installRoundcubeFiles eine eigene
// Funktion und nicht in opWebmailInstall verschweißt, wie bei WordPress.
func TestInstallRoundcubeFilesRundgang(t *testing.T) {
	archiv := roundcubeTarGz(t, map[string]string{
		"index.php":                    "<?php // roundcube",
		"config/config.inc.php.sample": "<?php // sample config",
		"SQL/mysql.initial.sql":        "-- schema",
	})
	mitTestArchiv(t, archiv)

	dest := t.TempDir()
	if err := installRoundcubeFiles(context.Background(), dest); err != nil {
		t.Fatalf("installRoundcubeFiles: %v", err)
	}

	for _, datei := range []string{"index.php", "config/config.inc.php.sample", "SQL/mysql.initial.sql"} {
		if _, err := os.Stat(filepath.Join(dest, datei)); err != nil {
			t.Errorf("%s fehlt nach dem auspacken: %v", datei, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dest, "roundcubemail-1.7.3")); err == nil {
		t.Error(`ein verzeichnis "roundcubemail-1.7.3" liegt in dest — das wurzelverzeichnis wurde nicht abgeschnitten`)
	}
}

// Stimmt die Prüfsumme nicht, landet nichts in dest.
func TestInstallRoundcubeFilesLehntFalscheSummeAb(t *testing.T) {
	archiv := roundcubeTarGz(t, map[string]string{"index.php": "<?php"})
	mitTestArchiv(t, archiv)
	roundcubeSHA256 = strings.Repeat("0", 64) // die echte Summe wieder verwerfen

	dest := t.TempDir()
	if err := installRoundcubeFiles(context.Background(), dest); err == nil {
		t.Fatal("eine falsche prüfsumme wurde angenommen")
	}
	if _, err := os.Stat(filepath.Join(dest, "index.php")); err == nil {
		t.Error("roundcube-dateien stehen trotz falscher prüfsumme in dest")
	}
}

// opWebmailInstall muss den Systembenutzer prüfen, bevor irgendein Byte über
// das Netz geht. roundcubeURL zeigt für diesen Test absichtlich auf nichts
// Erreichbares — würde die Prüfung fehlen, liefe der Aufruf bis zu *diesem*
// Netzwerkzugriff durch und schlüge mit einer ganz anderen Meldung fehl.
// Deshalb wird auf den genauen Text geprüft, nicht nur auf "err != nil".
func TestOpWebmailInstallLehntFremdenSystembenutzerAb(t *testing.T) {
	srv, dest := testServer(t)
	altURL := roundcubeURL
	roundcubeURL = "http://127.0.0.1:1/nichts-hier"
	t.Cleanup(func() { roundcubeURL = altURL })

	faelle := map[string]string{
		"root":            "reservierter systembenutzer",
		"nicht-vorhanden": "gibt es nicht",
	}
	for benutzer, erwartet := range faelle {
		raw, err := json.Marshal(WebmailInstallParams{
			WebRoot: filepath.Join(dest, "_webmail"), SystemUser: benutzer, WebGroup: "www-data",
			DBName: "volt_webmail", DBUser: "volt_webmail", DBPassword: "x",
			IMAPPort: 993, SMTPPort: 587,
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = srv.opWebmailInstall(t.Context(), raw)
		if err == nil {
			t.Fatalf("benutzer %q wurde angenommen", benutzer)
		}
		if !strings.Contains(err.Error(), erwartet) {
			t.Errorf("benutzer %q: meldung %q enthält nicht %q", benutzer, err.Error(), erwartet)
		}
	}
}

// Ein Datenbankname, der reMyDBName nicht besteht, darf nie beim Import
// ankommen — auch wenn er in diesem Panel immer eine feste Konstante ist,
// nicht aus einer Anfrage.
//
// Der Systembenutzer dieses Tests ist der, unter dem der Testprozess selbst
// läuft — ein Name, der auf der Maschine wirklich existiert, mit einer uid
// über 1000. Ohne einen echten Benutzer schlüge schon unprivilegierteIDs mit
// "gibt es nicht" fehl, und dieser Test bestünde aus dem falschen Grund: der
// Datenbankname käme nie zur Prüfung.
func TestOpWebmailInstallLehntUngueltigenDatenbanknamenAb(t *testing.T) {
	echt, err := user.Current()
	if err != nil {
		t.Skip("kein aktueller benutzer ermittelbar")
	}
	uid, _ := strconv.Atoi(echt.Uid)
	if uid < 1000 || !reUsername.MatchString(echt.Username) {
		t.Skipf("aktueller benutzer %q taugt für diesen test nicht (uid %d)", echt.Username, uid)
	}

	srv, dest := testServer(t)
	raw, err := json.Marshal(WebmailInstallParams{
		WebRoot: filepath.Join(dest, "_webmail"), SystemUser: echt.Username, WebGroup: "www-data",
		DBName: "'; DROP TABLE users; --", DBUser: "volt_webmail", DBPassword: "x",
		IMAPPort: 993, SMTPPort: 587,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = srv.opWebmailInstall(t.Context(), raw)
	if err == nil {
		t.Fatal("ein ungültiger datenbankname wurde angenommen")
	}
	if !strings.Contains(err.Error(), "datenbankname") {
		t.Errorf("abgelehnt, aber aus dem falschen grund: %v", err)
	}
}
