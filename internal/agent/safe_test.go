package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestJailBlocksEscapes ist der Kern des Datei-Schutzes. Fällt einer dieser
// Fälle um, kann der Web-Prozess über den Root-Daemon beliebige Systemdateien
// lesen oder überschreiben.
func TestJailBlocksEscapes(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	site := filepath.Join(root, "example.at")
	if err := os.MkdirAll(filepath.Join(site, "public"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "shadow"), []byte("geheim"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Ein Symlink aus der Site heraus — der klassische Ausbruch.
	escapeLink := filepath.Join(site, "escape")
	if err := os.Symlink(outside, escapeLink); err != nil {
		t.Fatal(err)
	}
	// Ein Symlink, der innerhalb der Wurzel bleibt, muss weiterhin gehen.
	innerLink := filepath.Join(site, "inner")
	if err := os.Symlink(filepath.Join(site, "public"), innerLink); err != nil {
		t.Fatal(err)
	}

	roots := []string{root}

	denied := []struct {
		name string
		path string
	}{
		{"traversal mit ..", filepath.Join(site, "..", "..", "etc", "passwd")},
		{"symlink nach draußen", escapeLink},
		{"datei hinter symlink nach draußen", filepath.Join(escapeLink, "shadow")},
		{"absoluter fremdpfad", "/etc/shadow"},
		{"relativer pfad", "example.at/public"},
		{"leerer pfad", ""},
		{"nullbyte", filepath.Join(site, "public") + "\x00/etc/passwd"},
		{"präfix-verwechslung", root + "-evil/datei"},
	}
	for _, tc := range denied {
		t.Run("verweigert: "+tc.name, func(t *testing.T) {
			if got, err := jail(tc.path, roots); err == nil {
				t.Fatalf("jail(%q) hat %q erlaubt, erwartet war eine Ablehnung", tc.path, got)
			}
		})
	}

	allowed := []struct {
		name string
		path string
	}{
		{"vorhandenes verzeichnis", filepath.Join(site, "public")},
		{"noch nicht vorhandene datei", filepath.Join(site, "public", "index.php")},
		{"tiefer noch nicht vorhandener pfad", filepath.Join(site, "a", "b", "c.txt")},
		{"symlink innerhalb der wurzel", innerLink},
		{"wurzel selbst", root},
		{"normalisiertes .. innerhalb", filepath.Join(site, "public", "..", "public")},
	}
	for _, tc := range allowed {
		t.Run("erlaubt: "+tc.name, func(t *testing.T) {
			got, err := jail(tc.path, roots)
			if err != nil {
				t.Fatalf("jail(%q) verweigert: %v", tc.path, err)
			}
			realRoot, _ := filepath.EvalSymlinks(root)
			if got != realRoot && !strings.HasPrefix(got, realRoot+string(filepath.Separator)) {
				t.Fatalf("jail(%q) = %q liegt außerhalb von %q", tc.path, got, realRoot)
			}
		})
	}
}

// TestJailErlaubtWurzelUebergreifendeSymlinks deckt die Testlücke ab, dass
// TestJailBlocksEscapes jail() nur mit einer einzelnen Wurzel aufruft. In
// Produktion läuft jail() immer mit der vollen s.roots-Liste (SitesDir,
// NginxDir, PHPDir, CertDir, LogDir, BackupDir zusammen) — kein bestehender
// Test prüft einen Symlink, der von einer Wurzel in eine *andere* Wurzel
// oder in eine Nachbar-Site derselben Wurzel führt.
//
// Das ist kein Bug, sondern dokumentiertes Verhalten: jail() prüft "liegt
// der aufgelöste Pfad unter irgendeiner der übergebenen Wurzeln", nicht
// "unter der für diese Operation vorgesehenen engeren Wurzel" — genau die
// Lücke, die internal/core/files.go (joinInside) mit einer zusätzlichen,
// engeren Prüfung pro Site schließt. Dieser Test hält das Verhalten von
// jail() selbst fest, damit eine künftige Änderung daran auffällt.
func TestJailErlaubtWurzelUebergreifendeSymlinks(t *testing.T) {
	sitesRoot := t.TempDir()
	certRoot := t.TempDir()
	roots := []string{sitesRoot, certRoot}

	mandantA := filepath.Join(sitesRoot, "mandant-a")
	mandantB := filepath.Join(sitesRoot, "mandant-b")
	if err := os.MkdirAll(mandantA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(mandantB, 0o755); err != nil {
		t.Fatal(err)
	}

	// Ein Symlink aus Mandant A in eine andere Wurzel (certRoot) — die Ziel-
	// wurzel ist selbst erlaubt, jail() lässt das durch.
	nachAndererWurzel := filepath.Join(mandantA, "zu-cert-root")
	if err := os.Symlink(certRoot, nachAndererWurzel); err != nil {
		t.Fatal(err)
	}
	if got, err := jail(nachAndererWurzel, roots); err != nil {
		t.Fatalf("jail() lehnte einen Symlink in eine andere erlaubte Wurzel ab: %v", err)
	} else {
		realCertRoot, _ := filepath.EvalSymlinks(certRoot)
		if got != realCertRoot {
			t.Fatalf("jail() = %q, erwartet %q", got, realCertRoot)
		}
	}

	// Ein Symlink aus Mandant A zu Mandant B, derselben Wurzel — auch das
	// lässt jail() durch, weil beide unter sitesRoot liegen. Die
	// mandantenscharfe Trennung ist nicht Aufgabe von jail(), sondern der
	// engeren Prüfung in internal/core/files.go.
	nachNachbarSite := filepath.Join(mandantA, "zu-mandant-b")
	if err := os.Symlink(mandantB, nachNachbarSite); err != nil {
		t.Fatal(err)
	}
	if got, err := jail(nachNachbarSite, roots); err != nil {
		t.Fatalf("jail() lehnte einen Symlink zu einer Nachbar-Site derselben Wurzel ab: %v", err)
	} else {
		realMandantB, _ := filepath.EvalSymlinks(mandantB)
		if got != realMandantB {
			t.Fatalf("jail() = %q, erwartet %q", got, realMandantB)
		}
	}
}

func TestCheckServiceWhitelist(t *testing.T) {
	ok := []string{"nginx", "mariadb", "php8.3-fpm", "php7.4-fpm", "nginx.service", "docker"}
	for _, n := range ok {
		if err := checkService(n); err != nil {
			t.Errorf("checkService(%q) = %v, erwartet erlaubt", n, err)
		}
	}

	// sshd fehlt bewusst in der Whitelist: ein Stop würde den Server unerreichbar machen.
	bad := []string{"sshd", "ssh", "systemd-logind", "nginx; rm -rf /", "../../etc/passwd",
		"php9.9-fpm-evil", "", "nginx\nmariadb"}
	for _, n := range bad {
		if err := checkService(n); err == nil {
			t.Errorf("checkService(%q) erlaubt, erwartet war eine Ablehnung", n)
		}
	}
}

func TestCheckUsernameRejectsSystemAccounts(t *testing.T) {
	for _, n := range []string{"root", "www-data", "volt", "mysql", "nobody"} {
		if err := checkUsername(n); err == nil {
			t.Errorf("checkUsername(%q) erlaubt, erwartet war eine Ablehnung", n)
		}
	}
	for _, n := range []string{"root; id", "ab c", "UPPER", "-dash", "", strings.Repeat("a", 40)} {
		if err := checkUsername(n); err == nil {
			t.Errorf("checkUsername(%q) erlaubt, erwartet war eine Ablehnung", n)
		}
	}
	for _, n := range []string{"site_example", "kunde01", "web-1"} {
		if err := checkUsername(n); err != nil {
			t.Errorf("checkUsername(%q) = %v, erwartet erlaubt", n, err)
		}
	}
}

func TestCheckDomain(t *testing.T) {
	for _, d := range []string{"example.at", "sub.example.at", "*.example.at", "a-b.example.co.uk"} {
		if _, err := checkDomain(d); err != nil {
			t.Errorf("checkDomain(%q) = %v, erwartet erlaubt", d, err)
		}
	}
	for _, d := range []string{"", "example", "../etc", "exa mple.at", "example.at/../x",
		"example.at;rm -rf /", "-example.at", strings.Repeat("a", 250) + ".at"} {
		if _, err := checkDomain(d); err == nil {
			t.Errorf("checkDomain(%q) erlaubt, erwartet war eine Ablehnung", d)
		}
	}
}

// TestCheckDomainNormalisiertGrossschreibung hält die Lücke fest, die
// checkDomain vorher hatte: es prüfte zwar kleingeschrieben, gab aber nie den
// normalisierten String zurück. Aufrufer, die mit dem Original weiterarbeiten,
// könnten auf einem case-sensitiven Dateisystem (ext4/XFS) "Example.com" und
// "example.com" als zwei getrennte Vhost-/Log-Dateien behandeln.
func TestCheckDomainNormalisiertGrossschreibung(t *testing.T) {
	got, err := checkDomain("Example.AT")
	if err != nil {
		t.Fatal(err)
	}
	if got != "example.at" {
		t.Errorf("checkDomain(%q) = %q, erwartet %q", "Example.AT", got, "example.at")
	}
}

// TestRunRejectsUnlistedBinary belegt, dass es keinen Weg zu einem beliebigen
// Kommando gibt — auch nicht über einen absoluten Pfad.
func TestRunRejectsUnlistedBinary(t *testing.T) {
	for _, bin := range []string{"sh", "bash", "/bin/sh", "rm", "curl"} {
		if _, err := run(t.Context(), 1, bin, "-c", "echo pwned"); err == nil {
			t.Errorf("run(%q) erlaubt, erwartet war eine Ablehnung", bin)
		}
	}
}
