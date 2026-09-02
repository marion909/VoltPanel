package agent

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Das Passwortfeld, das Dovecot bekommt.
//
// Der Vergleichswert stammt nicht aus diesem Code: er wurde mit Pythons
// hashlib und base64 gerechnet — base64(sha512(passwort + salz) + salz), die
// Schreibweise von {SSHA512}. Eine Selbstprüfung ("kommt zweimal dasselbe
// heraus") wäre auch dann grün, wenn Hash und Salz vertauscht wären, und
// Dovecot lehnte danach jede Anmeldung ab.
func TestSSHA512GegenFremdeRechnung(t *testing.T) {
	salz, err := hex.DecodeString("0001020304050607")
	if err != nil {
		t.Fatal(err)
	}
	will := "{SSHA512}HGNqBaQ0rldGvPAVWHD8QPX3oTaV/EeFcTsMiDwfN51YvGMo/TuoS6JJx1h8wPIJ" +
		"OEMFwEhHh5KIOe/Ym/poyQABAgMEBQYH"

	if got := sshaMitSalz("geheim123", salz); got != will {
		t.Errorf("sshaMitSalz = %q,\nerwartet      %q", got, will)
	}
}

// Zwei Aufrufe mit demselben Passwort müssen verschieden aussehen — sonst
// verrät die Datei, welche zwei Postfächer dasselbe Passwort haben.
func TestSSHA512SalztJedesMalNeu(t *testing.T) {
	a := hashSSHA512("geheim123")
	b := hashSSHA512("geheim123")
	if a == b {
		t.Error("zweimal derselbe hash — das salz ist fest")
	}
	if !strings.HasPrefix(a, "{SSHA512}") || len(a) < 100 {
		t.Errorf("das feld sieht nicht aus wie ein hash: %q", a)
	}
}

// Das Maildir bildet der Agent aus der Adresse. Ein Pfad aus der Anfrage wäre
// ein Weg, ein Postfach an eine beliebige Stelle des Dateisystems zu legen.
func TestMaildirKommtAusDerAdresse(t *testing.T) {
	gut := map[string]string{
		"post@example.at":         "example.at/post",
		"POST@Example.AT":         "example.at/post",
		"vor.nach@sub.example.at": "sub.example.at/vor.nach",
	}
	for adresse, will := range gut {
		got, err := maildirFuer(adresse)
		if err != nil {
			t.Errorf("maildirFuer(%q) = %v", adresse, err)
			continue
		}
		if got != will {
			t.Errorf("maildirFuer(%q) = %q, erwartet %q", adresse, got, will)
		}
	}

	schlecht := []string{
		"",
		"post",
		"../../etc/passwd@example.at",
		"post@../../etc",
		"post@example.at/../../etc",
		"post@example.at\nroot@fremde.at",
		"po st@example.at",
		"post@example",
	}
	for _, adresse := range schlecht {
		if got, err := maildirFuer(adresse); err == nil {
			t.Errorf("maildirFuer(%q) = %q, sollte abgelehnt werden", adresse, got)
		}
	}
}

// Der Aufrufer nennt eine Fähigkeit, keinen Paketnamen. `apt-get install` mit
// einer Eingabe aus dem Browser wäre eine Rootshell mit Umweg — apt führt
// Postinst-Skripte als root aus.
func TestFeatureNimmtKeinenPaketnamen(t *testing.T) {
	for _, name := range FeatureNames() {
		if !ValidFeature(name) {
			t.Errorf("%q steht in der liste, gilt aber nicht", name)
		}
	}
	for _, name := range []string{
		"", "nginx", "docker.io", "docker;bash", "../../etc", "sl",
		"fail2ban fail2ban", "DOCKER", "node", "nodejs",
	} {
		if ValidFeature(name) {
			t.Errorf("%q wurde als fähigkeit angenommen", name)
		}
	}
}

// Redis ist Phase 7: der erste Eintrag im Plugin-Katalog. Er muss über
// genau denselben Weg laufen wie jede andere Fähigkeit — keine zweite,
// nachgebaute Installationslogik nur für Plugins.
func TestRedisIstEineBekannteFaehigkeit(t *testing.T) {
	if !ValidFeature("redis") {
		t.Fatal(`"redis" gilt nicht als fähigkeit`)
	}
	found := false
	for _, n := range FeatureNames() {
		if n == "redis" {
			found = true
		}
	}
	if !found {
		t.Error(`"redis" fehlt in FeatureNames()`)
	}
}

// opFeatureUninstall muss dieselbe Schranke haben wie opFeatureInstall: ein
// Name aus der Anfrage darf nie zu einem Paketnamen werden, den apt zu sehen
// bekommt. Geprüft wird das, bevor überhaupt ein Prozess startet — ein
// unbekannter Name kommt gar nicht bis zu apt-get.
//
// Die Meldung wird ausdrücklich mitgeprüft, nicht nur "irgendein Fehler": in
// einer Testumgebung ohne apt-get schlägt der Aufruf ohnehin fehl, egal ob die
// Sperre greift oder nicht — ein Test, der das nicht unterscheidet, wäre grün,
// selbst wenn die Prüfung fehlte. Genau darauf bin ich beim ersten Versuch
// hereingefallen.
func TestFeatureUninstallNimmtKeinenPaketnamen(t *testing.T) {
	srv, _ := testServer(t)
	for _, name := range []string{
		"", "nginx", "docker.io", "docker;bash", "../../etc", "DOCKER",
	} {
		raw, err := json.Marshal(map[string]string{"feature": name})
		if err != nil {
			t.Fatal(err)
		}
		_, err = srv.opFeatureUninstall(t.Context(), raw)
		if err == nil {
			t.Errorf("%q wurde als fähigkeit zum entfernen angenommen", name)
			continue
		}
		if !strings.Contains(err.Error(), "bekannte fähigkeit") {
			t.Errorf("%q wurde abgelehnt, aber aus dem falschen grund: %v", name, err)
		}
	}
}

func TestDockerFeatureStartetDenDienst(t *testing.T) {
	srv, _ := testServer(t)
	dir := t.TempDir()
	logPath := filepath.Join(dir, "systemctl.log")
	systemctl := filepath.Join(dir, "systemctl")
	skript := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + logPath + "\n"
	if err := os.WriteFile(systemctl, []byte(skript), 0o755); err != nil {
		t.Fatal(err)
	}

	alt := allowedBinaries["systemctl"]
	allowedBinaries["systemctl"] = systemctl
	t.Cleanup(func() { allowedBinaries["systemctl"] = alt })

	if err := srv.featureDiensteStarten(t.Context(), "docker"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	aufrufe := string(got)
	for _, will := range []string{"enable docker\n", "start docker\n"} {
		if !strings.Contains(aufrufe, will) {
			t.Errorf("%q fehlt in den systemctl-Aufrufen:\n%s", will, aufrufe)
		}
	}
}

func TestDockerFeatureMeldetNichtStartklarenDaemon(t *testing.T) {
	err := dockerFeatureBereit(DockerStatus{
		Installed: true,
		Warnings:  []string{"Docker ist installiert, der Daemon antwortet aber nicht: boom"},
	})
	if err == nil || !strings.Contains(err.Error(), "nicht startklar") {
		t.Fatalf("dockerFeatureBereit = %v, erwartet klare Fehlermeldung", err)
	}
}
