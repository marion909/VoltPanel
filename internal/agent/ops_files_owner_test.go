package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCheckFileGroupLehntReservierteGruppenAb deckt die Lücke ab, die
// applyOwner vorher hatte: owner wurde über checkUsername geprüft, group
// dagegen gar nicht — file.chown/file.write/file.mkdir konnten die Gruppe
// einer Datei auf jede existierende Systemgruppe setzen, z. B. "root".
func TestCheckFileGroupLehntReservierteGruppenAb(t *testing.T) {
	for _, g := range []string{"root", "mysql", "www-data", "volt-agent"} {
		if err := checkFileGroup(g); err == nil {
			t.Errorf("checkFileGroup(%q) = nil, erwartet war eine Ablehnung", g)
		}
	}
}

func TestCheckFileGroupErlaubtGewoehnlicheGruppen(t *testing.T) {
	for _, g := range []string{"site_shop", "kunde_gruppe"} {
		if err := checkFileGroup(g); err != nil {
			t.Errorf("checkFileGroup(%q) = %v, erwartet war keine Ablehnung", g, err)
		}
	}
}

// TestOpFileChownLehntReservierteGruppeAb hält den Fund end-to-end fest:
// eine reservierte Zielgruppe muss scheitern, bevor überhaupt ein Lchown
// versucht wird — deshalb funktioniert der Test auch ohne root-Rechte.
func TestOpFileChownLehntReservierteGruppeAb(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "datei.txt")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := &Server{roots: []string{dir}}

	raw, err := json.Marshal(FileChownParams{Path: path, Owner: "site_shop", Group: "root"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.opFileChown(context.Background(), raw); err == nil {
		t.Fatal("opFileChown akzeptierte group=root")
	}
}

// TestOpFileMkdirGroupErlaubtWebserverGruppe hält den Fund fest, der jede neue
// Site am Anlegen scheitern ließ: SiteService.applyWebPermissions ruft
// Client.MkdirGroup mit group=www-data (config.WebGroup) auf — genau die
// Gruppe, die checkFileGroup sperrt. Lief das über denselben Op wie der
// Datei-Manager eines Mandanten (file.mkdir), schlug jedes Anlegen einer Site
// mit "www-data ist eine reservierte systemgruppe" fehl. file.mkdir_group ist
// der eigene, getrennte Op dafür, den ein Mandant nie erreicht (files.go
// schickt beim eigenen Mkdir nie eine Gruppe mit).
func TestOpFileMkdirGroupErlaubtWebserverGruppe(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "site", "public")
	srv := &Server{roots: []string{dir}}

	raw, err := json.Marshal(FileMkdirParams{Path: path, Mode: 0o750, Owner: "site_shop", Group: "www-data"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = srv.opFileMkdirGroup(context.Background(), raw)
	// Fehlt die Gruppe www-data auf dem Testsystem oder fehlen Rechte zum
	// Chown, scheitert das eigentliche os.Chown — kein Testfehler. Die
	// Behauptung ist nur, dass checkFileGroup hier nicht mehr dazwischenkommt.
	if err != nil && strings.Contains(err.Error(), "reservierte systemgruppe") {
		t.Fatalf("opFileMkdirGroup lehnt group=www-data weiterhin ab: %v", err)
	}
}

// TestOpFileMkdirLehntWeiterhinReservierteGruppeAb ist die Kehrseite: der
// Datei-Manager-Op eines Mandanten (file.mkdir) bleibt gesperrt — nur der
// getrennte file.mkdir_group-Op für die interne Site-Provisionierung darf
// die Webserver-Gruppe setzen.
func TestOpFileMkdirLehntWeiterhinReservierteGruppeAb(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "site", "public")
	srv := &Server{roots: []string{dir}}

	raw, err := json.Marshal(FileMkdirParams{Path: path, Mode: 0o750, Owner: "site_shop", Group: "www-data"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.opFileMkdir(context.Background(), raw); err == nil {
		t.Fatal("opFileMkdir akzeptierte group=www-data")
	}
}

// TestApplyOwnerPrueftEigentuemerNichtMehrSelbst hält die Kehrseite des Fixes
// fest: applyOwner vertraut jetzt seinem Aufrufer für den Eigentümer, statt
// selbst über checkUsername zu sperren. Ohne diese Änderung scheiterten die
// beiden internen, vertrauenswürdigen Aufrufer, die bewusst "root" als
// Eigentümer übergeben (ops_app.go für die App-Umgebungsdatei,
// ops_htpasswd.go für die Basic-Auth-Datei), an genau dieser Sperre — bei
// ops_app.go als harter Fehler bei jedem App-Schreibvorgang mit
// Umgebungsvariablen.
func TestApplyOwnerPrueftEigentuemerNichtMehrSelbst(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "datei.txt")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := applyOwner(path, "root", "", false)
	// Läuft dieser Test nicht als root, scheitert das eigentliche Lchown an
	// fehlenden Rechten — das ist kein Testfehler. Die Behauptung ist nur,
	// dass die Validierung selbst "root" nicht mehr als reservierten
	// Systembenutzer ablehnt.
	if err != nil && strings.Contains(err.Error(), "reservierter systembenutzer") {
		t.Fatalf("applyOwner lehnt owner=root weiterhin über checkUsername ab: %v", err)
	}
}
