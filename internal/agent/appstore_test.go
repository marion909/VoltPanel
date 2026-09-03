package agent

import (
	"archive/tar"
	"context"
	"crypto/sha1" //nolint:gosec // Testet gegen dasselbe Format, das wordpress.org tatsächlich ausliefert.
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sha1Hex ist die Prüfsumme, wie wordpress.org sie neben dem Archiv
// veröffentlicht — hier zum Bauen der Testantwort, nicht zum Prüfen.
func sha1Hex(data []byte) string {
	sum := sha1.Sum(data) //nolint:gosec
	return hex.EncodeToString(sum[:])
}

// Die Datei unter wordpressChecksumURL enthält nur die Summe selbst — anders
// als bei Node steht kein Dateiname daneben. Ein Format, das das nicht
// unterscheidet, akzeptierte am Ende auch eine Node-artige Zeile
// "abc…  wordpress-6.7.tar.gz" und schnitte den Dateinamen als Teil der
// vermeintlichen Prüfsumme mit — oder verwürfe die echte Summe als "zu lang".
func TestParseWordPressChecksum(t *testing.T) {
	gut := strings.Repeat("a1", 20) // 40 Zeichen, gültiges Hex
	faelle := map[string]bool{
		gut:                        true,
		gut + "\n":                 true, // trailing newline, wie curl es liefert
		"  " + gut + "  ":          true,
		strings.ToUpper(gut):       true,
		"":                         false,
		gut[:39]:                   false, // einen zu kurz
		gut + "a":                  false, // einen zu lang
		gut[:38] + "gg":            false, // nicht-hex am Ende
		gut + "  wordpress.tar.gz": false, // Node-Format mit Dateinamen
		"<html>fehler</html>":      false, // eine Fehlerseite statt der Summe
	}
	for in, sollGehen := range faelle {
		_, err := parseWordPressChecksum([]byte(in))
		if sollGehen && err != nil {
			t.Errorf("parseWordPressChecksum(%q) = %v, sollte gehen", in, err)
		}
		if !sollGehen && err == nil {
			t.Errorf("parseWordPressChecksum(%q) wurde angenommen, sollte scheitern", in)
		}
	}

	// Groß- wird zu Kleinschreibung normalisiert — die SHA-1-Summe, die
	// fetchAndExtract zurückgibt, ist immer klein.
	got, err := parseWordPressChecksum([]byte(strings.ToUpper(gut)))
	if err != nil || got != gut {
		t.Errorf("groß geschrieben ergab %q, %v — erwartet %q, nil", got, err, gut)
	}
}

// opAppStoreWordPress muss den Systembenutzer prüfen, bevor irgendein Byte
// über das Netz geht. Ein WordPress, das unter einem fremden oder
// Systemkonto liefe, wäre PHP-Code, der unter dieser Kennung liest und
// schreibt.
func TestOpAppStoreWordPressLehntFremdenSystembenutzerAb(t *testing.T) {
	// wordpressURL zeigt für diesen Test absichtlich auf nichts Erreichbares.
	// Würde die Prüfung des Systembenutzers fehlen oder übersprungen, liefe
	// der Aufruf bis zu diesem Netzwerkzugriff durch — und in einer
	// Testumgebung ohne Netz schlüge auch *der* fehl, mit einer ganz anderen
	// Meldung. Deshalb wird hier nicht nur "err != nil" verlangt, sondern die
	// konkrete Meldung der jeweiligen Schranke.
	altURL := wordpressURL
	wordpressURL = "http://127.0.0.1:1/nirgendwo" // Port 1: garantiert nichts, das antwortet
	t.Cleanup(func() { wordpressURL = altURL })

	srv, sitesDir := testServer(t)
	dest := filepath.Join(sitesDir, "example.at", "public")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}

	faelle := map[string]string{
		"root":     "reservierter systembenutzer",
		"www-data": "reservierter systembenutzer",
		"volt":     "reservierter systembenutzer",
		"alice":    "kein systembenutzer einer site",
		"":         "benutzername",
	}
	for benutzer, erwartet := range faelle {
		raw, err := json.Marshal(WordPressInstallParams{
			WebRoot: dest, SystemUser: benutzer, WebGroup: "www-data",
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = srv.opAppStoreWordPress(t.Context(), raw)
		if err == nil {
			t.Errorf("systembenutzer %q wurde angenommen", benutzer)
			continue
		}
		if !strings.Contains(err.Error(), erwartet) {
			t.Errorf("systembenutzer %q: abgelehnt, aber aus dem falschen grund: %v",
				benutzer, err)
		}
	}
}

// wordpressTarGz baut ein Archiv wie das von wordpress.org: alles unter einem
// einzigen "wordpress/"-Wurzelverzeichnis.
func wordpressTarGz(t *testing.T, inhalt map[string]string) []byte {
	t.Helper()
	eintraege := make([]*tar.Header, 0, len(inhalt))
	for name := range inhalt {
		eintraege = append(eintraege, &tar.Header{
			Name: "wordpress/" + name, Mode: 0o644, Typeflag: tar.TypeReg,
		})
	}
	daten := make(map[string]string, len(inhalt))
	for name, wert := range inhalt {
		daten["wordpress/"+name] = wert
	}
	return tarGz(t, eintraege, daten)
}

// TestInstallWordPressFilesRundgang ist der Kern: laden, gegen die Summe
// halten, auspacken, die Platzhalterseite von CreateSite entfernen.
//
// Ohne echten Systembenutzer geprüft — das braucht applyOwner, das hier nicht
// aufgerufen wird. Genau deshalb ist installWordPressFiles eine eigene
// Funktion und nicht in opAppStoreWordPress verschweißt.
func TestInstallWordPressFilesRundgang(t *testing.T) {
	archiv := wordpressTarGz(t, map[string]string{
		"index.php":            "<?php // wordpress",
		"wp-config-sample.php": "<?php // sample config",
		"wp-admin/install.php": "<?php // installer",
	})

	mux := http.NewServeMux()
	mux.HandleFunc("/latest.tar.gz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archiv)
	})
	mux.HandleFunc("/latest.tar.gz.sha1", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(sha1Hex(archiv) + "\n"))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	altURL, altSum := wordpressURL, wordpressChecksumURL
	wordpressURL = ts.URL + "/latest.tar.gz"
	wordpressChecksumURL = ts.URL + "/latest.tar.gz.sha1"
	t.Cleanup(func() { wordpressURL, wordpressChecksumURL = altURL, altSum })

	dest := t.TempDir()
	// Die Platzhalterseite, wie CreateSite sie hinterlässt.
	if err := os.WriteFile(filepath.Join(dest, "index.html"), []byte("platzhalter"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := installWordPressFiles(context.Background(), dest)
	if err != nil {
		t.Fatalf("installWordPressFiles: %v", err)
	}
	if got != sha1Hex(archiv) {
		t.Errorf("zurückgegebene summe %q passt nicht zum archiv", got)
	}

	if _, err := os.Stat(filepath.Join(dest, "index.html")); err == nil {
		t.Error("die platzhalterseite steht noch da — nginx würde sie vor index.php ausliefern")
	}
	for _, datei := range []string{"index.php", "wp-config-sample.php", "wp-admin/install.php"} {
		if _, err := os.Stat(filepath.Join(dest, datei)); err != nil {
			t.Errorf("%s fehlt nach dem auspacken: %v", datei, err)
		}
	}
	// Das Wurzelverzeichnis "wordpress/" selbst darf nicht mit ausgepackt
	// worden sein — die Dateien gehören direkt in dest.
	if _, err := os.Stat(filepath.Join(dest, "wordpress")); err == nil {
		t.Error(`ein verzeichnis "wordpress" liegt in dest — das wurzelverzeichnis wurde nicht abgeschnitten`)
	}
}

// Stimmt die Prüfsumme nicht, landet nichts in dest — weder der halb
// ausgepackte Stand noch der Platzhalter wird angefasst.
func TestInstallWordPressFilesLehntFalscheSummeAb(t *testing.T) {
	archiv := wordpressTarGz(t, map[string]string{"index.php": "<?php"})

	mux := http.NewServeMux()
	mux.HandleFunc("/latest.tar.gz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archiv)
	})
	mux.HandleFunc("/latest.tar.gz.sha1", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("0", 40)))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	altURL, altSum := wordpressURL, wordpressChecksumURL
	wordpressURL = ts.URL + "/latest.tar.gz"
	wordpressChecksumURL = ts.URL + "/latest.tar.gz.sha1"
	t.Cleanup(func() { wordpressURL, wordpressChecksumURL = altURL, altSum })

	dest := t.TempDir()
	if err := os.WriteFile(filepath.Join(dest, "index.html"), []byte("platzhalter"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := installWordPressFiles(context.Background(), dest); err == nil {
		t.Fatal("eine falsche prüfsumme wurde angenommen")
	}
	if _, err := os.Stat(filepath.Join(dest, "index.html")); err != nil {
		t.Error("die platzhalterseite wurde entfernt, obwohl die installation scheiterte")
	}
	if _, err := os.Stat(filepath.Join(dest, "index.php")); err == nil {
		t.Error("wordpress-dateien stehen trotz falscher prüfsumme in dest")
	}
}
