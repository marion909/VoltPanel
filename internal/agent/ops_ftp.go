package agent

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Pure-FTPd liest seine Einstellungen auf Debian und Ubuntu aus einzelnen
// Dateien unter /etc/pure-ftpd/conf: der Dateiname ist die Option, ihr Inhalt
// der Wert. Deshalb steht hier kein Template, sondern eine Tabelle — es gibt
// keine Datei, in die man ein Template rendern könnte.
//
// Von Hand bearbeitet wird trotzdem nichts: der Agent schreibt genau diese
// Werte, jeder mit dem Grund daneben. Was hier nicht steht, gilt nicht.
const (
	ftpConfDir = "/etc/pure-ftpd/conf"
	ftpDBPath  = "/etc/pure-ftpd/pureftpd.passwd"
	ftpTLSPath = "/etc/ssl/private/pure-ftpd.pem"

	// Diese Datei liest der Startwrapper des Debian-Pakets, bevor er
	// pure-ftpd aufruft. Sie steht außerhalb von ftpConfDir und damit
	// außerhalb der Tabelle unten.
	ftpDefaultsPath = "/etc/default/pure-ftpd-common"

	// Der passive Bereich muss in der Firewall offen sein. Hundert Ports
	// reichen für hundert gleichzeitige Übertragungen — mehr als
	// MaxClientsNumber zulässt.
	ftpPassiveFrom = 30000
	ftpPassiveTo   = 30100
)

// ftpDefaults: eigener Dienst statt inetd, und kein Chroot in ein Verzeichnis,
// das dem Kunden gehört. VIRTUALCHROOT würde die Sperre in das Heimatverzeichnis
// selbst verlegen, wo ein Symlink sie aufmachen kann; ohne die Angabe sperrt
// pure-ftpd über chroot(2), und daran ändert kein Symlink etwas.
const ftpDefaults = `# Von VoltPanel geschrieben. Änderungen gehen beim nächsten Einrichten verloren.
STANDALONE_OR_INETD=standalone
VIRTUALCHROOT=false
`

// reFTPName wiederholt die Prüfung aus dem Store. Der Name wird eine Zeile in
// der PureDB, in der der Doppelpunkt das Trennzeichen ist.
var reFTPName = regexp.MustCompile(`^[a-z][a-z0-9_]{2,31}$`)

// reFTPPassword schließt aus, was die PureDB oder die Übergabe über stdin
// zerlegen könnte. Der Doppelpunkt ist dort ein Feldtrenner, der Zeilenumbruch
// beendet die Eingabe.
var reFTPPassword = regexp.MustCompile(`^[A-Za-z0-9!#%()*+,\-./;<=>?@^_{|}~]{12,128}$`)

// ftpValueKind ist die Regel, nach der der Debian-Wrapper den Inhalt einer
// dieser Dateien liest — dieselbe Einteilung, die er in seiner eigenen Tabelle
// trifft (debian/pure-ftpd-wrapper, %conf).
//
// Sie steht hier, weil der Wrapper einen Wert, den er nicht versteht, nicht
// etwa überspringt, sondern daran stirbt:
//
//	die "Invalid configuration file /etc/pure-ftpd/conf/Umask: \"137:027\" not two octal numbers"
//
// Ein Tippfehler in der Tabelle unten ist damit kein schiefer Wert, sondern ein
// Dienst, der nicht mehr startet. Genau das ist hier zweimal passiert: Umask
// und PassivePortRange standen mit Doppelpunkt statt mit Leerzeichen.
type ftpValueKind int

const (
	ftpYesNo      ftpValueKind = iota // parse_yesno:     yes/no/on/off/0/1
	ftpNumber                         // parse_number_1:  eine Zahl
	ftpTwoNumbers                     // parse_number_2:  zwei Zahlen, Leerzeichen dazwischen
	ftpUmask                          // parse_umask:     zwei dreistellige Oktalzahlen, Leerzeichen dazwischen
	ftpFilename                       // parse_filename:  die Datei muss existieren
)

// ftpValueRules bildet die Prüfungen des Wrappers nach. Die Ausdrücke sind
// seine, nur in Go-Schreibweise.
var ftpValueRules = map[ftpValueKind]*regexp.Regexp{
	ftpYesNo:      regexp.MustCompile(`^(?i:yes|no|on|off|0|1)$`),
	ftpNumber:     regexp.MustCompile(`^\d+$`),
	ftpTwoNumbers: regexp.MustCompile(`^\d+\s+\d+$`),
	ftpUmask:      regexp.MustCompile(`^[0-7]{3}\s+[0-7]{3}$`),
}

// reFTPOptionName ist die Bedingung, unter der der Wrapper eine Datei
// überhaupt ansieht: `next unless /^[A-Za-z][A-Za-z0-9]+$/`.
//
// Was hier nicht passt, wird stillschweigend übergangen — und das ist die
// unangenehmere Hälfte: ein Dienst, der nicht startet, fällt auf. Ein
// verschluckter Dateiname namens "Chroot-Everyone" fiele niemandem auf, und
// jeder FTP-Zugang stünde ohne Sperre im Dateisystem aller anderen.
var reFTPOptionName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]+$`)

type ftpSetting struct {
	Value string
	Kind  ftpValueKind
}

func ftpSettings() map[string]ftpSetting {
	return map[string]ftpSetting{
		// Die virtuellen Zugänge liegen in der PureDB, nicht in /etc/passwd.
		// parse_filename heißt: die übersetzte Datenbank muss dastehen, bevor
		// der Dienst startet — sonst stirbt der Wrapper an dieser Zeile.
		"PureDB": {ftpDBPath + ".pdb", ftpFilename},

		// Jeder Zugang sitzt in seinem Heimatverzeichnis fest. Ohne das käme
		// ein Kunde über .. in die Verzeichnisse aller anderen.
		"ChrootEveryone": {"yes", ftpYesNo},
		"NoAnonymous":    {"yes", ftpYesNo},

		// Unter 1000 liegen die Systemkonten. Selbst wenn ein Eintrag mit
		// falscher UID in die PureDB geriete, würde er hier abgewiesen.
		"MinUID": {"1000", ftpNumber},

		// Verschlüsselung ist Pflicht, nicht Angebot. FTP ohne TLS schickt das
		// Passwort im Klartext über die Leitung — bei einem Panel, das sonst
		// jede Kleinigkeit absichert, wäre das ein Widerspruch.
		"TLS": {"2", ftpNumber},

		// Dieselben Rechte, die auch das Panel auf den Site-Verzeichnissen
		// setzt: Dateien 640, Verzeichnisse 750. Die Gruppe ist über das
		// setgid-Bit die des Webservers, der die Dateien lesen können muss;
		// alle anderen sehen nichts.
		//
		// Leerzeichen, kein Doppelpunkt: der Wrapper liest hier zwei Zahlen
		// und setzt selbst `-U %s:%s` daraus zusammen.
		"Umask": {"137 027", ftpUmask},

		"MaxClientsNumber": {"50", ftpNumber},
		"MaxClientsPerIP":  {"8", ftpNumber},

		// Ebenfalls zwei Zahlen mit Leerzeichen — `-p %d:%d` baut der Wrapper.
		"PassivePortRange": {fmt.Sprintf("%d %d", ftpPassiveFrom, ftpPassiveTo), ftpTwoNumbers},

		// Kein Rückwärts-Auflösen der Adresse je Verbindung: das kostet bei
		// einem trägen Resolver Sekunden und steht in keinem Log, das jemand
		// liest.
		"DontResolve": {"yes", ftpYesNo},

		// Ein FTP-Client, der nichts mehr tut, belegt sonst dauerhaft einen
		// der Plätze oben.
		"MaxIdleTime": {"15", ftpNumber},
	}
}

// checkFTPSettings prüft die Tabelle gegen die Regeln des Wrappers, bevor
// irgendetwas geschrieben wird.
//
// Ein halb geschriebenes Konfigurationsverzeichnis wäre der schlechtere
// Zustand: die alten Dateien wären schon ersetzt, die neuen unvollständig.
//
// Die Existenz der PureDB wird hier nicht geprüft — die entsteht erst weiter
// unten. Dafür ist checkFTPFiles da.
func checkFTPSettings() error {
	for name, set := range ftpSettings() {
		if !reFTPOptionName.MatchString(name) {
			return fmt.Errorf("%q ist kein name, den der wrapper ansieht", name)
		}
		re, ok := ftpValueRules[set.Kind]
		if !ok {
			continue // ftpFilename, siehe checkFTPFiles
		}
		if !re.MatchString(set.Value) {
			return fmt.Errorf("%s=%q passt nicht zu dem, was pure-ftpd dort liest", name, set.Value)
		}
	}
	return nil
}

// checkFTPFiles prüft, was der Wrapper als Datei erwartet. Fehlt sie, stirbt er
// mit "No such file" — und der Dienst startet nie.
func checkFTPFiles() error {
	for name, set := range ftpSettings() {
		if set.Kind == ftpFilename && !fileExists(set.Value) {
			return fmt.Errorf("%s: %s fehlt", name, set.Value)
		}
	}
	return nil
}

// opFTPSetup richtet Pure-FTPd ein: Paket, Einstellungen, Zertifikat, Dienst.
//
// Die Operation nimmt keine Parameter. Alles, was sie schreibt, steht in der
// Tabelle oben oder kommt aus der Konfiguration des Agents — der Web-Prozess
// kann also weder eine Option noch einen Pfad bestimmen.
func (s *Server) opFTPSetup(ctx context.Context, _ json.RawMessage) (any, error) {
	var installHinweis string
	if !fileExists("/usr/sbin/pure-ftpd") {
		if out, err := s.aptInstall(ctx, "pure-ftpd"); err != nil {
			// apt meldet auch dann einen Fehler, wenn nur das Postinstall
			// eines abhängigen Pakets gescheitert ist. pure-ftpd hängt an
			// openbsd-inetd, und das braucht VoltPanel nicht — Pure-FTPd läuft
			// hier als eigener Dienst. Ist das gewünschte Paket also heil,
			// wird weitergemacht statt abgebrochen.
			if !packageInstalled(ctx, "pure-ftpd") {
				return nil, opErr(OpFTPSetup, "pure-ftpd installieren: %s", aptMessage(out))
			}
			installHinweis = "Ein mitinstalliertes Paket meldete einen Fehler; " +
				"pure-ftpd selbst ist vollständig eingerichtet. Meldung: " + aptMessage(out)
			s.log.Warn("paketinstallation mit fehler, pure-ftpd trotzdem vollständig",
				"apt", aptMessage(out))
		}
	}

	// Das Paket zieht openbsd-inetd mit. Bleibt der Dienst auf inetd gestellt,
	// beendet sich die Unit sofort wieder und Port 21 hängt an einem Dienst,
	// der von den Einstellungen unten nichts weiß.
	if err := os.WriteFile(ftpDefaultsPath, []byte(ftpDefaults), 0o644); err != nil {
		return nil, opErr(OpFTPSetup, "betriebsart setzen: %v", err)
	}

	// Erst prüfen, dann schreiben: ein halb ersetztes
	// Konfigurationsverzeichnis wäre der schlechtere Zustand.
	if err := checkFTPSettings(); err != nil {
		return nil, opErr(OpFTPSetup, "eigene einstellungen sind fehlerhaft: %v", err)
	}
	if err := os.MkdirAll(ftpConfDir, 0o755); err != nil {
		return nil, opErr(OpFTPSetup, "konfigurationsverzeichnis: %v", err)
	}
	for name, set := range ftpSettings() {
		path := filepath.Join(ftpConfDir, name)
		if err := os.WriteFile(path, []byte(set.Value+"\n"), 0o644); err != nil {
			return nil, opErr(OpFTPSetup, "%s schreiben: %v", name, err)
		}
	}

	if err := s.writeFTPCert(); err != nil {
		return nil, opErr(OpFTPSetup, "%v", err)
	}

	// Eine leere PureDB, damit der Dienst startet, bevor der erste Zugang
	// angelegt ist. Ohne sie beendet sich pure-ftpd mit einem Fehler.
	if !fileExists(ftpDBPath) {
		if err := os.WriteFile(ftpDBPath, nil, 0o600); err != nil {
			return nil, opErr(OpFTPSetup, "puredb anlegen: %v", err)
		}
	}
	if out, err := run(ctx, shortTimeout, "pure-pw", "mkdb", ftpDBPath+".pdb",
		"-f", ftpDBPath); err != nil {
		return nil, opErr(OpFTPSetup, "puredb übersetzen: %s", truncate(out, 300))
	}

	// Was der Wrapper als Datei erwartet, muss jetzt dastehen. Fehlt sie,
	// stirbt er beim Lesen der Konfiguration, und die Meldung des Dienstes
	// wäre "No such file" ohne Hinweis darauf, wer die Datei anlegt.
	if err := checkFTPFiles(); err != nil {
		return nil, opErr(OpFTPSetup, "%v", err)
	}

	// enable ohne --now: der Start kommt gleich darunter, und ein Fehlschlag
	// soll die Meldung des Starts tragen, nicht die des Einrichtens.
	if out, err := run(ctx, longTimeout, "systemctl", "enable", "pure-ftpd"); err != nil {
		return nil, opErr(OpFTPSetup, "dienst eintragen: %s", truncate(out, 300))
	}
	if err := s.startService(ctx, OpFTPSetup, "pure-ftpd", "restart"); err != nil {
		return nil, err
	}

	res := FTPSetupResult{
		Ready:        true,
		Installed:    true,
		Active:       true,
		PassiveFrom:  ftpPassiveFrom,
		PassiveTo:    ftpPassiveTo,
		FirewallHint: s.openFTPPorts(ctx),
		TLSCert:      ftpTLSPath,
		Notice:       installHinweis,
	}
	return res, nil
}

// writeFTPCert legt das Zertifikat ab, das Pure-FTPd erwartet: Schlüssel und
// Kette in einer Datei.
//
// Genommen wird das des Panels. Es ist bereits da — beim ersten Start entsteht
// mindestens ein selbstsigniertes —, und wer später ein echtes für die
// Panel-Domain holt, bekommt es hier mit dem nächsten Einrichten ebenfalls.
func (s *Server) writeFTPCert() error {
	var letzter error
	for _, pair := range s.panelCertChain() {
		key, err := os.ReadFile(pair.Key)
		if err != nil {
			continue
		}
		cert, err := os.ReadFile(pair.Cert)
		if err != nil {
			continue
		}
		// Prüfen, bevor pure-ftpd es tut. Mit TLS=2 ist Verschlüsselung
		// Pflicht: ein Zertifikat, das nicht aufgeht, ist kein
		// Schönheitsfehler, sondern ein Dienst, der nicht startet — und die
		// Meldung dazu käme aus pure-ftpd, nicht von hier.
		if _, err := tls.X509KeyPair(cert, key); err != nil {
			letzter = fmt.Errorf("%s und %s passen nicht zusammen: %w", pair.Cert, pair.Key, err)
			s.log.Warn("zertifikat für ftp unbrauchbar", "cert", pair.Cert, "err", err)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(ftpTLSPath), 0o710); err != nil {
			return fmt.Errorf("verzeichnis für das ftp-zertifikat: %w", err)
		}
		// 0600: die Datei enthält den privaten Schlüssel. Pure-FTPd liest sie
		// als root, bevor es die Rechte fallen lässt, und weigert sich, wenn
		// Gruppe oder Andere sie lesen dürfen.
		if err := os.WriteFile(ftpTLSPath, joinPEM(key, cert), 0o600); err != nil {
			return fmt.Errorf("ftp-zertifikat schreiben: %w", err)
		}
		return nil
	}
	if letzter != nil {
		return letzter
	}
	return fmt.Errorf("kein lesbares zertifikat gefunden — ohne eines startet pure-ftpd nicht, " +
		"weil verschlüsselung pflicht ist")
}

// joinPEM setzt PEM-Blöcke zu einer Datei zusammen.
//
// Der Zeilenumbruch dazwischen ist der ganze Zweck. Endet der Schlüssel ohne
// einen — jedes von Hand abgelegte Zertifikat kann das —, stünde
// "-----END PRIVATE KEY----------BEGIN CERTIFICATE-----" in einer Zeile, und
// die Datei wäre unlesbar.
func joinPEM(parts ...[]byte) []byte {
	var b bytes.Buffer
	for _, p := range parts {
		p = bytes.TrimRight(p, "\r\n")
		if len(p) == 0 {
			continue
		}
		b.Write(p)
		b.WriteByte('\n')
	}
	return b.Bytes()
}

// openFTPPorts gibt die Ports in ufw frei, sofern ufw überhaupt läuft.
//
// Die Argumente sind fest. Nichts davon kommt aus einer Anfrage, und für
// nftables geschieht bewusst nichts: dort gibt es kein Regelwerk, in das sich
// eine Zeile gefahrlos einfügen ließe.
func (s *Server) openFTPPorts(ctx context.Context) string {
	out, err := run(ctx, shortTimeout, "ufw", "status")
	if err != nil || !strings.Contains(out, "Status: active") {
		return fmt.Sprintf("Port 21 und %d–%d müssen in der Firewall offen sein.",
			ftpPassiveFrom, ftpPassiveTo)
	}

	rules := []string{"21/tcp", fmt.Sprintf("%d:%d/tcp", ftpPassiveFrom, ftpPassiveTo)}
	for _, rule := range rules {
		if out, err := run(ctx, shortTimeout, "ufw", "allow", rule); err != nil {
			s.log.Warn("ufw-regel nicht gesetzt", "regel", rule, "err", err, "out", truncate(out, 200))
			return "Die Firewall-Regeln konnten nicht gesetzt werden — bitte Port 21 und " +
				fmt.Sprintf("%d–%d selbst freigeben.", ftpPassiveFrom, ftpPassiveTo)
		}
	}
	return fmt.Sprintf("ufw: Port 21 und %d–%d sind freigegeben.", ftpPassiveFrom, ftpPassiveTo)
}

// opFTPStatus sagt, ob der Dienst überhaupt einsatzbereit ist.
func (s *Server) opFTPStatus(ctx context.Context, _ json.RawMessage) (any, error) {
	res := FTPSetupResult{
		Installed:   fileExists("/usr/sbin/pure-ftpd"),
		PassiveFrom: ftpPassiveFrom,
		PassiveTo:   ftpPassiveTo,
		TLSCert:     ftpTLSPath,
	}
	if !res.Installed {
		return res, nil
	}

	out, err := run(ctx, shortTimeout, "systemctl", "is-active", "pure-ftpd")
	res.Active = err == nil && strings.TrimSpace(out) == "active"
	res.Ready = res.Active && fileExists(ftpDBPath+".pdb") && fileExists(ftpTLSPath)
	return res, nil
}

// opFTPUserSet legt einen virtuellen Zugang an oder setzt sein Passwort neu.
//
// Virtuell heißt: kein Eintrag in /etc/passwd. Der Zugang bekommt die UID und
// GID des Systembenutzers seiner Site, arbeitet also mit genau den Rechten, die
// dort ohnehin gelten — nicht mehr.
func (s *Server) opFTPUserSet(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[FTPUserParams](raw, OpFTPUserSet)
	if err != nil {
		return nil, err
	}
	if err := checkFTPUser(OpFTPUserSet, p); err != nil {
		return nil, err
	}
	uid, gid, err := siteUserIDs(OpFTPUserSet, p.SystemUser)
	if err != nil {
		return nil, err
	}
	home, err := jail(p.HomeDir, s.roots)
	if err != nil {
		return nil, err
	}

	exists, err := s.ftpUserExists(ctx, p.Username)
	if err != nil {
		return nil, err
	}

	opts := []string{"-f", ftpDBPath, "-u", strconv.Itoa(uid),
		"-g", strconv.Itoa(gid), "-d", home, "-m"}
	if p.QuotaMB > 0 {
		opts = append(opts, "-N", strconv.FormatInt(p.QuotaMB, 10))
	}

	if exists {
		// Bestehender Zugang: erst die Angaben, dann das Passwort. usermod
		// fragt keines ab, passwd tut es.
		if out, err := run(ctx, shortTimeout, "pure-pw",
			append([]string{"usermod", p.Username}, opts...)...); err != nil {
			return nil, opErr(OpFTPUserSet, "zugang ändern: %s", truncate(out, 300))
		}
		if err := s.ftpPassword(ctx, "passwd", p.Username, p.Password, "-f", ftpDBPath, "-m"); err != nil {
			return nil, err
		}
		return FTPUserResult{Username: p.Username, UID: uid, GID: gid, HomeDir: home}, nil
	}

	if err := s.ftpPassword(ctx, "useradd", p.Username, p.Password, opts...); err != nil {
		return nil, err
	}
	return FTPUserResult{Username: p.Username, UID: uid, GID: gid, HomeDir: home, Created: true}, nil
}

// siteUserIDs schlägt UID und GID zum Systembenutzer einer Site nach.
//
// Zwei Schranken: der Name muss der eines Site-Benutzers sein, und die
// aufgelöste UID muss über dem Bereich der Systemkonten liegen. Die zweite
// Prüfung ist nicht überflüssig — sie fängt den Fall ab, dass jemand ein Konto
// namens site_x mit der UID 0 angelegt hat.
func siteUserIDs(op Op, name string) (int, int, error) {
	if err := checkUsername(name); err != nil {
		return 0, 0, err
	}
	if !strings.HasPrefix(name, sitePrefix) {
		return 0, 0, opInputErr(op, "%q ist kein systembenutzer einer site", name)
	}
	u, err := user.Lookup(name)
	if err != nil {
		return 0, 0, opInputErr(op, "den systembenutzer %q gibt es nicht", name)
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return 0, 0, opErr(op, "uid von %q: %v", name, err)
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return 0, 0, opErr(op, "gid von %q: %v", name, err)
	}
	if uid < 1000 || gid < 1000 {
		return 0, 0, opInputErr(op, "%q hat die uid %d — ein ftp-zugang darf nicht auf einem "+
			"systemkonto laufen", name, uid)
	}
	return uid, gid, nil
}

// ftpPassword ruft pure-pw auf und schiebt das Passwort über die Standardeingabe
// nach.
//
// Nicht als Argument: Argumente stehen in der Prozessliste, jeder Benutzer des
// Servers könnte das Passwort dort mitlesen. pure-pw fragt zweimal danach,
// deshalb steht es zweimal im Strom.
func (s *Server) ftpPassword(ctx context.Context, action, username, password string, extra ...string) error {
	args := append([]string{action, username}, extra...)
	stdin := strings.NewReader(password + "\n" + password + "\n")
	if err := runInto(ctx, shortTimeout, nil, stdin, "pure-pw", args...); err != nil {
		return opErr(OpFTPUserSet, "pure-pw %s: %v", action, err)
	}
	return nil
}

// panelCertChain sind die Zertifikatspaare des Panels in absteigender Güte —
// dieselbe Reihenfolge, die auch der Webserver benutzt.
//
// Die Pfade entstehen aus der Konfiguration des Agents, nicht aus einer
// Anfrage: sonst wäre "richte FTP ein" ein Weg, eine beliebige Datei des
// Servers nach /etc/ssl/private zu kopieren.
func (s *Server) panelCertChain() []struct{ Cert, Key string } {
	var chain []struct{ Cert, Key string }
	add := func(dir string) {
		chain = append(chain, struct{ Cert, Key string }{
			Cert: filepath.Join(dir, "fullchain.pem"),
			Key:  filepath.Join(dir, "privkey.pem"),
		})
	}
	if s.panelDomain != "" {
		add(filepath.Join(s.certDir, s.panelDomain))
	}
	add(filepath.Join(s.certDir, "panel"))
	return chain
}

func (s *Server) ftpUserExists(ctx context.Context, username string) (bool, error) {
	out, err := run(ctx, shortTimeout, "pure-pw", "list", "-f", ftpDBPath)
	if err != nil {
		// Eine noch nicht angelegte Datenbank ist kein Fehler, sondern der
		// Zustand vor dem ersten Zugang.
		if !fileExists(ftpDBPath) {
			return false, nil
		}
		return false, opErr(OpFTPUserSet, "zugänge lesen: %s", truncate(out, 300))
	}
	for _, line := range strings.Split(out, "\n") {
		name, _, _ := strings.Cut(strings.TrimSpace(line), " ")
		if name == username {
			return true, nil
		}
	}
	return false, nil
}

// opFTPUserDelete entfernt einen Zugang aus der PureDB. Die Dateien der Site
// bleiben unangetastet — sie gehören der Site, nicht dem Zugang.
func (s *Server) opFTPUserDelete(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[FTPUserParams](raw, OpFTPUserDelete)
	if err != nil {
		return nil, err
	}
	if !reFTPName.MatchString(p.Username) {
		return nil, opInputErr(OpFTPUserDelete, "%q ist kein gültiger ftp-benutzername", p.Username)
	}

	exists, err := s.ftpUserExists(ctx, p.Username)
	if err != nil {
		return nil, err
	}
	if !exists {
		// Schon weg ist das Ziel, nicht ein Fehler.
		return TextResult{Text: "ftp-zugang " + p.Username + " war nicht vorhanden"}, nil
	}
	if out, err := run(ctx, shortTimeout, "pure-pw", "userdel", p.Username,
		"-f", ftpDBPath, "-m"); err != nil {
		return nil, opErr(OpFTPUserDelete, "%s", truncate(out, 300))
	}
	return TextResult{Text: "ftp-zugang " + p.Username + " entfernt"}, nil
}

// opFTPUserList liest, was der Dienst wirklich kennt — nicht, was das Panel
// glaubt. Der Unterschied ist der Punkt: so lässt sich eine Abweichung sehen.
func (s *Server) opFTPUserList(ctx context.Context, _ json.RawMessage) (any, error) {
	if !fileExists(ftpDBPath) {
		return []string{}, nil
	}
	out, err := run(ctx, shortTimeout, "pure-pw", "list", "-f", ftpDBPath)
	if err != nil {
		return nil, opErr(OpFTPUserList, "%s", truncate(out, 300))
	}

	names := []string{}
	for _, line := range strings.Split(out, "\n") {
		name, _, _ := strings.Cut(strings.TrimSpace(line), " ")
		if reFTPName.MatchString(name) {
			names = append(names, name)
		}
	}
	return names, nil
}

func checkFTPUser(op Op, p FTPUserParams) error {
	if !reFTPName.MatchString(p.Username) {
		return opInputErr(op, "%q ist kein gültiger ftp-benutzername — 3 bis 32 zeichen, "+
			"kleinbuchstaben, ziffern und unterstrich", p.Username)
	}
	if !reFTPPassword.MatchString(p.Password) {
		return opInputErr(op, "das passwort muss 12 bis 128 zeichen lang sein und darf keinen "+
			"doppelpunkt, kein anführungszeichen und keinen zeilenumbruch enthalten")
	}
	if p.QuotaMB < 0 {
		return opInputErr(op, "die quota kann nicht negativ sein")
	}
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
