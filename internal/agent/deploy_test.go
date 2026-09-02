package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marion909/voltpanel/internal/gitspec"
)

func deployCall(t *testing.T, srv *Server, op Op, params any) error {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	switch op {
	case OpDeployRun:
		_, err = srv.opDeployRun(t.Context(), raw)
	case OpDeployList:
		_, err = srv.opDeployList(t.Context(), raw)
	case OpDeployRollback:
		_, err = srv.opDeployRollback(t.Context(), raw)
	}
	return err
}

// TestDeployPruefungenGreifenVorDemKlon: alles, was der Deploy an git und an
// die Buildschritte weitergibt, wird vorher geprüft — bevor irgendein Prozess
// startet.
func TestDeployPruefungenGreifenVorDemKlon(t *testing.T) {
	srv, _ := testServer(t)
	root := srv.roots[0] + "/kunde.example.at"

	basis := DeployParams{
		Name: "kunde-example-at", SystemUser: "site_kunde", RootPath: root,
		RepoURL: "https://github.com/a/b.git", Ref: "main",
	}

	// Zu jedem Fall der Grund, der kommen muss. Ein Test auf "irgendein
	// Fehler" wäre hier grün, ohne etwas zu zeigen: auf einem Rechner ohne die
	// Systembenutzer scheitert der Aufruf ohnehin, und genau darauf bin ich
	// beim ersten Anlauf hereingefallen.
	cases := map[string]struct {
		kaputt func(*DeployParams)
		grund  string
	}{
		"Adresse als Kommando": {
			func(p *DeployParams) { p.RepoURL = "ext::sh -c whoami" },
			"zeichen, die dort nicht vorkommen"},
		"Adresse als Option": {
			func(p *DeployParams) { p.RepoURL = "--upload-pack=/bin/sh" },
			"bindestrich"},
		"lokale Datei": {
			func(p *DeployParams) { p.RepoURL = "file:///etc" },
			"wird nicht unterstützt"},
		"unverschlüsseltes Protokoll": {
			func(p *DeployParams) { p.RepoURL = "git://host/x.git" },
			"wird nicht unterstützt"},
		"ssh-Option im Hostnamen": {
			func(p *DeployParams) { p.RepoURL = "ssh://-oProxyCommand=id/x.git" },
			"hostname"},
		"Passwort in der Adresse": {
			func(p *DeployParams) { p.RepoURL = "https://u:geheim@host/x.git" },
			"kein passwort in der adresse"},
		"Branch als Option": {
			func(p *DeployParams) { p.Ref = "--upload-pack=id" },
			"branch- oder tagname"},
		"Pfad außerhalb": {
			func(p *DeployParams) { p.RootPath = "/etc" },
			"außerhalb der erlaubten verzeichnisse"},
		"Name mit Pfadwechsel": {
			func(p *DeployParams) { p.Name = "../../etc/passwd" },
			"kein gültiger name"},
		"unbekannter Buildschritt": {
			func(p *DeployParams) { p.Steps = []string{"rm -rf /"} },
			"kein bekannter buildschritt"},
		"Buildschritt als Kommando": {
			func(p *DeployParams) { p.Steps = []string{"npm run build && curl evil"} },
			"kein bekannter buildschritt"},
	}

	for name, tc := range cases {
		p := basis
		tc.kaputt(&p)
		err := deployCall(t, srv, OpDeployRun, p)
		if err == nil {
			t.Errorf("%s wurde angenommen", name)
			continue
		}
		if !strings.Contains(err.Error(), tc.grund) {
			t.Errorf("%s abgelehnt, aber aus dem falschen Grund: %v", name, err)
		}
	}
}

// TestBuildschritteSindNamen: eine Kommandozeile vom Kunden müsste jemand
// zerlegen, und wer zerlegt, landet früher oder später bei einer Shell. Ein
// Name schlägt eine feste Argumentliste nach — oder er wird abgelehnt.
func TestBuildschritteSindNamen(t *testing.T) {
	for name, cmd := range gitspec.Steps {
		if len(cmd) == 0 {
			t.Errorf("%s hat kein kommando", name)
			continue
		}
		if allowedBinaries[cmd[0]] == "" {
			t.Errorf("%s ruft %q auf, das nicht in allowedBinaries steht", name, cmd[0])
		}
		for _, arg := range cmd {
			if strings.ContainsAny(arg, " \t\n;|&$`") {
				t.Errorf("%s: das Argument %q enthält Trennzeichen", name, arg)
			}
		}
	}
	// Und was nicht in der Liste steht, gibt es nicht.
	for _, fremd := range []string{"", "sh", "npm", "npm ci", "NPM-CI", "npm-ci; id"} {
		if gitspec.ValidStep(fremd) {
			t.Errorf("%q steht in der Liste der Buildschritte", fremd)
		}
	}
}

// TestNurEigeneStaendeWerdenAufgeraeumt: ein Verzeichnis, das jemand von Hand
// nach releases/ gelegt hat, bleibt stehen. Aufräumen darf nie mehr wegnehmen,
// als es selbst angelegt hat.
func TestNurEigeneStaendeWerdenAufgeraeumt(t *testing.T) {
	for _, eigen := range []string{"20260901-120000", "20260101-000000"} {
		if !validRelease(eigen) {
			t.Errorf("%q gilt nicht als eigener Stand", eigen)
		}
	}
	for _, fremd := range []string{
		"", ".", "..", "wichtige-daten", "20260901", "20260901-1200000",
		"20260901-12000a", "../etc", "20260901-120000.bak",
	} {
		if validRelease(fremd) {
			t.Errorf("%q würde aufgeräumt", fremd)
		}
	}
}

// TestRollbackNurAufEinenStand: der Name wird ein Verzeichnisname unterhalb von
// releases/. Ein "../.." wäre sonst ein Symlink auf ein beliebiges Verzeichnis
// des Servers — ausgeliefert unter der Domain des Kunden.
func TestRollbackNurAufEinenStand(t *testing.T) {
	srv, _ := testServer(t)
	root := srv.roots[0] + "/kunde.example.at"
	if err := os.MkdirAll(filepath.Join(root, releasesDir), 0o755); err != nil {
		t.Fatal(err)
	}

	for _, release := range []string{
		"../../../etc", "..", "/etc", "20260901-120000/../../..", "",
	} {
		err := deployCall(t, srv, OpDeployRollback, DeployRollbackParams{
			SystemUser: "site_kunde", RootPath: root, Release: release,
		})
		if err == nil {
			t.Errorf("der Rollback auf %q wurde angenommen", release)
			continue
		}
		if !strings.Contains(err.Error(), "ist kein stand") {
			t.Errorf("%q abgelehnt, aber aus dem falschen Grund: %v", release, err)
		}
	}
}

// TestUmschaltenIstEinSchritt prüft das Ergebnis des Umschaltens: current zeigt
// auf den neuen Stand, und der temporäre Name bleibt nicht liegen.
//
// Was er *nicht* prüft, ist die Lückenlosigkeit selbst. Dass rename(2) den
// Symlink in einem Schritt ersetzt — statt eines `rm` mit anschließendem `ln`,
// das dazwischen einen Moment ohne current hätte —, ist eine Zusage des
// Betriebssystems, keine dieses Codes.
//
// Ein nebenläufiger Test dafür ist auf diesem Rechner nicht möglich: macOS gibt
// readlink(2) mit EINVAL zurück, wenn es mit einem rename auf denselben Pfad
// zusammentrifft, obwohl lstat den Symlink weiterhin sieht. Nachgestellt mit
// einem Programm ohne jeden VoltPanel-Code — 233 von 400 Leseversuchen — also
// die Plattform und nicht diese Funktion. Auf Linux, wo das Panel läuft, tritt
// das nicht auf; nachprüfen lässt es sich hier trotzdem nicht, und einen Test,
// den ich nicht fallen sehen kann, schiebe ich nicht ein.
func TestUmschaltenIstEinSchritt(t *testing.T) {
	root := t.TempDir()
	alt := filepath.Join(root, releasesDir, "20260101-000000")
	neu := filepath.Join(root, releasesDir, "20260901-120000")
	for _, d := range []string{alt, neu} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	srv, _ := testServer(t)
	// uid/gid des laufenden Prozesses: Lchown auf eigene Dateien geht ohne
	// Rechte, ein fremder Eigentümer nicht.
	plan := &deployPlan{root: root, uid: os.Getuid(), gid: os.Getgid()}

	if err := srv.switchCurrent(t.Context(), plan, alt); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, currentLink)
	if ziel, _ := os.Readlink(link); ziel != alt {
		t.Fatalf("current zeigt auf %q", ziel)
	}

	// Umschalten auf den neuen Stand: der Link darf nie fehlen, auch nicht
	// kurz. Nach dem zweiten Aufruf zeigt er auf den neuen.
	if err := srv.switchCurrent(t.Context(), plan, neu); err != nil {
		t.Fatal(err)
	}
	if ziel, _ := os.Readlink(link); ziel != neu {
		t.Errorf("current zeigt nach dem Umschalten auf %q", ziel)
	}
	// Kein Rest vom temporären Namen.
	if _, err := os.Lstat(link + ".neu"); err == nil {
		t.Error("der temporäre Symlink ist liegengeblieben")
	}
}

// TestAufraeumenLaesstDenGueltigenStehen: nach einem Rollback ist der neueste
// Stand nicht der benutzte. Ihn wegzuräumen hieße, die laufende Site zu löschen.
func TestAufraeumenLaesstDenGueltigenStehen(t *testing.T) {
	root := t.TempDir()
	var alle []string
	for _, name := range []string{
		"20260101-000000", "20260102-000000", "20260103-000000", "20260104-000000",
		"20260105-000000", "20260106-000000", "20260107-000000",
	} {
		d := filepath.Join(root, releasesDir, name)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		alle = append(alle, d)
	}
	// Ein fremdes Verzeichnis, das nicht von uns stammt.
	fremd := filepath.Join(root, releasesDir, "wichtige-daten")
	if err := os.MkdirAll(fremd, 0o755); err != nil {
		t.Fatal(err)
	}

	srv, _ := testServer(t)
	plan := &deployPlan{root: root, uid: os.Getuid(), gid: os.Getgid()}
	// current zeigt auf den ältesten — genau der Fall nach einem Rollback.
	if err := srv.switchCurrent(t.Context(), plan, alle[0]); err != nil {
		t.Fatal(err)
	}

	srv.pruneReleases(plan)

	if _, err := os.Stat(alle[0]); err != nil {
		t.Error("der gültige Stand wurde aufgeräumt — die Site wäre jetzt leer")
	}
	if _, err := os.Stat(fremd); err != nil {
		t.Error("ein fremdes Verzeichnis wurde aufgeräumt")
	}
	// Die jüngsten fünf bleiben.
	for _, d := range alle[len(alle)-keepReleases:] {
		if _, err := os.Stat(d); err != nil {
			t.Errorf("%s fehlt, obwohl er zu den jüngsten %d gehört", filepath.Base(d), keepReleases)
		}
	}
}

// Derselbe Fall, aber ohne sich auf das Temporärverzeichnis der Plattform zu
// verlassen: die Site liegt hinter einem Symlink.
//
// Das ist keine Erfindung für den Test. /home, das auf /srv/home zeigt, oder
// ein Site-Verzeichnis auf einer nachträglich eingehängten Platte — dann ist
// der aufgelöste Pfad ein anderer als der gespeicherte, und wer die beiden
// Zeichenketten vergleicht, räumt den Stand weg, der gerade ausgeliefert wird.
//
// Gefunden hat das nicht dieser Test, sondern der darüber: unter macOS liegt
// das Temporärverzeichnis hinter /var -> /private/var, und dort fiel es auf.
// Auf Linux wäre es erst einem Kunden aufgefallen.
func TestAufraeumenErkenntDenGueltigenHinterEinemSymlink(t *testing.T) {
	tmp := t.TempDir()
	echt := filepath.Join(tmp, "echt")
	if err := os.MkdirAll(echt, 0o755); err != nil {
		t.Fatal(err)
	}
	verweis := filepath.Join(tmp, "verweis")
	if err := os.Symlink(echt, verweis); err != nil {
		t.Fatal(err)
	}

	// Der Server kennt die Site nur über den Weg durch den Symlink.
	root := filepath.Join(verweis, "site")
	var alle []string
	for _, name := range []string{
		"20260101-000000", "20260102-000000", "20260103-000000", "20260104-000000",
		"20260105-000000", "20260106-000000", "20260107-000000",
	} {
		d := filepath.Join(root, releasesDir, name)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		alle = append(alle, d)
	}

	srv, _ := testServer(t)
	plan := &deployPlan{root: root, uid: os.Getuid(), gid: os.Getgid()}
	if err := srv.switchCurrent(t.Context(), plan, alle[0]); err != nil {
		t.Fatal(err)
	}

	srv.pruneReleases(plan)

	if _, err := os.Stat(alle[0]); err != nil {
		t.Error("hinter einem symlink wurde der gültige stand aufgeräumt")
	}
}
