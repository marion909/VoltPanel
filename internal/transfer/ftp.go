package transfer

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
)

// FTP als Ablageort für Archive.
//
// Ein eigener kleiner Client statt einer Bibliothek: gebraucht werden Anmelden,
// Verzeichnis anlegen, eine Datei hochladen. Das sind sieben Kommandos, und der
// Rest des Protokolls — ASCII-Modus, Wiederaufnahme, Umbenennen, Auflisten —
// käme nie zum Einsatz.
//
// Der wichtigste Unterschied zum eigenen FTP-Dienst des Panels: dort ist
// Verschlüsselung Pflicht, weil VoltPanel den Server stellt. Hier stellt ihn
// jemand anderes. TLS bleibt die Voreinstellung, lässt sich aber abschalten —
// mit einem Satz in der Oberfläche, der sagt, was das bedeutet.

const (
	ftpDialTimeout = 20 * time.Second
	ftpLineTimeout = 60 * time.Second
	// Eine Antwortzeile von einem FTP-Server ist kurz. Alles darüber ist
	// entweder kaputt oder ein Versuch, den Speicher vollzuschreiben.
	ftpMaxLine = 4096
)

// FTPConfig beschreibt einen Ablageort.
type FTPConfig struct {
	Host string
	Port int
	User string
	Pass string
	// BaseDir ist das Verzeichnis auf dem fremden Server, unter dem die
	// Archive liegen. Leer heisst: das Heimatverzeichnis des Zugangs.
	BaseDir string
	// TLS verlangt AUTH TLS vor der Anmeldung. Voreinstellung ist an.
	TLS bool
	// InsecureSkipVerify lässt ein selbstsigniertes Zertifikat durch. Viele
	// kleine FTP-Server haben nichts anderes; ohne diese Möglichkeit bliebe
	// nur, TLS ganz abzuschalten — und das wäre schlechter.
	InsecureSkipVerify bool
}

// ftpConn ist eine offene Steuerverbindung.
type ftpConn struct {
	conn net.Conn
	r    *bufio.Reader
	cfg  FTPConfig
	tls  *tls.Config
}

// PutFileFTP lädt eine Datei hoch und gibt den Pfad zurück, unter dem sie liegt.
func PutFileFTP(ctx context.Context, cfg FTPConfig, localPath, name string) (string, error) {
	if err := validateFTP(cfg); err != nil {
		return "", err
	}

	f, err := os.Open(localPath)
	if err != nil {
		return "", fmt.Errorf("archiv öffnen: %w", err)
	}
	defer f.Close()

	c, err := ftpDial(ctx, cfg)
	if err != nil {
		return "", err
	}
	defer c.quit()

	target := path.Join(cfg.BaseDir, name)
	if dir := path.Dir(target); dir != "." && dir != "/" {
		if err := c.makeDirs(dir); err != nil {
			return "", err
		}
	}

	if err := c.store(ctx, target, f); err != nil {
		return "", err
	}
	return target, nil
}

// ProbeFTP prüft Erreichbarkeit und Zugangsdaten, ohne etwas zu schreiben.
func ProbeFTP(ctx context.Context, cfg FTPConfig) error {
	if err := validateFTP(cfg); err != nil {
		return err
	}
	c, err := ftpDial(ctx, cfg)
	if err != nil {
		return err
	}
	defer c.quit()

	// PWD beantwortet, ob die Anmeldung wirklich durch ist — manche Server
	// nehmen PASS an und verweigern danach alles.
	_, _, err = c.cmd("PWD")
	return err
}

func ftpDial(ctx context.Context, cfg FTPConfig) (*ftpConn, error) {
	address := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	conn, err := SafeDialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("verbindung zu %s: %w", address, err)
	}

	c := &ftpConn{
		conn: conn,
		r:    bufio.NewReaderSize(conn, ftpMaxLine),
		cfg:  cfg,
		tls: &tls.Config{
			ServerName:         cfg.Host,
			InsecureSkipVerify: cfg.InsecureSkipVerify, //nolint:gosec // bewusst, siehe FTPConfig
			MinVersion:         tls.VersionTLS12,
		},
	}

	// Die Begrüssung kommt unaufgefordert.
	if _, _, err := c.expect(); err != nil {
		conn.Close()
		return nil, err
	}

	if cfg.TLS {
		if err := c.startTLS(); err != nil {
			conn.Close()
			return nil, err
		}
	}

	if code, msg, err := c.cmd("USER " + cfg.User); err != nil {
		conn.Close()
		return nil, err
	} else if code == 331 || code == 332 {
		if _, _, err := c.cmd("PASS " + cfg.Pass); err != nil {
			conn.Close()
			return nil, fmt.Errorf("anmeldung abgelehnt: %w", err)
		}
	} else if code >= 300 {
		conn.Close()
		return nil, fmt.Errorf("anmeldung: %d %s", code, msg)
	}

	if _, _, err := c.cmd("TYPE I"); err != nil {
		conn.Close()
		return nil, err
	}
	return c, nil
}

// startTLS hebt die Steuerverbindung auf TLS und schaltet auch die
// Datenverbindung auf verschlüsselt.
//
// PBSZ 0 und PROT P gehören zusammen und müssen in dieser Reihenfolge kommen.
// Ohne sie liefe die Steuerverbindung verschlüsselt und die Datei daneben im
// Klartext — der Fehler, den man nicht bemerkt, weil alles funktioniert.
func (c *ftpConn) startTLS() error {
	if _, _, err := c.cmd("AUTH TLS"); err != nil {
		return fmt.Errorf("der server bietet kein tls an (%w) — entweder auf dem "+
			"server einschalten oder die verschlüsselung hier bewusst abwählen", err)
	}

	tlsConn := tls.Client(c.conn, c.tls)
	if err := tlsConn.Handshake(); err != nil {
		return fmt.Errorf("tls-handschlag: %w", err)
	}
	c.conn = tlsConn
	c.r = bufio.NewReaderSize(tlsConn, ftpMaxLine)

	if _, _, err := c.cmd("PBSZ 0"); err != nil {
		return err
	}
	if _, _, err := c.cmd("PROT P"); err != nil {
		return fmt.Errorf("der server verschlüsselt die datenverbindung nicht: %w", err)
	}
	return nil
}

// makeDirs legt den Pfad an, Ebene für Ebene.
//
// FTP kennt kein "mkdir -p". Ein bereits vorhandenes Verzeichnis ist kein
// Fehler, sondern der Normalfall ab dem zweiten Backup — deshalb wird die
// Antwort auf MKD bewusst nicht geprüft.
func (c *ftpConn) makeDirs(dir string) error {
	parts := strings.Split(strings.Trim(dir, "/"), "/")
	current := ""
	if strings.HasPrefix(dir, "/") {
		current = "/"
	}
	for _, part := range parts {
		if part == "" {
			continue
		}
		current = path.Join(current, part)
		_, _, _ = c.cmd("MKD " + current)
	}

	if _, _, err := c.cmd("CWD " + dir); err != nil {
		return fmt.Errorf("das verzeichnis %s liess sich nicht anlegen oder betreten: %w", dir, err)
	}
	return nil
}

// store lädt die Datei über eine passive Datenverbindung hoch.
func (c *ftpConn) store(ctx context.Context, target string, src io.Reader) error {
	host, port, err := c.passive()
	if err != nil {
		return err
	}

	data, err := SafeDialer.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return fmt.Errorf("datenverbindung: %w", err)
	}
	defer data.Close()

	if c.cfg.TLS {
		// Dieselbe Prüfung wie auf der Steuerverbindung. Ein Server, der die
		// Daten auf einen anderen Host umleitet, käme damit nicht durch.
		tlsData := tls.Client(data, c.tls)
		if err := tlsData.Handshake(); err != nil {
			return fmt.Errorf("tls auf der datenverbindung: %w", err)
		}
		data = tlsData
	}

	// STOR erst nach der offenen Datenverbindung: der Server antwortet mit
	// 150 und beginnt zu lesen.
	if code, msg, err := c.cmd("STOR " + path.Base(target)); err != nil {
		return err
	} else if code >= 300 {
		return fmt.Errorf("hochladen abgelehnt: %d %s", code, msg)
	}

	if _, err := io.Copy(data, src); err != nil {
		return fmt.Errorf("übertragen: %w", err)
	}
	// Erst schliessen, dann die Abschlussmeldung lesen — der Server schickt
	// sie, wenn die Datenverbindung zu ist.
	if err := data.Close(); err != nil {
		return err
	}
	if code, msg, err := c.expect(); err != nil {
		return err
	} else if code >= 300 {
		return fmt.Errorf("der server hat die datei nicht angenommen: %d %s", code, msg)
	}
	return nil
}

// passive erfragt Host und Port für die Datenverbindung.
//
// EPSV zuerst: es funktioniert auch über IPv6 und liefert nur einen Port, den
// der Server nicht falsch aus einer NAT-Adresse ableiten kann. PASV bleibt als
// Rückfall für ältere Server.
func (c *ftpConn) passive() (string, int, error) {
	if _, msg, err := c.cmd("EPSV"); err == nil {
		if port, ok := parseEPSV(msg); ok {
			return c.remoteHost(), port, nil
		}
	}

	_, msg, err := c.cmd("PASV")
	if err != nil {
		return "", 0, fmt.Errorf("passiver modus: %w", err)
	}
	host, port, ok := parsePASV(msg)
	if !ok {
		return "", 0, fmt.Errorf("die antwort auf PASV war nicht lesbar: %s", truncate(msg, 120))
	}
	return host, port, nil
}

// remoteHost ist der Host der Steuerverbindung.
//
// Bei PASV nennt der Server selbst eine Adresse, und die ist bei einem Server
// hinter NAT regelmäßig falsch. Bei EPSV nennt er keine — dann gilt die, mit
// der ohnehin schon gesprochen wird.
func (c *ftpConn) remoteHost() string {
	host, _, err := net.SplitHostPort(c.conn.RemoteAddr().String())
	if err != nil {
		return c.cfg.Host
	}
	return host
}

// parseEPSV liest "229 Entering Extended Passive Mode (|||50123|)".
func parseEPSV(msg string) (int, bool) {
	open := strings.Index(msg, "(")
	close := strings.LastIndex(msg, ")")
	if open < 0 || close <= open {
		return 0, false
	}
	fields := strings.Split(msg[open+1:close], "|")
	if len(fields) < 4 {
		return 0, false
	}
	port, err := strconv.Atoi(fields[3])
	if err != nil || port < 1 || port > 65535 {
		return 0, false
	}
	return port, true
}

// parsePASV liest "227 Entering Passive Mode (192,168,1,5,196,17)".
func parsePASV(msg string) (string, int, bool) {
	open := strings.LastIndex(msg, "(")
	close := strings.LastIndex(msg, ")")
	if open < 0 || close <= open {
		return "", 0, false
	}
	parts := strings.Split(msg[open+1:close], ",")
	if len(parts) != 6 {
		return "", 0, false
	}
	nums := make([]int, 6)
	for i, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil || n < 0 || n > 255 {
			return "", 0, false
		}
		nums[i] = n
	}
	host := fmt.Sprintf("%d.%d.%d.%d", nums[0], nums[1], nums[2], nums[3])
	return host, nums[4]<<8 | nums[5], true
}

// cmd schickt ein Kommando und liest die Antwort.
//
// Der Zeilenumbruch beendet ein FTP-Kommando. Stünde einer im Argument, wäre
// alles danach ein zweites Kommando auf derselben Verbindung — dieselbe Lücke
// wie eine Command Injection, nur in einem anderen Protokoll. Deshalb wird hier
// abgebrochen und nicht etwa gefiltert.
func (c *ftpConn) cmd(line string) (int, string, error) {
	if strings.ContainsAny(line, "\r\n\x00") {
		return 0, "", fmt.Errorf("das kommando enthält einen zeilenumbruch")
	}
	_ = c.conn.SetDeadline(time.Now().Add(ftpLineTimeout))
	if _, err := io.WriteString(c.conn, line+"\r\n"); err != nil {
		return 0, "", fmt.Errorf("senden: %w", err)
	}
	return c.expect()
}

// expect liest eine Antwort — auch eine mehrzeilige.
//
// Mehrzeilig heisst: die erste Zeile hat einen Bindestrich hinter dem Code, und
// die Antwort endet erst bei einer Zeile mit demselben Code und einem
// Leerzeichen. Wer das übersieht, liest die Fortsetzung als Antwort auf das
// nächste Kommando und ist ab da um eine Antwort versetzt.
func (c *ftpConn) expect() (int, string, error) {
	_ = c.conn.SetDeadline(time.Now().Add(ftpLineTimeout))

	line, err := c.readLine()
	if err != nil {
		return 0, "", err
	}
	if len(line) < 4 {
		return 0, "", fmt.Errorf("unverständliche antwort: %q", truncate(line, 80))
	}
	code, err := strconv.Atoi(line[:3])
	if err != nil {
		return 0, "", fmt.Errorf("unverständliche antwort: %q", truncate(line, 80))
	}

	msg := line
	if line[3] == '-' {
		prefix := line[:3] + " "
		for {
			next, err := c.readLine()
			if err != nil {
				return 0, "", err
			}
			msg += "\n" + next
			if strings.HasPrefix(next, prefix) {
				break
			}
		}
	}

	if code >= 400 {
		return code, msg, fmt.Errorf("%s", strings.TrimSpace(msg))
	}
	return code, msg, nil
}

func (c *ftpConn) readLine() (string, error) {
	line, err := c.r.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("lesen: %w", err)
	}
	if len(line) > ftpMaxLine {
		return "", fmt.Errorf("die antwort des servers ist unverhältnismässig lang")
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func (c *ftpConn) quit() {
	_, _, _ = c.cmd("QUIT")
	_ = c.conn.Close()
}

func validateFTP(cfg FTPConfig) error {
	switch {
	case cfg.Host == "":
		return fmt.Errorf("der host fehlt")
	case cfg.Port < 1 || cfg.Port > 65535:
		return fmt.Errorf("der port %d liegt ausserhalb des gültigen bereichs", cfg.Port)
	case cfg.User == "":
		return fmt.Errorf("der benutzername fehlt")
	}
	// Alles, was in ein Kommando geht, darf die Zeile nicht beenden.
	for name, v := range map[string]string{
		"host": cfg.Host, "benutzername": cfg.User,
		"passwort": cfg.Pass, "verzeichnis": cfg.BaseDir,
	} {
		if strings.ContainsAny(v, "\r\n\x00") {
			return fmt.Errorf("%s darf keinen zeilenumbruch enthalten", name)
		}
	}
	return nil
}
