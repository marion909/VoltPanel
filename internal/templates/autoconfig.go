package templates

import (
	"bytes"
	"fmt"
	"html"
	"time"

	"github.com/marion909/voltpanel/internal/store"
)

// Autokonfiguration für Mailprogramme: Thunderbird fragt
// autoconfig.<domain>, Outlook autodiscover.<domain> — beide über einen
// eigenen Vhost, den internal/core/mail.go anlegt. Der Vhost selbst liefert
// nur zwei statische Dateien aus (opMailAutoconfig in internal/agent schreibt
// sie); hier entsteht ihr Inhalt.
//
// xmlesc ist zu phpstr, was XML zu PHP ist: text/template escaped nichts von
// selbst, und die einzigen freien Werte hier (Domain, Host) landen als
// Text zwischen zwei XML-Tags. html.EscapeString maskiert genau die fünf
// Zeichen, die XML als Entität kennt (< > & ' "); mehr braucht ein
// Textknoten nicht, und mit HTML hat das nur die Zufälligkeit gemeinsam,
// dass die Standardbibliothek dieselbe Funktion für beides anbietet.
func xmlesc(s string) string {
	return html.EscapeString(s)
}

// AutoconfigData ist das Eingabemodell der Mozilla- und
// Microsoft-Konfiguration einer Maildomäne.
type AutoconfigData struct {
	Domain   string
	Host     string
	IMAPPort int
	SMTPPort int
}

func (d AutoconfigData) validate() error {
	if !store.ValidMailDomain(d.Domain) {
		return fmt.Errorf("%q ist keine gültige maildomäne", d.Domain)
	}
	if !store.ValidDomain(d.Host) {
		return fmt.Errorf("%q ist kein gültiger servername", d.Host)
	}
	if d.IMAPPort < 1 || d.IMAPPort > 65535 {
		return fmt.Errorf("imap-port %d liegt außerhalb 1–65535", d.IMAPPort)
	}
	if d.SMTPPort < 1 || d.SMTPPort > 65535 {
		return fmt.Errorf("smtp-port %d liegt außerhalb 1–65535", d.SMTPPort)
	}
	return nil
}

// RenderMozillaAutoconfig erzeugt config-v1.1.xml, wie Thunderbird sie unter
// autoconfig.<domain>/mail/config-v1.1.xml erwartet.
func RenderMozillaAutoconfig(d AutoconfigData) (string, error) {
	if err := d.validate(); err != nil {
		return "", err
	}
	var b bytes.Buffer
	if err := tmpl.ExecuteTemplate(&b, "mail-autoconfig.xml.tmpl", d); err != nil {
		return "", err
	}
	return b.String(), nil
}

// RenderMicrosoftAutodiscover erzeugt autodiscover.xml, wie Outlook sie unter
// autodiscover.<domain>/autodiscover/autodiscover.xml erwartet.
func RenderMicrosoftAutodiscover(d AutoconfigData) (string, error) {
	if err := d.validate(); err != nil {
		return "", err
	}
	var b bytes.Buffer
	if err := tmpl.ExecuteTemplate(&b, "mail-autodiscover.xml.tmpl", d); err != nil {
		return "", err
	}
	return b.String(), nil
}

// AutoconfigVhostData ist das Eingabemodell des Vhosts, der beide Dateien
// ausliefert.
type AutoconfigVhostData struct {
	AutoconfigHost   string
	AutodiscoverHost string
	CertPath         string
	KeyPath          string
	MozillaPath      string
	MicrosoftPath    string
	GeneratedAt      string
}

func (d *AutoconfigVhostData) validate() error {
	if !store.ValidDomain(d.AutoconfigHost) {
		return fmt.Errorf("domain %q ist ungültig", d.AutoconfigHost)
	}
	if !store.ValidDomain(d.AutodiscoverHost) {
		return fmt.Errorf("domain %q ist ungültig", d.AutodiscoverHost)
	}
	for name, p := range map[string]string{
		"cert_path": d.CertPath, "key_path": d.KeyPath,
		"mozilla_path": d.MozillaPath, "microsoft_path": d.MicrosoftPath,
	} {
		if err := checkPath(name, p); err != nil {
			return err
		}
	}
	return nil
}

// RenderAutoconfigVhost erzeugt den Vhost für autoconfig.<domain> und
// autodiscover.<domain> — eine gemeinsame Config, ein gemeinsames Zertifikat
// mit beiden Namen als SAN.
func RenderAutoconfigVhost(d AutoconfigVhostData) (string, error) {
	if err := d.validate(); err != nil {
		return "", err
	}
	d.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	var b bytes.Buffer
	if err := tmpl.ExecuteTemplate(&b, "mail-autoconfig.conf.tmpl", d); err != nil {
		return "", err
	}
	return b.String(), nil
}
