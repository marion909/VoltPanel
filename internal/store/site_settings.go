package store

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
)

// SiteSettings sind die Vhost-Einstellungen, die über die Grunddaten einer
// Site hinausgehen.
//
// Sie werden als Ganzes gelesen und geschrieben und nie einzeln abgefragt —
// deshalb ein JSON-Feld statt eigener Spalten und Nebentabellen. Die Prüfung
// findet zweimal statt: hier beim Speichern und noch einmal im
// templates-Paket beim Rendern, weil dort nichts escaped wird.
type SiteSettings struct {
	Redirects []Redirect `json:"redirects,omitempty"`

	// DenyIPs sperrt einzelne Adressen oder Netze aus. AllowIPs kehrt die
	// Logik um: ist die Liste nicht leer, kommt sonst niemand mehr durch.
	DenyIPs  []string `json:"deny_ips,omitempty"`
	AllowIPs []string `json:"allow_ips,omitempty"`

	// BasicAuth schützt die ganze Site mit einer Passwortabfrage.
	BasicAuth *BasicAuth `json:"basic_auth,omitempty"`

	// ExtraLines sind einzelne Nginx-Direktiven aus dem Rewrite-Editor.
	// Bewusst keine Blöcke — siehe die Prüfung im templates-Paket.
	ExtraLines []string `json:"extra_lines,omitempty"`

	MaxBodySize    string `json:"max_body_size,omitempty"`
	FastCGITimeout int    `json:"fastcgi_timeout,omitempty"`
}

type Redirect struct {
	From string `json:"from"`
	To   string `json:"to"`
	Code int    `json:"code"`
}

// BasicAuth hält die Anmeldedaten. Das Klartextpasswort wird nie gespeichert:
// der Agent legt eine htpasswd-Datei mit bcrypt-Hashes an, hier steht nur,
// dass und mit welchem Realm der Schutz aktiv ist.
type BasicAuth struct {
	Enabled bool     `json:"enabled"`
	Realm   string   `json:"realm,omitempty"`
	Users   []string `json:"users,omitempty"` // nur die Namen, für die Anzeige
}

var (
	reRedirectPath = regexp.MustCompile(`^/[A-Za-z0-9._~!$&'()*+,;=:@/%-]*$`)
	reSizeValue    = regexp.MustCompile(`^\d{1,6}[KMG]?$`)
	reAuthUser     = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,64}$`)
	reRealm        = regexp.MustCompile(`^[a-zA-Z0-9 äöüÄÖÜß._-]{1,64}$`)
)

// Validate prüft die Einstellungen, bevor sie gespeichert werden.
//
// Meldet alle Fehler auf einmal: wer fünf Weiterleitungen einträgt, soll nicht
// fünfmal speichern müssen, um alle Tippfehler zu finden.
func (s *SiteSettings) Validate() error {
	var problems []string

	for i, r := range s.Redirects {
		if !reRedirectPath.MatchString(r.From) {
			problems = append(problems,
				fmt.Sprintf("weiterleitung %d: quelle %q muss ein pfad ab / sein", i+1, r.From))
		}
		switch r.Code {
		case 301, 302, 307, 308:
		default:
			problems = append(problems,
				fmt.Sprintf("weiterleitung %d: code %d ist nicht erlaubt (301, 302, 307, 308)", i+1, r.Code))
		}
		if err := checkRedirectTarget(r.To); err != nil {
			problems = append(problems, fmt.Sprintf("weiterleitung %d: %v", i+1, err))
		}
	}

	for _, list := range []struct {
		name    string
		entries []string
	}{{"sperrliste", s.DenyIPs}, {"freigabeliste", s.AllowIPs}} {
		for _, entry := range list.entries {
			if err := checkIPEntry(entry); err != nil {
				problems = append(problems, fmt.Sprintf("%s: %v", list.name, err))
			}
		}
	}

	if s.BasicAuth != nil {
		if s.BasicAuth.Realm != "" && !reRealm.MatchString(s.BasicAuth.Realm) {
			problems = append(problems, fmt.Sprintf("bereichsname %q enthält unerlaubte zeichen", s.BasicAuth.Realm))
		}
		for _, user := range s.BasicAuth.Users {
			if !reAuthUser.MatchString(user) {
				problems = append(problems, fmt.Sprintf("benutzername %q enthält unerlaubte zeichen", user))
			}
		}
		if s.BasicAuth.Enabled && len(s.BasicAuth.Users) == 0 {
			problems = append(problems, "passwortschutz ist aktiv, aber es gibt keinen benutzer")
		}
	}

	// Der Rewrite-Editor darf nur einzelne Direktiven aufnehmen. Klammern zu
	// zählen genügt nicht: "} server { listen 8080;" ist balanciert und bricht
	// trotzdem aus dem Server-Block in den http-Kontext aus.
	for i, line := range s.ExtraLines {
		if err := checkDirectiveLine(line); err != nil {
			problems = append(problems, fmt.Sprintf("zusatzzeile %d: %v", i+1, err))
		}
	}

	if s.MaxBodySize != "" && !reSizeValue.MatchString(s.MaxBodySize) {
		problems = append(problems,
			fmt.Sprintf("maximale anfragegröße %q ist ungültig (z.B. 64M)", s.MaxBodySize))
	}
	if s.FastCGITimeout < 0 || s.FastCGITimeout > 3600 {
		problems = append(problems,
			fmt.Sprintf("php-zeitlimit %d liegt außerhalb von 0–3600 sekunden", s.FastCGITimeout))
	}

	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	return nil
}

func checkRedirectTarget(to string) error {
	if strings.HasPrefix(to, "/") {
		if !reRedirectPath.MatchString(to) {
			return fmt.Errorf("ziel %q ist kein gültiger pfad", to)
		}
		return nil
	}
	u, err := url.Parse(to)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("ziel %q muss ein pfad oder eine http(s)-url sein", to)
	}
	if strings.ContainsAny(to, " \t\n\r;{}\"'") {
		return fmt.Errorf("ziel %q enthält unerlaubte zeichen", to)
	}
	return nil
}

func checkIPEntry(entry string) error {
	if strings.Contains(entry, "/") {
		if _, _, err := net.ParseCIDR(entry); err != nil {
			return fmt.Errorf("%q ist kein gültiges netz", entry)
		}
		return nil
	}
	if net.ParseIP(entry) == nil {
		return fmt.Errorf("%q ist keine gültige ip-adresse", entry)
	}
	return nil
}

func checkDirectiveLine(line string) error {
	trimmed := strings.TrimSpace(line)
	switch {
	case trimmed == "":
		return fmt.Errorf("ist leer")
	case strings.ContainsAny(trimmed, "\n\r\x00"):
		return fmt.Errorf("darf nur eine zeile sein")
	case strings.ContainsAny(trimmed, "{}"):
		return fmt.Errorf("darf keine geschweiften klammern enthalten — erlaubt sind einzelne direktiven")
	case strings.HasPrefix(trimmed, "#"):
		return nil
	case !strings.HasSuffix(trimmed, ";"):
		return fmt.Errorf("muss mit ';' enden")
	}
	if first, _, _ := strings.Cut(trimmed, " "); strings.EqualFold(first, "include") {
		return fmt.Errorf("include ist nicht erlaubt — es würde beliebige dateien einbinden")
	}
	return nil
}

// encodeSettings serialisiert für die Datenbank.
func encodeSettings(s *SiteSettings) string {
	if s == nil {
		return "{}"
	}
	b, err := json.Marshal(s)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// decodeSettings liest die Spalte. Unlesbares JSON ergibt leere Einstellungen
// statt eines Fehlers: eine Site soll nicht unerreichbar werden, weil ihre
// Zusatzeinstellungen beschädigt sind.
func decodeSettings(raw string) SiteSettings {
	var s SiteSettings
	if strings.TrimSpace(raw) == "" {
		return s
	}
	_ = json.Unmarshal([]byte(raw), &s)
	return s
}
