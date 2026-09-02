// Package gitspec prüft, was von einem Kunden kommt und später ein Argument
// von git oder ein Buildschritt wird.
//
// Eigenes Paket, weil zwei Seiten dieselbe Prüfung brauchen: der Store beim
// Speichern und der Agent unmittelbar vor dem Aufruf. Dieselbe, nicht eine
// ähnliche — eine zweite, nachgebaute Prüfung ist die Stelle, an der beide
// auseinanderlaufen, und dann lässt die eine durch, was die andere verbietet.
//
// Unter beiden, weil store und agent einander nicht importieren können, ohne
// einen Zyklus zu bilden.
package gitspec

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// ErrInvalid trägt jede Ablehnung aus diesem Paket. Der Aufrufer entscheidet
// daran, ob daraus ein 400 wird oder ein 500.
var ErrInvalid = errors.New("ungültige eingabe")

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

// NormalizeURL prüft eine Repository-Adresse und gibt sie in kanonischer
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
func NormalizeURL(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	switch {
	case s == "":
		return "", fmt.Errorf("%w: keine repository-adresse", ErrInvalid)
	case len(s) > 300:
		return "", fmt.Errorf("%w: die adresse ist zu lang", ErrInvalid)
	case strings.ContainsAny(s, " \t\n\r\x00'\";|&$`<>()"):
		return "", fmt.Errorf("%w: die adresse enthält zeichen, die dort nicht vorkommen",
			ErrInvalid)
	case strings.HasPrefix(s, "-"):
		// git läse das als Option, nicht als Adresse.
		//
		// Diese Zeile ist die zweite Schranke, nicht die erste: eine solche
		// Eingabe scheitert ohnehin weiter unten, weil sie zu keiner der drei
		// erlaubten Formen passt. Sie steht hier, weil die Meldung dann sagt,
		// woran es wirklich liegt — und weil eine spätere Lockerung der Formen
		// sie nicht mitnehmen soll.
		return "", fmt.Errorf("%w: eine adresse beginnt nicht mit einem bindestrich", ErrInvalid)
	}

	if strings.Contains(s, "://") {
		return normalizeScheme(s)
	}
	return normalizeSCP(s)
}

// normalizeScheme behandelt https:// und ssh://.
func normalizeScheme(s string) (string, error) {
	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("%w: die adresse ist nicht lesbar", ErrInvalid)
	}
	switch u.Scheme {
	case "https", "ssh":
	default:
		// Hier landen ext::, file://, git://, http:// und alles Übrige.
		// git:// wäre unverschlüsselt und nicht authentifiziert, http:// ebenso;
		// file:// wäre ein Weg an jedes Verzeichnis des Servers.
		return "", fmt.Errorf("%w: %q wird nicht unterstützt — https:// oder ssh://",
			ErrInvalid, u.Scheme)
	}
	if u.User != nil {
		if _, hatPasswort := u.User.Password(); hatPasswort {
			// Ein Passwort in der Adresse landete in der Datenbank, im
			// Audit-Log und in jeder Fehlermeldung. Für https gibt es den
			// Token, für ssh den Deploy-Key.
			return "", fmt.Errorf("%w: kein passwort in der adresse — dafür gibt es "+
				"den deploy-key", ErrInvalid)
		}
		if !reGitUser.MatchString(u.User.Username()) {
			return "", fmt.Errorf("%w: benutzername %q", ErrInvalid, u.User.Username())
		}
	}

	host, port := u.Hostname(), u.Port()
	if err := checkHost(host); err != nil {
		return "", err
	}
	if port != "" && !regexp.MustCompile(`^[0-9]{1,5}$`).MatchString(port) {
		return "", fmt.Errorf("%w: port %q", ErrInvalid, port)
	}
	pfad, err := cleanPath(u.Path)
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

// normalizeSCP behandelt die Kurzform git@host:pfad.
func normalizeSCP(s string) (string, error) {
	user, rest, ok := strings.Cut(s, "@")
	if !ok {
		return "", fmt.Errorf("%w: die adresse braucht ein schema (https:// oder ssh://) "+
			"oder die form benutzer@host:pfad", ErrInvalid)
	}
	host, pfad, ok := strings.Cut(rest, ":")
	if !ok {
		return "", fmt.Errorf("%w: nach dem host fehlt der doppelpunkt mit dem pfad", ErrInvalid)
	}
	if !reGitUser.MatchString(user) {
		return "", fmt.Errorf("%w: benutzername %q", ErrInvalid, user)
	}
	if err := checkHost(host); err != nil {
		return "", err
	}
	clean, err := cleanPath(pfad)
	if err != nil {
		return "", err
	}
	return user + "@" + host + ":" + clean, nil
}

// checkHost prüft den Hostnamen einer Repository-Adresse.
//
// Zwei Dinge. Erstens die Form: ein Hostname beginnt mit einem Buchstaben oder
// einer Ziffer, nie mit einem Bindestrich — das ist die Zeile, die
// CVE-2017-1000117 schließt.
//
// Zweitens das Ziel, soweit es hier schon feststeht. Steht dort eine
// IP-Adresse, wird sie angesehen: eine Adresse im Link-Local-Bereich ist der
// Metadatendienst der Cloud (169.254.169.254), eine Loopback-Adresse ist der
// Server selbst. Ein `git clone` dorthin ist ein Aufruf von innen, den ein
// Kunde von außen ausgelöst hat — und die Antwort landet im Protokoll des
// Deploys, wo er sie lesen kann.
//
// Private Netze bleiben erlaubt. Ein selbst betriebenes Gitea auf 10.0.0.5 ist
// der Normalfall in genau der Art von Umgebung, für die dieses Panel gedacht
// ist; sie zu sperren hieße, ein echtes Bedürfnis für einen geringen Gewinn
// aufzugeben.
//
// Das ist keine vollständige SSRF-Abwehr: ein Name, der auf 169.254.169.254
// zeigt, geht weiterhin durch, und gegen DNS-Rebinding hilft ohnehin nur ein
// Proxy, der die aufgelöste Adresse prüft. Es schließt den bequemen Weg, nicht
// jeden.
//
// IPv6-Literale kommen hier gar nicht erst an: der Doppelpunkt steht nicht im
// Zeichenvorrat eines Hostnamens, "https://[::1]/x.git" scheitert also schon
// eine Zeile höher. Das ist eine Einschränkung — eine Adresse in eckigen
// Klammern lässt sich nicht als Repository angeben —, aber keine, die jemandem
// im Weg steht: Hoster haben Namen.
func checkHost(host string) error {
	if !reGitHost.MatchString(host) {
		return fmt.Errorf("%w: hostname %q", ErrInvalid, host)
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		// Kein Literal, sondern ein Name — mehr lässt sich hier nicht sagen.
		return nil
	}
	switch {
	case addr.IsLoopback():
		return fmt.Errorf("%w: %s zeigt auf diesen server selbst", ErrInvalid, addr)
	case addr.IsUnspecified():
		return fmt.Errorf("%w: %s ist keine adresse, die man aufrufen kann", ErrInvalid, addr)
	case addr.IsLinkLocalUnicast(), addr.IsLinkLocalMulticast():
		return fmt.Errorf("%w: %s ist eine link-local-adresse — dort antwortet auf "+
			"vielen servern der metadaten-dienst der cloud, nicht ein repository",
			ErrInvalid, addr)
	case addr.IsMulticast(), addr.IsInterfaceLocalMulticast():
		return fmt.Errorf("%w: %s ist eine multicast-adresse", ErrInvalid, addr)
	}
	return nil
}

// Endpoint zerlegt eine bereits geprüfte Adresse in ihre Bestandteile.
//
// Eingabe ist, was NormalizeURL zurückgibt — nicht, was ein Kunde eingetippt
// hat. Deshalb steht hier keine Prüfung mehr: die drei Formen sind bekannt,
// und was ihnen nicht entspricht, ist nie durch NormalizeURL gegangen.
//
// Gebraucht wird das eine Ebene höher, unmittelbar vor dem Aufruf von git: der
// Hostname muss dort aufgelöst und die Adresse dahinter geprüft werden, und
// dafür muss man wissen, welcher Teil der Adresse der Hostname ist.
func Endpoint(canonical string) (scheme, host, port string, err error) {
	rest, mitSchema := "", true
	switch {
	case strings.HasPrefix(canonical, "https://"):
		scheme, rest = "https", strings.TrimPrefix(canonical, "https://")
	case strings.HasPrefix(canonical, "ssh://"):
		scheme, rest = "ssh", strings.TrimPrefix(canonical, "ssh://")
	default:
		// Die Kurzform benutzer@host:pfad. Sie spricht immer ssh, und sie
		// kennt keinen Port — der Doppelpunkt trennt dort den Pfad ab.
		scheme, rest, mitSchema = "ssh", canonical, false
	}
	if _, hinter, ok := strings.Cut(rest, "@"); ok {
		rest = hinter
	}

	if mitSchema {
		// Beim Schema trennt der erste Schrägstrich den Pfad ab; was davor
		// steht, ist host[:port].
		if i := strings.Index(rest, "/"); i >= 0 {
			rest = rest[:i]
		}
		host = rest
		if h, p, ok := strings.Cut(rest, ":"); ok {
			host, port = h, p
		}
	} else {
		// Kurzform: der erste Doppelpunkt trennt den Pfad ab. Ihn als Port zu
		// lesen wäre der Fehler, den diese Verzweigung verhindert —
		// "git@github.com:marion909/VoltPanel.git" hätte sonst den Port
		// "marion909".
		host, _, _ = strings.Cut(rest, ":")
	}

	if host == "" {
		return "", "", "", fmt.Errorf("%w: in %q steht kein host", ErrInvalid, canonical)
	}
	return scheme, host, port, nil
}

// cleanPath prüft den Pfadteil und nimmt führende Schrägstriche weg.
func cleanPath(p string) (string, error) {
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		return "", fmt.Errorf("%w: in der adresse fehlt der pfad zum repository", ErrInvalid)
	}
	if strings.Contains(p, "..") {
		return "", fmt.Errorf("%w: der pfad darf kein .. enthalten", ErrInvalid)
	}
	if !reGitPath.MatchString(p) {
		return "", fmt.Errorf("%w: pfad %q", ErrInvalid, p)
	}
	return p, nil
}

// ValidRef prüft einen Branch- oder Tagnamen.
//
// Der Name wird ein Argument von `git checkout`. Ein führender Bindestrich wäre
// dort eine Option; die übrigen Regeln sind die von git selbst
// (git-check-ref-format), soweit sie hier zählen.
func ValidRef(ref string) bool {
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

// Steps ist die Liste der Buildschritte: Namen, keine Kommandozeilen.
//
// Der Unterschied ist die ganze Sicherheit daran. Eine Kommandozeile vom Kunden
// müsste jemand zerlegen, und wer zerlegt, landet früher oder später bei einer
// Shell. Ein Name schlägt hier eine feste Argumentliste nach, oder er wird
// abgelehnt.
//
// Das erste Element jeder Liste ist ein Schlüssel aus der Binary-Whitelist des
// Agents; dass es ihn dort wirklich gibt, hält ein Test im Agent fest.
var Steps = map[string][]string{
	"npm-ci":           {"npm", "ci"},
	"npm-install":      {"npm", "install"},
	"npm-build":        {"npm", "run", "build"},
	"npm-prod":         {"npm", "ci", "--omit=dev"},
	"composer-install": {"composer", "install", "--no-dev", "--optimize-autoloader", "--no-interaction"},
}

// ValidStep sagt, ob ein Name für einen Buildschritt steht.
func ValidStep(name string) bool {
	_, ok := Steps[name]
	return ok
}

// StepNames sind die möglichen Buildschritte, sortiert. Für die Oberfläche:
// sie soll die Namen anbieten, nicht ein Textfeld.
func StepNames() []string {
	out := make([]string, 0, len(Steps))
	for name := range Steps {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
