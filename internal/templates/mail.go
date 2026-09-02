package templates

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Die Dateien, aus denen Postfix und Dovecot ihre Benutzer kennen.
//
// Postfix und Dovecot lesen die Panel-Datenbank nicht. Das ist eine
// Entscheidung, keine Bequemlichkeit: ein Mailserver mit einer eigenen Kennung
// auf dieser Datenbank wäre der Weg, über eine Lücke im Mailserver an die
// Zugangsdaten aller Kunden zu kommen. Stattdessen schreibt der Agent aus den
// Zeilen flache Dateien — so wie er aus den Sites die Vhosts schreibt.
//
// Eine Map ist zeilenweise aufgebaut, und darin liegt die ganze Gefahr: was
// einen Zeilenumbruch in einen Wert bekommt, schreibt die nächste Zuordnung
// selbst. "post@example.at\nroot@fremde.at post@meine.at" wären zwei Einträge,
// und der zweite fängt fremde Post ab. Derselbe Mechanismus wie bei einer
// systemd-Unit, nur mit einer anderen Grammatik.
//
// Der Store prüft schon beim Anlegen. Hier wird noch einmal geprüft, und zwar
// hart: was nicht passt, kommt nicht in die Datei, sondern ergibt einen Fehler.
// Zwei Schranken, weil zwischen "steht in der Datenbank" und "steht in der
// Datei" eine Migration, ein Import oder ein direkter Zugriff liegen kann.

var (
	// reMailEntry ist eine Adresse, wie sie in einer Map stehen darf. Kein
	// Leerzeichen (es trennt die beiden Spalten), kein Umbruch (er trennt die
	// Zeilen), sonst nichts Besonderes.
	reMailEntry = regexp.MustCompile(`^[a-z0-9][a-z0-9._+-]{0,62}@` +
		`[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)

	// reMailDomainEntry ist eine Domäne in derselben Rolle.
	reMailDomainEntry = regexp.MustCompile(
		`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)

	// reMailHash ist das, was Dovecot als Passwort bekommt: ein Schema in
	// geschweiften Klammern und danach base64 oder eine crypt-Zeichenkette.
	// Ein Doppelpunkt darf darin nicht vorkommen — er trennt die Felder.
	reMailHash = regexp.MustCompile(`^\{[A-Z0-9-]{1,20}\}[A-Za-z0-9+/=$.,_-]{1,200}$`)
)

// MailboxEntry ist ein Postfach, wie die Dateien es brauchen.
type MailboxEntry struct {
	Address string
	// Hash ist das, was Dovecot prüft. Das Klartextpasswort kommt hier nie an:
	// der Agent hat es beim Erzeugen des Hashes und danach nicht mehr.
	Hash string
	// QuotaMB, 0 heißt unbegrenzt — wie überall im Panel.
	QuotaMB int64
	// Maildir ist der Pfad unterhalb der Mailwurzel, ohne führenden Schrägstrich.
	Maildir string
}

// AliasEntry leitet eine Adresse an eine andere weiter.
//
// Source darf mit "@" beginnen: das ist die Schreibweise für einen Catch-All
// ("@example.at"), und Postfix versteht sie an dieser Stelle.
type AliasEntry struct {
	Source      string
	Destination string
}

// MailData ist der Stand, aus dem alle Mail-Dateien entstehen.
type MailData struct {
	GeneratedAt string
	Domains     []string
	Mailboxes   []MailboxEntry
	Aliases     []AliasEntry
	// MailRoot ist das Verzeichnis, unter dem die Maildirs liegen.
	MailRoot string
	// VMailUID/GID ist die Kennung, der alle Maildirs gehören.
	VMailUID int
	VMailGID int
}

// checkMailData prüft jeden Wert, der in eine Datei geht.
func checkMailData(d *MailData) error {
	if !strings.HasPrefix(d.MailRoot, "/") || strings.ContainsAny(d.MailRoot, " \n\t\r") {
		return fmt.Errorf("%q ist kein zulässiges mailverzeichnis", d.MailRoot)
	}
	if d.VMailUID < 1000 || d.VMailGID < 1000 {
		// Kein Systemkonto und erst recht nicht root: die Maildirs gehören
		// einem eigenen unprivilegierten Benutzer.
		return fmt.Errorf("die kennung für die maildirs ist zu niedrig (%d:%d)",
			d.VMailUID, d.VMailGID)
	}

	for _, dom := range d.Domains {
		if !reMailDomainEntry.MatchString(dom) {
			return fmt.Errorf("%q ist keine domäne, die in eine map gehört", dom)
		}
	}
	for _, m := range d.Mailboxes {
		switch {
		case !reMailEntry.MatchString(m.Address):
			return fmt.Errorf("%q ist keine adresse, die in eine map gehört", m.Address)
		case !reMailHash.MatchString(m.Hash):
			// Ohne diese Zeile stünde ein leeres Passwortfeld in der Datei —
			// und je nach Dovecot-Einstellung käme damit jeder herein.
			return fmt.Errorf("das passwortfeld von %s ist unbrauchbar", m.Address)
		case m.QuotaMB < 0:
			return fmt.Errorf("die quota von %s ist negativ", m.Address)
		case !gueltigerMaildir(m.Maildir):
			return fmt.Errorf("%q ist kein zulässiges maildir", m.Maildir)
		}
	}
	for _, a := range d.Aliases {
		quelle := strings.TrimPrefix(a.Source, "@")
		istCatchAll := strings.HasPrefix(a.Source, "@")
		if istCatchAll && !reMailDomainEntry.MatchString(quelle) {
			return fmt.Errorf("%q ist kein catch-all", a.Source)
		}
		if !istCatchAll && !reMailEntry.MatchString(a.Source) {
			return fmt.Errorf("%q ist keine adresse, die in eine map gehört", a.Source)
		}
		if !reMailEntry.MatchString(a.Destination) {
			return fmt.Errorf("%q ist kein ziel, das in eine map gehört", a.Destination)
		}
	}
	return nil
}

// gueltigerMaildir prüft den relativen Pfad eines Postfachs.
func gueltigerMaildir(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "..") {
		return false
	}
	if strings.ContainsAny(p, " \n\t\r:\\") {
		return false
	}
	return true
}

// RenderPostfixDomains schreibt die Liste der Domänen, für die Post angenommen
// wird.
//
// Format: "domain OK" je Zeile. Postfix fragt die Map nur, ob es einen Eintrag
// gibt; was rechts steht, ist ihm gleich — "OK" ist die übliche Schreibweise.
func RenderPostfixDomains(d MailData) (string, error) {
	if err := checkMailData(&d); err != nil {
		return "", err
	}
	var b strings.Builder
	mapKopf(&b, d.GeneratedAt, "Domänen, für die dieser Server Post annimmt.")
	domains := append([]string(nil), d.Domains...)
	sort.Strings(domains)
	for _, dom := range domains {
		fmt.Fprintf(&b, "%s\tOK\n", dom)
	}
	return b.String(), nil
}

// RenderPostfixMailboxes ordnet jeder Adresse ihr Maildir zu.
func RenderPostfixMailboxes(d MailData) (string, error) {
	if err := checkMailData(&d); err != nil {
		return "", err
	}
	var b strings.Builder
	mapKopf(&b, d.GeneratedAt, "Adresse -> Maildir, relativ zu "+d.MailRoot+".")
	boxen := append([]MailboxEntry(nil), d.Mailboxes...)
	sort.Slice(boxen, func(i, j int) bool { return boxen[i].Address < boxen[j].Address })
	for _, m := range boxen {
		// Der abschließende Schrägstrich sagt Postfix "Maildir", nicht "mbox".
		// Ohne ihn schreibt es alle Mails in eine einzige Datei.
		fmt.Fprintf(&b, "%s\t%s/\n", m.Address, strings.TrimSuffix(m.Maildir, "/"))
	}
	return b.String(), nil
}

// RenderPostfixAliases schreibt Weiterleitungen und Catch-Alls.
func RenderPostfixAliases(d MailData) (string, error) {
	if err := checkMailData(&d); err != nil {
		return "", err
	}
	var b strings.Builder
	mapKopf(&b, d.GeneratedAt, "Weiterleitungen. \"@domain\" ist der Catch-All.")
	aliase := append([]AliasEntry(nil), d.Aliases...)
	sort.Slice(aliase, func(i, j int) bool {
		if aliase[i].Source != aliase[j].Source {
			return aliase[i].Source < aliase[j].Source
		}
		return aliase[i].Destination < aliase[j].Destination
	})
	for _, a := range aliase {
		fmt.Fprintf(&b, "%s\t%s\n", a.Source, a.Destination)
	}
	return b.String(), nil
}

// RenderDovecotUsers schreibt die Passwortdatei.
//
// Format von Dovecots passwd-file:
//
//	benutzer:passwort:uid:gid:gecos:home:shell:extra
//
// Gebraucht werden davon vier Felder. In "extra" steht die Quota-Regel — dort,
// weil Dovecot sie je Benutzer aus der userdb nimmt und nicht aus der
// Konfiguration.
func RenderDovecotUsers(d MailData) (string, error) {
	if err := checkMailData(&d); err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Von VoltPanel erzeugt — nicht von Hand bearbeiten.\n")
	fmt.Fprintf(&b, "# Jede Änderung geht beim nächsten Schreiben verloren.\n")
	fmt.Fprintf(&b, "# Erzeugt: %s\n", d.GeneratedAt)

	boxen := append([]MailboxEntry(nil), d.Mailboxes...)
	sort.Slice(boxen, func(i, j int) bool { return boxen[i].Address < boxen[j].Address })
	for _, m := range boxen {
		heim := strings.TrimSuffix(d.MailRoot, "/") + "/" + strings.TrimSuffix(m.Maildir, "/")
		extra := ""
		if m.QuotaMB > 0 {
			// 0 heißt im Panel "unbegrenzt". Eine Regel mit 0M wäre in Dovecot
			// das Gegenteil: gar kein Platz.
			extra = fmt.Sprintf("userdb_quota_rule=*:storage=%dM", m.QuotaMB)
		}
		fmt.Fprintf(&b, "%s:%s:%d:%d::%s::%s\n",
			m.Address, m.Hash, d.VMailUID, d.VMailGID, heim, extra)
	}
	return b.String(), nil
}

func mapKopf(b *strings.Builder, erzeugt, was string) {
	fmt.Fprintf(b, "# Von VoltPanel erzeugt — nicht von Hand bearbeiten.\n")
	fmt.Fprintf(b, "# Jede Änderung geht beim nächsten Schreiben verloren.\n")
	fmt.Fprintf(b, "# %s\n", was)
	fmt.Fprintf(b, "# Erzeugt: %s\n", erzeugt)
}

// NowStamp ist der Zeitstempel im Kopf jeder erzeugten Datei.
func NowStamp() string { return time.Now().Format(time.RFC3339) }

// DKIMEntry ist ein Schlüssel, mit dem für eine Domäne unterschrieben wird.
type DKIMEntry struct {
	Domain   string
	Selector string
	// KeyPath ist die Datei, in der der private Schlüssel liegt. Sie entsteht
	// im Agent aus Domäne und Selector, nicht aus einer Anfrage.
	KeyPath string
}

// checkDKIM prüft, was in die OpenDKIM-Tabellen geht.
//
// Dieselbe Gefahr wie bei den Postfix-Maps, mit einem Zusatz: in der KeyTable
// steht ein Dateipfad. Was dort hineingerät, liest OpenDKIM als Schlüssel —
// und unterschreibt damit im Namen einer Domäne.
func checkDKIM(e DKIMEntry) error {
	switch {
	case !reMailDomainEntry.MatchString(e.Domain):
		return fmt.Errorf("%q ist keine domäne für dkim", e.Domain)
	case !reDKIMSel.MatchString(e.Selector):
		return fmt.Errorf("%q ist kein dkim-selector", e.Selector)
	case !strings.HasPrefix(e.KeyPath, "/") || strings.ContainsAny(e.KeyPath, " \n\t\r:"):
		return fmt.Errorf("%q ist kein zulässiger pfad für einen schlüssel", e.KeyPath)
	}
	return nil
}

var reDKIMSel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`)

// RenderDKIMKeyTable ordnet jedem Selector seine Schlüsseldatei zu.
func RenderDKIMKeyTable(entries []DKIMEntry, erzeugt string) (string, error) {
	var b strings.Builder
	mapKopf(&b, erzeugt, "Selector -> Domäne:Selector:Schlüsseldatei.")
	sortiert := sortiereDKIM(entries)
	for _, e := range sortiert {
		if err := checkDKIM(e); err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "%s._domainkey.%s %s:%s:%s\n",
			e.Selector, e.Domain, e.Domain, e.Selector, e.KeyPath)
	}
	return b.String(), nil
}

// RenderDKIMSigningTable sagt, welche Absender mit welchem Schlüssel
// unterschreiben.
func RenderDKIMSigningTable(entries []DKIMEntry, erzeugt string) (string, error) {
	var b strings.Builder
	mapKopf(&b, erzeugt, "Absender -> Selector. \"*@domain\" gilt für alle Postfächer.")
	for _, e := range sortiereDKIM(entries) {
		if err := checkDKIM(e); err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "*@%s %s._domainkey.%s\n", e.Domain, e.Selector, e.Domain)
	}
	return b.String(), nil
}

// RenderDKIMTrustedHosts sind die Absender, für die überhaupt unterschrieben
// wird.
//
// Nur der Server selbst. Eine Liste, die weiter reicht, macht aus dem
// Mailserver ein offenes Relay mit gültiger Unterschrift — und die Domäne
// bekommt den Ruf dafür.
func RenderDKIMTrustedHosts(erzeugt string) string {
	var b strings.Builder
	mapKopf(&b, erzeugt, "Absender, für die unterschrieben wird — nur dieser Server.")
	b.WriteString("127.0.0.1\n::1\nlocalhost\n")
	return b.String()
}

func sortiereDKIM(entries []DKIMEntry) []DKIMEntry {
	out := append([]DKIMEntry(nil), entries...)
	sort.Slice(out, func(i, j int) bool { return out[i].Domain < out[j].Domain })
	return out
}
