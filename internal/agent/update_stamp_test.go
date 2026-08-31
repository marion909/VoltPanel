package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestFileStampErkenntDenTausch deckt die Frage ab, an der der Neustart nach
// einem Update hängt: wurde das Programm auf der Platte ersetzt?
//
// Vorher wurde das allein an der Ausgabe von `volt --version` festgemacht.
// Liefert die nichts — ein Fehler beim Aufruf genügt —, sah ein erfolgreiches
// Update aus wie ein Leerlauf, volt-web lief weiter mit dem alten Programm,
// und in der Oberfläche fehlte alles Neue.
func TestFileStampErkenntDenTausch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "volt")

	if got := fileStamp(path); got != "" {
		t.Errorf("eine fehlende Datei ergab %q, erwartet eine leere Angabe", got)
	}

	if err := os.WriteFile(path, []byte("alt"), 0o755); err != nil {
		t.Fatal(err)
	}
	before := fileStamp(path)
	if before == "" {
		t.Fatal("eine vorhandene Datei ergab keine Angabe")
	}
	if fileStamp(path) != before {
		t.Error("zweimal dieselbe Datei ergab zwei verschiedene Angaben")
	}

	// Gleiche Länge, anderer Inhalt: erkannt wird das über die Änderungszeit.
	// Sie muss deshalb fein genug aufgelöst sein — auf Sekunden gerundet wäre
	// ein Tausch innerhalb derselben Sekunde unsichtbar.
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(path, []byte("neu"), 0o755); err != nil {
		t.Fatal(err)
	}
	if after := fileStamp(path); after == before {
		t.Errorf("nach dem Tausch dieselbe Angabe %q — der Neustart bliebe aus", after)
	}
}
