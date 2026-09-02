package agent

import (
	"context"
	"crypto/rand"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/marion909/voltpanel/internal/templates"
)

// Mail: die Dateien, aus denen Postfix und Dovecot ihre Benutzer kennen.
//
// Der Aufbau folgt dem der Vhosts. Das Panel beschreibt den Zustand — welche
// Domänen, welche Postfächer, welche Weiterleitungen —, der Agent schreibt
// daraus die Dateien und lädt die Dienste neu. Postfix und Dovecot sehen die
// Panel-Datenbank nie; ein Mailserver mit einer Kennung darauf wäre der Weg,
// über eine Lücke im Mailserver an die Zugangsdaten aller Kunden zu kommen.
//
// Das Passwort kommt im Klartext über den Socket und verlässt diese Datei
// nicht: der Agent bildet den Hash und schreibt nur den. Denselben Weg gehen
// die FTP-Zugänge. Über die Kommandozeile eines Hilfsprogramms ginge es auch —
// aber dann stünde es in der Prozessliste, und die kann jeder Benutzer des
// Servers lesen.

// Dieselben Muster wie im Store, hier noch einmal: der Agent darf nicht davon
// abhängen, dass jemand dort später etwas lockert.
var (
	reMailLocal      = regexp.MustCompile(`^[a-z0-9]([a-z0-9._+-]{0,62}[a-z0-9])?$`)
	reDKIMSelector   = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`)
	reMailDomainTeil = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)
)

const (
	postfixMailDir = "/etc/postfix/volt"
	dovecotMailDir = "/etc/dovecot/volt"
	// vmailUser ist der Benutzer, dem alle Maildirs gehören.
	//
	// Einer für alle, nicht einer je Mandant. Das ist die übliche Bauart eines
	// virtuellen Mailservers, und sie hat eine Grenze, die hier benannt gehört:
	// die Trennung zwischen zwei Mandanten liegt in Dovecot, nicht im
	// Dateisystem. Für die Websites ist es umgekehrt — dort hat jede Site ihre
	// eigene Kennung.
	vmailUser = "vmail"
	vmailHome = "/var/vmail"
	// opendkimDir ist das Verzeichnis, in dem OpenDKIM seine Tabellen sucht.
	opendkimDir = "/etc/opendkim"
	// dovecotConfD ist das Verzeichnis, aus dem Dovecot Ergaenzungen liest.
	dovecotConfD = "/etc/dovecot/conf.d"
	// rspamdDir sagt, ob Rspamd installiert ist. Seine Regeln bringt es
	// selbst mit; das Panel traegt es nur als zweiten Milter ein.
	rspamdDir = "/etc/rspamd"
)

// MailboxParams ist ein Postfach, wie das Panel es beschreibt.
type MailboxParams struct {
	Address string `json:"address"`
	// Password ist Klartext und wird hier zu einem Hash. Ist es leer, bleibt
	// das bestehende Passwort stehen — dafür trägt der Aufrufer Hash mit.
	Password string `json:"password"`
	Hash     string `json:"hash"`
	QuotaMB  int64  `json:"quota_mb"`
}

// MailAliasParams ist eine Weiterleitung. "@domain" ist der Catch-All.
type MailAliasParams struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
}

// MailApplyParams ist der vollständige Sollzustand.
//
// Vollständig und nicht als Änderung: dieselbe Bauart wie bei den Vhosts. Wer
// einzelne Zeilen schickt, hat irgendwann eine Datei, die zu keinem Stand der
// Datenbank mehr passt — und niemand merkt es, bis eine Adresse Post bekommt,
// die es nicht mehr geben sollte.
type MailApplyParams struct {
	Domains   []string          `json:"domains"`
	Mailboxes []MailboxParams   `json:"mailboxes"`
	Aliases   []MailAliasParams `json:"aliases"`
	DKIM      []DKIMParams      `json:"dkim"`
}

// DKIMParams ist ein Schlüssel, mit dem für eine Domäne unterschrieben wird.
//
// Der private Teil kommt im Klartext über den Socket und wird hier in eine
// Datei geschrieben, die nur OpenDKIM lesen darf. Der Pfad entsteht aus
// Domäne und Selector — nicht aus der Anfrage: was OpenDKIM aus der KeyTable
// liest, unterschreibt im Namen einer Domäne.
type DKIMParams struct {
	Domain     string `json:"domain"`
	Selector   string `json:"selector"`
	PrivateKey string `json:"private_key"`
}

// MailStatus sagt, was auf diesem Server steht.
type MailStatus struct {
	PostfixInstalled bool `json:"postfix_installed"`
	DovecotInstalled bool `json:"dovecot_installed"`
	// Configured heißt: die Dateien des Panels stehen und Postfix zeigt darauf.
	Configured bool `json:"configured"`
	// HashScheme ist das Passwortschema, das die Datei benutzt.
	HashScheme string   `json:"hash_scheme,omitempty"`
	Mailboxes  int      `json:"mailboxes"`
	Domains    int      `json:"domains"`
	Hinweise   []string `json:"hinweise,omitempty"`
}

// opMailStatus berichtet, was da ist und was fehlt.
func (s *Server) opMailStatus(_ context.Context, _ json.RawMessage) (any, error) {
	res := MailStatus{HashScheme: "SSHA512"}
	res.PostfixInstalled = fileExists(allowedBinaries["postconf"])
	res.DovecotInstalled = fileExists(allowedBinaries["doveadm"])

	if !res.PostfixInstalled {
		res.Hinweise = append(res.Hinweise,
			"Postfix ist auf diesem Server nicht installiert. Ohne es nimmt niemand Post an.")
	}
	if !res.DovecotInstalled {
		res.Hinweise = append(res.Hinweise,
			"Dovecot ist auf diesem Server nicht installiert. Ohne es kommt niemand an seine Post.")
	}

	if b, err := os.ReadFile(filepath.Join(postfixMailDir, "domains")); err == nil {
		res.Configured = true
		res.Domains = zaehleEintraege(string(b))
	}
	if b, err := os.ReadFile(filepath.Join(postfixMailDir, "mailboxes")); err == nil {
		res.Mailboxes = zaehleEintraege(string(b))
	}
	return res, nil
}

// zaehleEintraege zählt die Zeilen einer Map, ohne Kopf und Leerzeilen.
func zaehleEintraege(inhalt string) int {
	var n int
	for _, line := range strings.Split(inhalt, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			n++
		}
	}
	return n
}

// opMailApply schreibt den Sollzustand in die Dateien.
func (s *Server) opMailApply(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[MailApplyParams](raw, OpMailApply)
	if err != nil {
		return nil, err
	}
	if !fileExists(allowedBinaries["postmap"]) {
		return nil, opErr(OpMailApply, "postfix ist auf diesem server nicht installiert")
	}

	uid, gid, err := siteUserIDs(OpMailApply, vmailUser)
	if err != nil {
		return nil, opErr(OpMailApply,
			"den benutzer %s gibt es noch nicht — `mail.setup` legt ihn an", vmailUser)
	}

	d := templates.MailData{
		GeneratedAt: templates.NowStamp(),
		MailRoot:    vmailHome,
		VMailUID:    uid,
		VMailGID:    gid,
		Domains:     p.Domains,
	}
	for _, m := range p.Mailboxes {
		hash := m.Hash
		if m.Password != "" {
			hash = hashSSHA512(m.Password)
		}
		// Das Maildir bildet der Agent aus der Adresse. Ein Pfad aus der
		// Anfrage wäre ein Weg, ein Postfach an eine beliebige Stelle des
		// Dateisystems zu legen — templates prüft ihn zwar, aber die Frage
		// stellt sich gar nicht erst, wenn er hier entsteht.
		verzeichnis, err := maildirFuer(m.Address)
		if err != nil {
			return nil, opInputErr(OpMailApply, "%v", err)
		}
		d.Mailboxes = append(d.Mailboxes, templates.MailboxEntry{
			Address: m.Address, Hash: hash, QuotaMB: m.QuotaMB, Maildir: verzeichnis,
		})
	}
	for _, a := range p.Aliases {
		d.Aliases = append(d.Aliases, templates.AliasEntry{
			Source: a.Source, Destination: a.Destination,
		})
	}

	domains, err := templates.RenderPostfixDomains(d)
	if err != nil {
		return nil, opInputErr(OpMailApply, "%v", err)
	}
	boxen, err := templates.RenderPostfixMailboxes(d)
	if err != nil {
		return nil, opInputErr(OpMailApply, "%v", err)
	}
	aliase, err := templates.RenderPostfixAliases(d)
	if err != nil {
		return nil, opInputErr(OpMailApply, "%v", err)
	}
	users, err := templates.RenderDovecotUsers(d)
	if err != nil {
		return nil, opInputErr(OpMailApply, "%v", err)
	}

	for _, dir := range []string{postfixMailDir, dovecotMailDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, opErr(OpMailApply, "%s anlegen: %v", dir, err)
		}
	}

	for name, inhalt := range map[string]string{
		"domains":   domains,
		"mailboxes": boxen,
		"aliases":   aliase,
	} {
		pfad := filepath.Join(postfixMailDir, name)
		if err := writeFileAtomic(pfad, []byte(inhalt), 0o644); err != nil {
			return nil, opErr(OpMailApply, "%s schreiben: %v", pfad, err)
		}
		// postmap erzeugt die .db, aus der Postfix wirklich liest. Ohne diesen
		// Schritt bleibt die Textdatei wirkungslos, und der Fehler sieht aus
		// wie "die Adresse gibt es nicht".
		if out, err := run(ctx, shortTimeout, "postmap", "hash:"+pfad); err != nil {
			return nil, opErr(OpMailApply, "postmap %s: %s", name, truncate(out, 300))
		}
	}

	// Die Passwortdatei ist die eine, die niemand lesen darf außer Dovecot.
	// 0640 und die Gruppe des Dienstes; kommt der Benutzer nicht vor, bleibt
	// es bei root — dann liest Dovecot sie als root, was es ohnehin tut.
	pfad := filepath.Join(dovecotMailDir, "users")
	if err := writeFileAtomic(pfad, []byte(users), 0o640); err != nil {
		return nil, opErr(OpMailApply, "%s schreiben: %v", pfad, err)
	}

	if err := s.schreibeDKIM(ctx, p.DKIM); err != nil {
		return nil, err
	}

	var meldungen []string
	if out, err := run(ctx, shortTimeout, "systemctl", "reload", "postfix"); err != nil {
		meldungen = append(meldungen, "postfix nicht neu geladen: "+truncate(out, 200))
	}
	if fileExists(allowedBinaries["doveadm"]) {
		if out, err := run(ctx, shortTimeout, "doveadm", "reload"); err != nil {
			meldungen = append(meldungen, "dovecot nicht neu geladen: "+truncate(out, 200))
		}
	}

	text := fmt.Sprintf("%d domänen, %d postfächer, %d weiterleitungen geschrieben",
		len(d.Domains), len(d.Mailboxes), len(d.Aliases))
	if len(meldungen) > 0 {
		text += " — " + strings.Join(meldungen, "; ")
	}
	return TextResult{Text: text}, nil
}

// schreibeDKIM legt die Schlüsseldateien und die Tabellen von OpenDKIM an.
//
// Ist OpenDKIM nicht installiert, passiert nichts — und das ist kein Fehler:
// eine Domäne kann einen Schlüssel haben, bevor der Dienst dasteht. Der
// DNS-Eintrag ist ohnehin der langsamere Teil.
func (s *Server) schreibeDKIM(ctx context.Context, keys []DKIMParams) error {
	if !fileExists(allowedBinaries["opendkim-testkey"]) && !dirExists(opendkimDir) {
		return nil
	}

	uid, gid := opendkimIDs()
	var eintraege []templates.DKIMEntry

	for _, k := range keys {
		if !reMailDomainTeil.MatchString(k.Domain) || !reDKIMSelector.MatchString(k.Selector) {
			return opInputErr(OpMailApply, "%q/%q ist kein zulässiges dkim-paar",
				k.Domain, k.Selector)
		}
		if !strings.Contains(k.PrivateKey, "PRIVATE KEY") {
			return opInputErr(OpMailApply, "der dkim-schlüssel von %s ist kein pem", k.Domain)
		}

		dir := filepath.Join(opendkimDir, "keys", k.Domain)
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return opErr(OpMailApply, "%s anlegen: %v", dir, err)
		}
		pfad := filepath.Join(dir, k.Selector+".private")
		// 0600: den Schlüssel liest OpenDKIM und sonst niemand. Wer ihn hat,
		// unterschreibt Mail im Namen dieser Domäne.
		if err := writeFileAtomic(pfad, []byte(k.PrivateKey), 0o600); err != nil {
			return opErr(OpMailApply, "dkim-schlüssel schreiben: %v", err)
		}
		if uid > 0 {
			_ = os.Chown(pfad, uid, gid)
			_ = os.Chown(dir, uid, gid)
		}
		eintraege = append(eintraege, templates.DKIMEntry{
			Domain: k.Domain, Selector: k.Selector, KeyPath: pfad,
		})
	}

	stamp := templates.NowStamp()
	keyTable, err := templates.RenderDKIMKeyTable(eintraege, stamp)
	if err != nil {
		return opInputErr(OpMailApply, "%v", err)
	}
	signing, err := templates.RenderDKIMSigningTable(eintraege, stamp)
	if err != nil {
		return opInputErr(OpMailApply, "%v", err)
	}

	for name, inhalt := range map[string]string{
		"KeyTable":     keyTable,
		"SigningTable": signing,
		"TrustedHosts": templates.RenderDKIMTrustedHosts(stamp),
	} {
		if err := writeFileAtomic(filepath.Join(opendkimDir, name), []byte(inhalt), 0o644); err != nil {
			return opErr(OpMailApply, "%s schreiben: %v", name, err)
		}
	}

	// Neu laden, nicht neu starten: ein Neustart hielte die Zustellung an,
	// und Postfix wartet dann auf einen Milter, den es gerade nicht gibt.
	_, _ = run(ctx, shortTimeout, "systemctl", "reload", "opendkim")
	return nil
}

// opendkimIDs sucht die Kennung, unter der OpenDKIM läuft.
func opendkimIDs() (int, int) {
	uid, gid, err := siteUserIDs(OpMailApply, "opendkim")
	if err != nil {
		return -1, -1
	}
	return uid, gid
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// mailPorts sind die Ports, ohne die ein Mailserver keiner ist.
//
//	25   die Post von anderen Servern
//	587  die Einlieferung durch eigene Kunden, mit Ausweis
//	993  IMAP über TLS
//
// Nicht dabei: 110 und 143 (POP3 und IMAP im Klartext) sowie 465. Die ersten
// beiden würden Passwörter über das Netz schicken, der dritte ist die alte
// Schreibweise für dasselbe wie 587 — ein Port weniger ist ein Port weniger.
var mailPorts = []string{"25/tcp", "587/tcp", "993/tcp"}

// openMailPorts gibt sie in ufw frei, sofern ufw überhaupt läuft.
//
// Feste Argumente, wie bei FTP. Für nftables geschieht bewusst nichts: dort
// gibt es kein Regelwerk, in das sich eine Zeile gefahrlos einfügen ließe.
func (s *Server) openMailPorts(ctx context.Context) string {
	out, err := run(ctx, shortTimeout, "ufw", "status")
	if err != nil || !strings.Contains(out, "Status: active") {
		return "Die Ports 25, 587 und 993 müssen in der Firewall offen sein."
	}
	for _, regel := range mailPorts {
		if out, err := run(ctx, shortTimeout, "ufw", "allow", regel); err != nil {
			s.log.Warn("ufw-regel nicht gesetzt", "regel", regel, "err", err,
				"out", truncate(out, 200))
			return "Die Firewall-Regeln konnten nicht gesetzt werden — bitte 25, 587 " +
				"und 993 selbst freigeben."
		}
	}
	return "ufw: 25, 587 und 993 sind freigegeben."
}

// mailCert sucht das Zertifikat, das Postfix und Dovecot benutzen sollen.
//
// Dasselbe wie beim Panel und bei FTP: die Pfade entstehen aus der
// Konfiguration des Agents, nicht aus einer Anfrage. Gefunden wird das erste
// lesbare Paar; gibt es keines, läuft Mail unverschlüsselt, und das steht
// dann im Ergebnis.
func (s *Server) mailCert() (cert, key string) {
	for _, paar := range s.panelCertChain() {
		if fileExists(paar.Cert) && fileExists(paar.Key) {
			return paar.Cert, paar.Key
		}
	}
	return "", ""
}

// maildirFuer bildet den Pfad eines Postfachs aus seiner Adresse.
//
// Zerlegt und neu gebaut, nicht durchgereicht: "domain/local" aus zwei
// geprüften Teilen kann nichts anderes sein als das.
func maildirFuer(address string) (string, error) {
	local, domain, ok := strings.Cut(strings.ToLower(strings.TrimSpace(address)), "@")
	if !ok {
		return "", fmt.Errorf("%q ist keine adresse", address)
	}
	if !reMailLocal.MatchString(local) || !reMailDomainTeil.MatchString(domain) {
		return "", fmt.Errorf("%q ist keine adresse", address)
	}
	return domain + "/" + local, nil
}

// hashSSHA512 erzeugt das Passwortfeld für Dovecot.
//
// {SSHA512} ist base64(sha512(passwort + salz) + salz) — die Schreibweise, die
// Dovecot ohne zusätzliche Bibliothek versteht. ARGON2ID wäre die bessere
// Ableitung, hängt in Dovecot aber an libsodium; wo das fehlt, wären alle
// Postfächer auf einen Schlag nicht mehr anmeldbar, und der Grund stünde
// nirgends.
//
// Das ist ein bewusster Tausch, kein Versehen: das Klartextpasswort liegt
// ohnehin verschlüsselt in der Panel-Datenbank, weil ein Mailkonto in einem
// Mailprogramm eingetragen wird. Wer die Datenbank hat, braucht den Hash nicht
// zu brechen.
func hashSSHA512(password string) string {
	salz := make([]byte, 8)
	if _, err := rand.Read(salz); err != nil {
		// Ohne Zufall kein Hash. Ein fester Wert wäre schlimmer als ein
		// Fehler, deshalb ein Feld, das die Prüfung in templates ablehnt.
		return ""
	}
	return sshaMitSalz(password, salz)
}

// sshaMitSalz ist der Kern, mit gegebenem Salz — damit er sich prüfen lässt.
func sshaMitSalz(password string, salz []byte) string {
	sum := sha512.Sum512(append([]byte(password), salz...))
	return "{SSHA512}" + base64.StdEncoding.EncodeToString(append(sum[:], salz...))
}

// opMailSetup richtet ein, was einmal eingerichtet werden muss.
//
// Der Benutzer für die Maildirs, die Verzeichnisse, und die Einstellungen in
// Postfix, die auf die Dateien des Panels zeigen. Gesetzt werden sie mit
// `postconf -e` und nicht durch Schreiben in main.cf: postconf kennt die
// Syntax der Datei, behält Kommentare und ist wiederholbar. Eine von Hand
// zusammengesetzte main.cf wäre genau das manuelle Patchen, das dieses Projekt
// nicht tut.
func (s *Server) opMailSetup(ctx context.Context, _ json.RawMessage) (any, error) {
	if !fileExists(allowedBinaries["postconf"]) {
		return nil, opErr(OpMailSetup, "postfix ist auf diesem server nicht installiert")
	}

	var schritte []string
	if out, err := run(ctx, longTimeout, "useradd",
		"--home-dir", vmailHome, "--create-home", "--shell", "/usr/sbin/nologin",
		"--user-group", "--comment", "voltpanel mail storage", vmailUser); err != nil {
		if !strings.Contains(out, "already exists") {
			return nil, opErr(OpMailSetup, "benutzer %s: %s", vmailUser, truncate(out, 300))
		}
		schritte = append(schritte, "benutzer "+vmailUser+" war schon da")
	} else {
		schritte = append(schritte, "benutzer "+vmailUser+" angelegt")
	}

	uid, gid, err := siteUserIDs(OpMailSetup, vmailUser)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(vmailHome, 0o750); err != nil {
		return nil, opErr(OpMailSetup, "%s anlegen: %v", vmailHome, err)
	}
	if err := os.Chown(vmailHome, uid, gid); err != nil {
		return nil, opErr(OpMailSetup, "%s übereignen: %v", vmailHome, err)
	}

	// Die Einstellungen, die Postfix auf die Dateien des Panels richten.
	// Jede einzeln, damit eine falsche nicht die anderen mitnimmt.
	einstellungen := [][2]string{
		{"virtual_mailbox_domains", "hash:" + postfixMailDir + "/domains"},
		{"virtual_mailbox_maps", "hash:" + postfixMailDir + "/mailboxes"},
		{"virtual_alias_maps", "hash:" + postfixMailDir + "/aliases"},
		{"virtual_mailbox_base", vmailHome},
		{"virtual_uid_maps", "static:" + strconv.Itoa(uid)},
		{"virtual_gid_maps", "static:" + strconv.Itoa(gid)},
		// Ohne diese Zeile nimmt Postfix Post für jede Domäne an, die in
		// virtual_mailbox_domains steht — und leitet sie an sich selbst weiter.
		{"virtual_transport", "virtual"},
	}
	for _, e := range einstellungen {
		if out, err := run(ctx, shortTimeout, "postconf", "-e", e[0]+"="+e[1]); err != nil {
			return nil, opErr(OpMailSetup, "postconf %s: %s", e[0], truncate(out, 200))
		}
	}
	schritte = append(schritte, "postfix zeigt auf "+postfixMailDir)

	// TLS und SASL. Ohne beides nimmt der Server zwar Post an, aber niemand
	// kann über ihn welche verschicken — und wer es doch tut, schickt sein
	// Passwort im Klartext über das Netz.
	cert, key := s.mailCert()
	if cert != "" {
		for _, e := range [][2]string{
			{"smtpd_tls_cert_file", cert},
			{"smtpd_tls_key_file", key},
			{"smtpd_tls_security_level", "may"},
			// Ausgehend ebenso: wenn die Gegenstelle TLS kann, wird es
			// benutzt. "may" und nicht "encrypt", weil ein Server, der kein
			// TLS kann, sonst gar keine Post mehr bekäme.
			{"smtp_tls_security_level", "may"},
			{"smtpd_tls_protocols", "!SSLv2,!SSLv3,!TLSv1,!TLSv1.1"},
		} {
			if out, err := run(ctx, shortTimeout, "postconf", "-e", e[0]+"="+e[1]); err != nil {
				return nil, opErr(OpMailSetup, "postconf %s: %s", e[0], truncate(out, 200))
			}
		}
		schritte = append(schritte, "tls aus "+cert)
	} else {
		schritte = append(schritte, "kein zertifikat gefunden — smtp läuft unverschlüsselt")
	}

	// SASL über Dovecot: Postfix fragt dort, ob ein Absender sich ausweisen
	// kann. Der Socket dafür steht in der Dovecot-Konfiguration weiter unten.
	for _, e := range [][2]string{
		{"smtpd_sasl_type", "dovecot"},
		{"smtpd_sasl_path", "private/auth"},
		{"smtpd_sasl_auth_enable", "yes"},
		// Die Reihenfolge ist die Zeile, auf die es ankommt: erst die eigenen
		// Netze, dann die ausgewiesenen Absender, dann ablehnen. Ohne das
		// abschließende reject_unauth_destination ist der Server ein offenes
		// Relay, und das merkt man daran, dass die IP auf jeder Sperrliste
		// steht.
		{"smtpd_relay_restrictions",
			"permit_mynetworks,permit_sasl_authenticated,reject_unauth_destination"},
	} {
		if out, err := run(ctx, shortTimeout, "postconf", "-e", e[0]+"="+e[1]); err != nil {
			return nil, opErr(OpMailSetup, "postconf %s: %s", e[0], truncate(out, 200))
		}
	}
	schritte = append(schritte, "sasl über dovecot, relay nur für ausgewiesene absender")

	// Der Einlieferungsport 587. Ohne ihn müsste ein Kunde über Port 25
	// senden, den viele Anbieter für ausgehende Verbindungen sperren.
	if out, err := run(ctx, shortTimeout, "postconf", "-M",
		"submission/inet=submission inet n - y - - smtpd"); err != nil {
		schritte = append(schritte, "port 587 nicht eingerichtet: "+truncate(out, 120))
	} else {
		for _, e := range []string{
			"submission/inet/syslog_name=postfix/submission",
			"submission/inet/smtpd_tls_security_level=encrypt",
			"submission/inet/smtpd_sasl_auth_enable=yes",
			"submission/inet/smtpd_relay_restrictions=permit_sasl_authenticated,reject",
		} {
			if out, err := run(ctx, shortTimeout, "postconf", "-P", e); err != nil {
				return nil, opErr(OpMailSetup, "postconf -P %s: %s", e, truncate(out, 200))
			}
		}
		schritte = append(schritte, "einlieferung auf 587, nur mit tls und ausweis")
	}

	// Und die Dovecot-Ergänzung: ohne sie kennt Dovecot die Passwortdatei
	// nicht, die der Agent schreibt — die Postfächer stünden da und niemand
	// käme herein.
	if dirExists(dovecotConfD) {
		// Der Hostname für postmaster_address kommt aus Postfix, nicht aus
		// einer Anfrage. Fehlt er oder taugt er nicht, bleibt es bei der
		// Zustellung durch Postfix — eine Dovecot-Konfiguration, die nicht
		// startet, hielte jede Mail in der Warteschlange fest.
		hostname := ""
		if out, err := run(ctx, shortTimeout, "postconf", "-h", "myhostname"); err == nil {
			hostname = strings.TrimSpace(out)
		}

		conf, err := templates.RenderDovecotConf(templates.DovecotData{
			GeneratedAt: templates.NowStamp(),
			MailRoot:    vmailHome,
			UsersFile:   filepath.Join(dovecotMailDir, "users"),
			Hostname:    hostname,
			VMailUID:    uid,
			VMailGID:    gid,
			CertPath:    cert,
			KeyPath:     key,
		})
		if err != nil {
			return nil, opErr(OpMailSetup, "dovecot-konfiguration: %v", err)
		}
		pfad := filepath.Join(dovecotConfD, "99-volt.conf")
		if err := writeFileAtomic(pfad, []byte(conf), 0o644); err != nil {
			return nil, opErr(OpMailSetup, "%s schreiben: %v", pfad, err)
		}
		if out, err := run(ctx, shortTimeout, "systemctl", "reload", "dovecot"); err != nil {
			schritte = append(schritte, "dovecot nicht neu geladen: "+truncate(out, 150))
		} else {
			schritte = append(schritte, "dovecot kennt die postfächer")

			// Erst wenn Dovecot wirklich neu geladen hat, wird die Zustellung
			// umgestellt. Andersherum zeigte Postfix auf einen LMTP-Dienst,
			// den es noch nicht gibt — die Mail ginge dabei nicht verloren,
			// sie bliebe in der Warteschlange, aber sie käme eben nicht an.
			if out, err := run(ctx, shortTimeout, "postconf", "-e",
				"virtual_transport=lmtp:unix:private/dovecot-lmtp"); err != nil {
				schritte = append(schritte, "zustellung nicht umgestellt: "+truncate(out, 150))
			} else {
				schritte = append(schritte, "zustellung über dovecot — damit greift die quota")
			}
		}
	} else {
		schritte = append(schritte, "dovecot fehlt — die postfächer sind nicht abrufbar")
	}

	// Die Milter — nur die, die wirklich dastehen. Postfix mit einem Dienst
	// sprechen zu lassen, den es nicht gibt, kostet bei jeder Mail erst einen
	// Zeitfehler; milter_default_action=accept fängt es ab, aber langsam.
	var milter []string
	if dirExists(opendkimDir) {
		milter = append(milter, "inet:localhost:8891")
		schritte = append(schritte, "opendkim als milter")
	} else {
		schritte = append(schritte, "opendkim fehlt — ohne dkim landet post häufiger im spam")
	}
	if dirExists(rspamdDir) {
		// Rspamd hängt hinter OpenDKIM: erst unterschreiben, dann bewerten.
		// Andersherum bewertete es eine Mail, die noch keine Unterschrift hat.
		milter = append(milter, "inet:localhost:11332")
		schritte = append(schritte, "rspamd als milter")
	}
	if len(milter) > 0 {
		liste := strings.Join(milter, " ")
		for _, e := range [][2]string{
			{"smtpd_milters", liste},
			{"non_smtpd_milters", liste},
			// Fällt ein Milter aus, geht die Mail unsigniert und ungeprüft
			// raus statt gar nicht. Unsigniert ist ein Nachteil bei der
			// Zustellung; nicht zugestellt ist ein Ausfall.
			{"milter_default_action", "accept"},
			{"milter_protocol", "6"},
		} {
			if out, err := run(ctx, shortTimeout, "postconf", "-e", e[0]+"="+e[1]); err != nil {
				return nil, opErr(OpMailSetup, "postconf %s: %s", e[0], truncate(out, 200))
			}
		}
	}

	// Leere Maps anlegen, damit Postfix beim Neuladen nicht über eine fehlende
	// Datei stolpert. Der erste echte Stand kommt mit mail.apply.
	leer := MailApplyParams{}
	if raw, err := json.Marshal(leer); err == nil {
		if _, err := s.opMailApply(ctx, raw); err != nil {
			schritte = append(schritte, "die leeren maps konnten noch nicht geschrieben werden")
		} else {
			schritte = append(schritte, "leere maps geschrieben")
		}
	}

	schritte = append(schritte, s.openMailPorts(ctx))

	return TextResult{Text: strings.Join(schritte, "; ")}, nil
}

// MailFacts ist, was nur der Server über sich selbst weiß.
//
// Getrennt von der Bewertung: der Agent sagt, was ist — welche Adressen, was
// horcht, was in der Postfix-Konfiguration steht. Ob das gut ist, entscheidet
// das Panel. Zwei Gründe: die Bewertung braucht DNS-Auskünfte, die der Agent
// nicht einholen soll, und eine Beurteilung im Agent wäre eine, die sich nur
// mit einem neuen Agent ändern lässt.
type MailFacts struct {
	// Hostname ist myhostname aus der Postfix-Konfiguration. Er steht im
	// HELO und ist der Name, für den der PTR-Eintrag passen muss.
	Hostname string `json:"hostname"`
	// PublicIPs sind die Adressen, unter denen dieser Server von außen zu
	// sehen ist — soweit sie an einer Schnittstelle hängen. Hinter NAT bleibt
	// die Liste leer, und dann kann das Panel den PTR-Eintrag nicht prüfen.
	PublicIPs []string `json:"public_ips"`
	// Listening sind die Ports aus mailPorts, auf denen wirklich etwas
	// antwortet.
	Listening []int `json:"listening"`
	// TLSCert ist der Pfad, den Postfix benutzt — leer heißt unverschlüsselt.
	TLSCert string `json:"tls_cert"`
	// RelayRestrictions ist die Zeile im Wortlaut. Ohne
	// reject_unauth_destination darin ist der Server ein offenes Relay.
	RelayRestrictions string `json:"relay_restrictions"`
	// DKIMDomains sind die Domänen, für die eine Schlüsseldatei dasteht.
	DKIMDomains []string `json:"dkim_domains"`
}

// opMailFacts sammelt die Tatsachen ein.
func (s *Server) opMailFacts(ctx context.Context, _ json.RawMessage) (any, error) {
	res := MailFacts{PublicIPs: []string{}, Listening: []int{}, DKIMDomains: []string{}}

	if out, err := run(ctx, shortTimeout, "postconf", "-h", "myhostname"); err == nil {
		res.Hostname = strings.TrimSpace(out)
	}
	if out, err := run(ctx, shortTimeout, "postconf", "-h", "smtpd_tls_cert_file"); err == nil {
		res.TLSCert = strings.TrimSpace(out)
	}
	if out, err := run(ctx, shortTimeout, "postconf", "-h", "smtpd_relay_restrictions"); err == nil {
		res.RelayRestrictions = strings.TrimSpace(out)
	}

	res.PublicIPs = oeffentlicheAdressen()

	// Horcht da etwas? Eine Verbindung nach 127.0.0.1 beantwortet das ohne
	// zusätzliches Werkzeug: ein Dienst auf 0.0.0.0 nimmt sie an.
	for _, p := range []int{25, 587, 993} {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(p)),
			2*time.Second)
		if err == nil {
			_ = conn.Close()
			res.Listening = append(res.Listening, p)
		}
	}

	if eintraege, err := os.ReadDir(filepath.Join(opendkimDir, "keys")); err == nil {
		for _, e := range eintraege {
			if e.IsDir() && reMailDomainTeil.MatchString(e.Name()) {
				res.DKIMDomains = append(res.DKIMDomains, e.Name())
			}
		}
	}
	return res, nil
}

// oeffentlicheAdressen sind die Adressen, unter denen der Server von außen zu
// sehen sein könnte.
//
// Private und Loopback-Adressen fallen weg: für einen PTR-Eintrag taugen sie
// nicht. Bleibt nichts übrig, steht der Server hinter NAT — dann lässt sich
// von hier aus nicht sagen, welche Adresse die Welt sieht, und das ist eine
// ehrlichere Auskunft als eine geratene.
func oeffentlicheAdressen() []string {
	out := []string{}
	adressen, err := net.InterfaceAddrs()
	if err != nil {
		return out
	}
	for _, a := range adressen {
		ipnet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		addr, ok := netip.AddrFromSlice(ipnet.IP)
		if !ok {
			continue
		}
		addr = addr.Unmap()
		if !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLinkLocalUnicast() {
			continue
		}
		out = append(out, addr.String())
	}
	return out
}
