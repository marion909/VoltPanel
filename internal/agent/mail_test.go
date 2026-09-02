package agent

import (
	"encoding/hex"
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
