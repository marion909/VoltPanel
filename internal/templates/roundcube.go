package templates

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/url"
	"time"

	"github.com/marion909/voltpanel/internal/store"
)

// config.inc.php für die eine, server-weite Roundcube-Installation
// (internal/core/webmail.go).
//
// Derselbe Grund wie bei wp-config.php: text/template escaped nichts von
// selbst, und config.inc.php ist ausführbarer PHP-Code. phpstr ist auch hier
// die einzige Stelle, an der jeder eingesetzte Wert vorbeikommt.
//
// Eine Besonderheit gegenüber wp-config.php: das Datenbankpasswort landet
// nicht nur als PHP-Zeichenkette, sondern zuerst als Teil einer DSN-URL
// (mysql://benutzer:passwort@host/name). Ein Passwort mit "@" oder "%" —
// beides erlaubte Zeichen bei authn.GeneratePassword — verschöbe sonst die
// Grenze zwischen Benutzername, Passwort und Host innerhalb der URL selbst,
// unabhängig von der PHP-Maskierung danach. url.UserPassword übernimmt genau
// diese Kodierung; phpstr kommt erst danach, für die PHP-Zeichenkette drumherum.

// RoundcubeConfigData ist das Eingabemodell von config.inc.php.
type RoundcubeConfigData struct {
	DBUser     string
	DBPassword string
	DBName     string
	IMAPPort   int
	SMTPPort   int
	// DESKey verschlüsselt das IMAP-Passwort in der Sitzung. Muss bei
	// Roundcubes Standard-Cipher genau 24 Zeichen lang sein.
	DESKey string
}

// NewRoundcubeDESKey erzeugt den Sitzungsschlüssel.
//
// 18 zufällige Bytes ergeben, mit Standard-Base64 kodiert, genau 24 Zeichen
// ohne Füllzeichen (18 ist durch 3 teilbar) — Roundcubes einzige Vorgabe an
// diesen Wert.
func NewRoundcubeDESKey() (string, error) {
	buf := make([]byte, 18)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("schlüssel erzeugen: %w", err)
	}
	return base64.StdEncoding.EncodeToString(buf), nil
}

func (d RoundcubeConfigData) validate() error {
	switch {
	case !store.ValidDBUser(d.DBUser):
		return fmt.Errorf("%q ist kein gültiger datenbankbenutzer", d.DBUser)
	case !store.ValidDBName(d.DBName):
		return fmt.Errorf("%q ist kein gültiger datenbankname", d.DBName)
	case d.DBPassword == "":
		return fmt.Errorf("kein datenbankpasswort übergeben")
	case d.IMAPPort < 1 || d.IMAPPort > 65535:
		return fmt.Errorf("imap-port %d liegt außerhalb 1–65535", d.IMAPPort)
	case d.SMTPPort < 1 || d.SMTPPort > 65535:
		return fmt.Errorf("smtp-port %d liegt außerhalb 1–65535", d.SMTPPort)
	case len(d.DESKey) != 24:
		return fmt.Errorf("des_key muss 24 zeichen lang sein, ist %d — NewRoundcubeDESKey vergessen?",
			len(d.DESKey))
	}
	return nil
}

// RenderRoundcubeConfig erzeugt config.inc.php.
func RenderRoundcubeConfig(d RoundcubeConfigData) (string, error) {
	if err := d.validate(); err != nil {
		return "", err
	}
	dsn := "mysql://" + url.UserPassword(d.DBUser, d.DBPassword).String() + "@localhost/" + d.DBName

	payload := struct {
		RoundcubeConfigData
		DSN string
	}{d, dsn}

	var b bytes.Buffer
	if err := tmpl.ExecuteTemplate(&b, "roundcube-config.php.tmpl", payload); err != nil {
		return "", err
	}
	return b.String(), nil
}

// WebmailVhostData ist das Eingabemodell des Vhosts, der Webmail ausliefert.
//
// Kein SiteData: Webmail gehört keiner Site, und der Vhost braucht eigene
// Sperren (SQL/config/temp/logs/vendor/tests/bin), die site.conf.tmpl nicht
// kennt und für jede gewöhnliche PHP-Site auch falsch wären.
type WebmailVhostData struct {
	Hostname       string
	CertPath       string
	KeyPath        string
	WebRoot        string
	LogDir         string
	SocketPath     string
	MaxBodySize    string
	FastCGITimeout int
	GeneratedAt    string
}

func (d *WebmailVhostData) validate() error {
	if !store.ValidDomain(d.Hostname) {
		return fmt.Errorf("domain %q ist ungültig", d.Hostname)
	}
	for name, p := range map[string]string{
		"cert_path": d.CertPath, "key_path": d.KeyPath,
		"web_root": d.WebRoot, "log_dir": d.LogDir, "socket_path": d.SocketPath,
	} {
		if err := checkPath(name, p); err != nil {
			return err
		}
	}
	return nil
}

func (d *WebmailVhostData) applyDefaults() {
	d.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	if d.MaxBodySize == "" {
		d.MaxBodySize = "64M"
	}
	if d.FastCGITimeout <= 0 {
		d.FastCGITimeout = 60
	}
}

// RenderWebmailVhost erzeugt den Vhost für die Webmail-Installation.
func RenderWebmailVhost(d WebmailVhostData) (string, error) {
	if err := d.validate(); err != nil {
		return "", err
	}
	d.applyDefaults()
	var b bytes.Buffer
	if err := tmpl.ExecuteTemplate(&b, "webmail.conf.tmpl", d); err != nil {
		return "", err
	}
	return b.String(), nil
}
