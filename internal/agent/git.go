package agent

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Git-Adressen und Referenzen vom Kunden.
//
// Eine Repository-Adresse ist kein bloßer Text: git legt sie selbst noch einmal
// aus, und dabei sind mehrere Formen möglich, die etwas anderes tun als
// "irgendwo etwas herunterladen".
//
//	ext::sh -c whoami            git führt das Kommando aus. So ist der
//	                             ext-Transport gemeint; er ist eine Funktion,
//	                             kein Fehler.
//	--upload-pack=/bin/sh        sieht wie eine Adresse aus, ist aber ein
//	                             Argument. Ohne "--" davor nimmt git es als
//	                             Option und ruft das Programm auf.
//	ssh://-oProxyCommand=…/x     derselbe Trick eine Ebene tiefer: der Hostname
//	                             beginnt mit einem Bindestrich und wird von ssh
//	                             als Option gelesen (CVE-2017-1000117).
//	file:///etc                  kein Angriff, aber ein Weg, jedes Verzeichnis
//	                             des Servers in ein Kundenverzeichnis zu kopieren.
//
// Deshalb wird hier nicht gefiltert, sondern zerlegt und wieder aufgebaut: was
// nicht in eine der drei erlaubten Formen passt, kommt gar nicht erst zurück.

var (
	// reGitHost ist ein Hostname, kein Optionsanfang. Der führende Buchstabe
	// oder die Ziffer ist die Zeile, die CVE-2017-1000117 schließt.
	reGitHost = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)

	// reGitPath: was auf einem Hoster als Pfad vorkommt, und nichts weiter.
	reGitPath = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/~-]{0,200}$`)

	// reGitUser ist der Benutzer vor dem @. Fast immer "git".
	reGitUser = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

	// reGitRef ist ein Branch- oder Tagname.
	reGitRef = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,99}$`)
)

// NormalizeGitURL prüft eine Repository-Adresse und gibt sie in kanonischer
// Form zurück.
//
// Erlaubt sind genau drei Formen:
//
//	https://host[:port]/pfad
//	ssh://[benutzer@]host[:port]/pfad
//	benutzer@host:pfad          (die Kurzform, die jeder Hoster anzeigt)
//
// Alles andere wird abgelehnt — auch Formen, die harmlos aussehen. Eine
// Whitelist, die nur die drei Fälle kennt, die wirklich vorkommen, ist hier
// mehr wert als eine Liste dessen, was jemandem an Angriffen eingefallen ist.
func NormalizeGitURL(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	switch {
	case s == "":
		return "", fmt.Errorf("%w: keine repository-adresse", errBadInput)
	case len(s) > 300:
		return "", fmt.Errorf("%w: die adresse ist zu lang", errBadInput)
	case strings.ContainsAny(s, " \t\n\r\x00'\";|&$`<>()"):
		return "", fmt.Errorf("%w: die adresse enthält zeichen, die dort nicht vorkommen",
			errBadInput)
	case strings.HasPrefix(s, "-"):
		// git läse das als Option, nicht als Adresse.
		//
		// Diese Zeile ist die zweite Schranke, nicht die erste: eine solche
		// Eingabe scheitert ohnehin weiter unten, weil sie zu keiner der drei
		// erlaubten Formen passt. Sie steht hier, weil die Meldung dann sagt,
		// woran es wirklich liegt — und weil eine spätere Lockerung der Formen
		// sie nicht mitnehmen soll.
		return "", fmt.Errorf("%w: eine adresse beginnt nicht mit einem bindestrich", errBadInput)
	}

	if strings.Contains(s, "://") {
		return normalizeGitScheme(s)
	}
	return normalizeGitSCP(s)
}

// normalizeGitScheme behandelt https:// und ssh://.
func normalizeGitScheme(s string) (string, error) {
	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("%w: die adresse ist nicht lesbar", errBadInput)
	}
	switch u.Scheme {
	case "https", "ssh":
	default:
		// Hier landen ext::, file://, git://, http:// und alles Übrige.
		// git:// wäre unverschlüsselt und nicht authentifiziert, http:// ebenso;
		// file:// wäre ein Weg an jedes Verzeichnis des Servers.
		return "", fmt.Errorf("%w: %q wird nicht unterstützt — https:// oder ssh://",
			errBadInput, u.Scheme)
	}
	if u.User != nil {
		if _, hatPasswort := u.User.Password(); hatPasswort {
			// Ein Passwort in der Adresse landete in der Datenbank, im
			// Audit-Log und in jeder Fehlermeldung. Für https gibt es den
			// Token, für ssh den Deploy-Key.
			return "", fmt.Errorf("%w: kein passwort in der adresse — dafür gibt es "+
				"den deploy-key", errBadInput)
		}
		if !reGitUser.MatchString(u.User.Username()) {
			return "", fmt.Errorf("%w: benutzername %q", errBadInput, u.User.Username())
		}
	}

	host, port := u.Hostname(), u.Port()
	if !reGitHost.MatchString(host) {
		return "", fmt.Errorf("%w: hostname %q", errBadInput, host)
	}
	if port != "" && !regexp.MustCompile(`^[0-9]{1,5}$`).MatchString(port) {
		return "", fmt.Errorf("%w: port %q", errBadInput, port)
	}
	pfad, err := cleanGitPath(u.Path)
	if err != nil {
		return "", err
	}

	// Neu zusammengesetzt, nicht durchgereicht: was hier herauskommt, besteht
	// nur aus geprüften Teilen.
	out := u.Scheme + "://"
	if u.User != nil && u.User.Username() != "" {
		out += u.User.Username() + "@"
	}
	out += host
	if port != "" {
		out += ":" + port
	}
	return out + "/" + pfad, nil
}

// normalizeGitSCP behandelt die Kurzform git@host:pfad.
func normalizeGitSCP(s string) (string, error) {
	user, rest, ok := strings.Cut(s, "@")
	if !ok {
		return "", fmt.Errorf("%w: die adresse braucht ein schema (https:// oder ssh://) "+
			"oder die form benutzer@host:pfad", errBadInput)
	}
	host, pfad, ok := strings.Cut(rest, ":")
	if !ok {
		return "", fmt.Errorf("%w: nach dem host fehlt der doppelpunkt mit dem pfad", errBadInput)
	}
	if !reGitUser.MatchString(user) {
		return "", fmt.Errorf("%w: benutzername %q", errBadInput, user)
	}
	if !reGitHost.MatchString(host) {
		return "", fmt.Errorf("%w: hostname %q", errBadInput, host)
	}
	clean, err := cleanGitPath(pfad)
	if err != nil {
		return "", err
	}
	return user + "@" + host + ":" + clean, nil
}

// cleanGitPath prüft den Pfadteil und nimmt führende Schrägstriche weg.
func cleanGitPath(p string) (string, error) {
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		return "", fmt.Errorf("%w: in der adresse fehlt der pfad zum repository", errBadInput)
	}
	if strings.Contains(p, "..") {
		return "", fmt.Errorf("%w: der pfad darf kein .. enthalten", errBadInput)
	}
	if !reGitPath.MatchString(p) {
		return "", fmt.Errorf("%w: pfad %q", errBadInput, p)
	}
	return p, nil
}

// ValidGitRef prüft einen Branch- oder Tagnamen.
//
// Der Name wird ein Argument von `git checkout`. Ein führender Bindestrich wäre
// dort eine Option; die übrigen Regeln sind die von git selbst
// (git-check-ref-format), soweit sie hier zählen.
func ValidGitRef(ref string) bool {
	switch {
	case !reGitRef.MatchString(ref):
		return false
	case strings.Contains(ref, ".."), strings.Contains(ref, "@{"):
		return false
	case strings.HasSuffix(ref, ".lock"), strings.HasSuffix(ref, "/"):
		return false
	case strings.Contains(ref, "//"):
		return false
	}
	return true
}
