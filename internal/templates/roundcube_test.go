package templates

import (
	"strings"
	"testing"
)

func gueltigeRoundcubeConfig(t *testing.T) RoundcubeConfigData {
	t.Helper()
	key, err := NewRoundcubeDESKey()
	if err != nil {
		t.Fatal(err)
	}
	return RoundcubeConfigData{
		DBUser: "volt_webmail", DBPassword: "ein-generiertes-passwort", DBName: "volt_webmail",
		IMAPPort: 993, SMTPPort: 587, DESKey: key,
	}
}

func TestNewRoundcubeDESKeyIst24Zeichen(t *testing.T) {
	key, err := NewRoundcubeDESKey()
	if err != nil {
		t.Fatal(err)
	}
	if len(key) != 24 {
		t.Errorf("länge %d, erwartet 24: %q", len(key), key)
	}
}

func TestNewRoundcubeDESKeyIstJedesMalAnders(t *testing.T) {
	a, err := NewRoundcubeDESKey()
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewRoundcubeDESKey()
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Error("zwei aufrufe ergeben denselben schlüssel")
	}
}

// Ein Datenbankpasswort mit "@" oder "%" — beides erlaubte Zeichen bei
// authn.GeneratePassword — muss innerhalb der DSN-URL kodiert werden. Ohne
// url.UserPassword verschöbe ein "@" im Passwort die Grenze zwischen
// Benutzer, Passwort und Host, und Roundcube verbände sich mit dem falschen
// Server oder scheiterte an der Anmeldung.
func TestRenderRoundcubeConfigKodiertDasPasswortInDerDSN(t *testing.T) {
	d := gueltigeRoundcubeConfig(t)
	d.DBPassword = "hat@ein-at-und%prozent"

	out, err := RenderRoundcubeConfig(d)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "hat@ein-at-und%prozent@localhost") {
		t.Errorf("das passwort steht unkodiert in der dsn:\n%s", out)
	}
	if !strings.Contains(out, "hat%40ein-at-und%25prozent@localhost") {
		t.Errorf("das passwort steht nicht korrekt kodiert in der dsn:\n%s", out)
	}
}

// url.UserPassword kodiert auch ' und \ (dieselben zwei Zeichen, die phpstr
// maskiert) — ein Passwort mit einem Anführungszeichen kommt als %27 in der
// DSN an, nie als rohes '. phpstr auf .DSN bleibt trotzdem stehen: Schutz,
// der nicht davon abhängt, dass die DSN-Kodierung nie verändert wird.
func TestRenderRoundcubeConfigDSNKodiertAuchAnfuehrungszeichen(t *testing.T) {
	d := gueltigeRoundcubeConfig(t)
	d.DBPassword = `ein'passwort\mit\backslash`

	out, err := RenderRoundcubeConfig(d)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, `ein'passwort`) || strings.Contains(out, `passwort\mit`) {
		t.Errorf("' oder \\ stehen roh in der datei:\n%s", out)
	}
	if !strings.Contains(out, `ein%27passwort%5Cmit%5Cbackslash`) {
		t.Errorf("das passwort steht nicht wie erwartet kodiert in der dsn:\n%s", out)
	}
}

func TestRenderRoundcubeConfigEnthaeltPorts(t *testing.T) {
	d := gueltigeRoundcubeConfig(t)
	out, err := RenderRoundcubeConfig(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"localhost:993", "localhost:587", "verify_peer"} {
		if !strings.Contains(out, want) {
			t.Errorf("config enthält %q nicht:\n%s", want, out)
		}
	}
}

func TestRenderRoundcubeConfigVerlangtVollstaendigenSchluessel(t *testing.T) {
	d := gueltigeRoundcubeConfig(t)
	d.DESKey = "zu-kurz"
	if _, err := RenderRoundcubeConfig(d); err == nil {
		t.Error("ein zu kurzer des_key wurde angenommen")
	}
}

func TestRenderRoundcubeConfigPrueftDenNamenNocheinmal(t *testing.T) {
	d := gueltigeRoundcubeConfig(t)
	d.DBName = "'; DROP TABLE users; --"
	if _, err := RenderRoundcubeConfig(d); err == nil {
		t.Error("ein ungültiger datenbankname wurde angenommen")
	}
}

func gueltigerWebmailVhost() WebmailVhostData {
	return WebmailVhostData{
		Hostname:   "webmail.example.at",
		CertPath:   "/var/lib/volt/certs/webmail.example.at/fullchain.pem",
		KeyPath:    "/var/lib/volt/certs/webmail.example.at/privkey.pem",
		WebRoot:    "/var/www/_webmail",
		LogDir:     "/var/log",
		SocketPath: "/run/php/webmail.sock",
	}
}

func TestRenderWebmailVhostEnthaeltHostnameUndSperren(t *testing.T) {
	out, err := RenderWebmailVhost(gueltigerWebmailVhost())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"webmail.example.at", "fullchain.pem", "privkey.pem", "_webmail",
		"webmail.sock", "SQL|config|temp|logs|vendor|tests|bin",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("vhost enthält %q nicht:\n%s", want, out)
		}
	}
}

func TestRenderWebmailVhostPrueftHostname(t *testing.T) {
	d := gueltigerWebmailVhost()
	d.Hostname = "nicht gültig"
	if _, err := RenderWebmailVhost(d); err == nil {
		t.Error("ein ungültiger hostname wurde angenommen")
	}
}

func TestRenderWebmailVhostPrueftPfade(t *testing.T) {
	d := gueltigerWebmailVhost()
	d.SocketPath = "/run/php/x; server { listen 1.2.3.4:80; }"
	if _, err := RenderWebmailVhost(d); err == nil {
		t.Error("ein socket-pfad mit ';' wurde angenommen")
	}
}
