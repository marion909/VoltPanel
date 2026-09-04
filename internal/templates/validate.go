package templates

import (
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	rePoolName    = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{1,63}$`)
	rePHPVersion  = regexp.MustCompile(`^[578]\.[0-9]$`)
	reSystemUser  = regexp.MustCompile(`^[a-z_][a-z0-9_-]{1,31}$`)
	reSize        = regexp.MustCompile(`^\d{1,6}[KMG]?$`)
	reRedirectSrc = regexp.MustCompile(`^/[A-Za-z0-9._~!$&'()*+,;=:@/%-]*$`)
)

// checkPath lässt nur absolute, einzeilige Pfade ohne .. durch. Ein Pfad mit
// Zeilenumbruch könnte in einer Nginx-Config eine zusätzliche Direktive öffnen.
func checkPath(name, p string) error {
	switch {
	case p == "":
		return fmt.Errorf("%s fehlt", name)
	case !filepath.IsAbs(p):
		return fmt.Errorf("%s %q muss absolut sein", name, p)
	case strings.ContainsAny(p, "\n\r\x00;{}\"'"):
		return fmt.Errorf("%s %q enthält unerlaubte zeichen", name, p)
	case strings.Contains(p, ".."):
		return fmt.Errorf("%s %q darf kein .. enthalten", name, p)
	}
	return nil
}

// checkIP akzeptiert eine einzelne Adresse oder ein CIDR-Netz.
func checkIP(s string) error {
	if strings.Contains(s, "/") {
		if _, _, err := net.ParseCIDR(s); err != nil {
			return fmt.Errorf("ip-bereich %q ist ungültig", s)
		}
		return nil
	}
	if net.ParseIP(s) == nil {
		return fmt.Errorf("ip-adresse %q ist ungültig", s)
	}
	return nil
}

func checkRedirect(r Redirect) error {
	if !reRedirectSrc.MatchString(r.From) {
		return fmt.Errorf("weiterleitung von %q muss ein pfad ab / sein", r.From)
	}
	switch r.Code {
	case 301, 302, 307, 308:
	default:
		return fmt.Errorf("weiterleitungscode %d ist nicht erlaubt (301, 302, 307, 308)", r.Code)
	}

	// Ziel darf ein absoluter Pfad oder eine http(s)-URL sein — sonst ließe sich
	// hier eine beliebige Nginx-Direktive unterbringen.
	if strings.HasPrefix(r.To, "/") {
		if !reRedirectSrc.MatchString(r.To) {
			return fmt.Errorf("weiterleitungsziel %q ist kein gültiger pfad", r.To)
		}
		return nil
	}
	u, err := url.Parse(r.To)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("weiterleitungsziel %q muss ein pfad oder eine http(s)-url sein", r.To)
	}
	if strings.ContainsAny(r.To, " \t\n\r;{}\"'") {
		return fmt.Errorf("weiterleitungsziel %q enthält unerlaubte zeichen", r.To)
	}
	return nil
}

// checkINIValue lässt einen Wert durch, der roh in eine php.ini-artige
// Konfigurationsdatei geschrieben wird (PoolData.ExtraINI, DisableFunctions).
// Ein Zeilenumbruch gefolgt von "[name]" würde einen komplett neuen,
// unabhängigen PHP-FPM-Pool in derselben Datei eröffnen — z. B. mit
// user = root oder einem beliebigen Socket-Pfad. Deshalb sind hier, anders
// als bei checkDirective (Nginx), keine eigenen Abschnittsklammern erlaubt.
func checkINIValue(name, value string) error {
	if strings.ContainsAny(value, "\n\r\x00") {
		return fmt.Errorf("%s enthält einen zeilenumbruch oder ein nullbyte", name)
	}
	if strings.ContainsAny(value, "[]") {
		return fmt.Errorf("%s darf keinen neuen abschnitt („[...]“) eröffnen", name)
	}
	return nil
}

// checkDirective lässt genau eine Nginx-Direktive durch: eine Zeile, keine
// geschweiften Klammern, abgeschlossen mit ";".
func checkDirective(line string) error {
	trimmed := strings.TrimSpace(line)
	switch {
	case trimmed == "":
		return fmt.Errorf("leere zusatzzeile")
	case strings.ContainsAny(trimmed, "\n\r\x00"):
		return fmt.Errorf("zusatzzeile %q darf nur eine zeile sein", line)
	case strings.ContainsAny(trimmed, "{}"):
		return fmt.Errorf("zusatzzeile %q darf keine geschweiften klammern enthalten — "+
			"erlaubt sind einzelne direktiven", line)
	case strings.HasPrefix(trimmed, "#"):
		return nil
	case !strings.HasSuffix(trimmed, ";"):
		return fmt.Errorf("zusatzzeile %q muss mit ';' enden", line)
	}
	// Ein "include" würde beliebige Dateien in die Config ziehen und damit die
	// gesamte Prüfung hier aushebeln.
	if first, _, _ := strings.Cut(trimmed, " "); strings.EqualFold(first, "include") {
		return fmt.Errorf("include ist in zusatzzeilen nicht erlaubt")
	}
	return nil
}
