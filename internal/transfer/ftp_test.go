package transfer

import (
	"bufio"
	"io"
	"net"
	"strings"
	"testing"
)

// gespraech baut eine ftpConn auf einer Speicher-Verbindung auf und spielt die
// Rolle des Servers. Kein echter Netzverkehr — und damit auch kein Umweg um
// SafeDialer, der 127.0.0.1 zu Recht ablehnt.
func gespraech(t *testing.T, antworten ...string) (*ftpConn, chan string) {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() { client.Close(); server.Close() })

	gesendet := make(chan string, 16)
	go func() {
		r := bufio.NewReader(server)
		for _, antwort := range antworten {
			if _, err := io.WriteString(server, antwort); err != nil {
				return
			}
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			gesendet <- strings.TrimRight(line, "\r\n")
		}
	}()

	return &ftpConn{
		conn: client,
		r:    bufio.NewReaderSize(client, ftpMaxLine),
		cfg:  FTPConfig{Host: "sicherung.example.at", Port: 21},
	}, gesendet
}

// TestMehrzeiligeAntwortWirdGanzGelesen ist der Fehler, den man erst zwei
// Kommandos später bemerkt.
//
// Eine mehrzeilige Antwort beginnt mit "220-" und endet erst bei einer Zeile
// mit demselben Code und einem Leerzeichen. Wer nur die erste Zeile liest,
// hält die Fortsetzung für die Antwort auf das nächste Kommando und ist ab da
// dauerhaft um eine Antwort versetzt — mit Meldungen, die zu nichts passen.
func TestMehrzeiligeAntwortWirdGanzGelesen(t *testing.T) {
	c, _ := gespraech(t,
		"220-Willkommen auf dem Server\r\n220-Bitte nur mit TLS\r\n220 Bereit\r\n")

	code, msg, err := c.expect()
	if err != nil {
		t.Fatalf("expect: %v", err)
	}
	if code != 220 {
		t.Errorf("code = %d, erwartet 220", code)
	}
	if !strings.Contains(msg, "Bereit") {
		t.Errorf("die letzte Zeile fehlt: %q", msg)
	}
	if !strings.Contains(msg, "Bitte nur mit TLS") {
		t.Errorf("die mittlere Zeile fehlt: %q", msg)
	}

	// Und danach ist nichts übrig. Ein zweites expect() hier abzuwarten wäre
	// der falsche Weg — es setzt seine eigene Deadline und der Test liefe eine
	// Minute. Der Puffer selbst ist die genauere Auskunft.
	if n := c.r.Buffered(); n != 0 {
		t.Errorf("nach der mehrzeiligen Antwort liegen noch %d bytes im Puffer — "+
			"die nächste Antwort wäre um eine versetzt", n)
	}
}

// TestKommandoMitZeilenumbruchWirdAbgewiesen: der Zeilenumbruch beendet ein
// FTP-Kommando. Stünde einer in einem Dateinamen, wäre alles danach ein
// zweites Kommando auf derselben Verbindung — dieselbe Lücke wie eine Command
// Injection, nur in einem anderen Protokoll.
func TestKommandoMitZeilenumbruchWirdAbgewiesen(t *testing.T) {
	c, gesendet := gespraech(t, "200 Ok\r\n")

	böse := []string{
		"STOR datei.gz\r\nDELE wichtig.gz",
		"STOR datei.gz\nQUIT",
		"CWD ordner\x00",
	}
	for _, line := range böse {
		if _, _, err := c.cmd(line); err == nil {
			t.Errorf("%q wurde gesendet", line)
		}
	}

	select {
	case s := <-gesendet:
		t.Errorf("es ging trotzdem etwas über die Leitung: %q", s)
	default:
	}
}

// TestPassivModusWirdRichtigGelesen: die beiden Antwortformate sind die Stelle,
// an der ein FTP-Client typischerweise falsch liegt — und ein falscher Port
// verbindet auf einen anderen Dienst desselben Servers.
func TestPassivModusWirdRichtigGelesen(t *testing.T) {
	host, port, ok := parsePASV("227 Entering Passive Mode (192,168,1,5,196,17)")
	if !ok {
		t.Fatal("PASV-Antwort nicht gelesen")
	}
	if host != "192.168.1.5" {
		t.Errorf("host = %q, erwartet 192.168.1.5", host)
	}
	// 196*256 + 17 = 50193
	if port != 50193 {
		t.Errorf("port = %d, erwartet 50193", port)
	}

	if p, ok := parseEPSV("229 Entering Extended Passive Mode (|||50123|)"); !ok || p != 50123 {
		t.Errorf("EPSV: port = %d, ok = %v, erwartet 50123", p, ok)
	}

	// Unsinn muss als Unsinn erkannt werden, nicht als Port 0 oder als Panik.
	kaputt := []string{
		"227 Entering Passive Mode",
		"227 (1,2,3)",
		"227 (1,2,3,4,999,17)",
		"227 (a,b,c,d,e,f)",
		"",
	}
	for _, msg := range kaputt {
		if _, _, ok := parsePASV(msg); ok {
			t.Errorf("%q wurde als PASV-Antwort angenommen", msg)
		}
	}
	for _, msg := range []string{"229 ()", "229 (|||abc|)", "229 (|||99999|)", ""} {
		if _, ok := parseEPSV(msg); ok {
			t.Errorf("%q wurde als EPSV-Antwort angenommen", msg)
		}
	}
}

// TestFehlercodeWirdZumFehler: ab 400 ist die Antwort eine Absage. Ohne diese
// Unterscheidung liefe der Upload weiter, als wäre alles in Ordnung.
func TestFehlercodeWirdZumFehler(t *testing.T) {
	c, _ := gespraech(t, "550 Permission denied\r\n")

	code, _, err := c.expect()
	if err == nil {
		t.Fatal("550 wurde nicht als Fehler gewertet")
	}
	if code != 550 {
		t.Errorf("code = %d, erwartet 550", code)
	}
	if !strings.Contains(err.Error(), "Permission denied") {
		t.Errorf("die Meldung des Servers fehlt: %v", err)
	}
}

// TestFTPKonfigurationWirdGeprueft.
func TestFTPKonfigurationWirdGeprueft(t *testing.T) {
	gut := FTPConfig{Host: "sicherung.example.at", Port: 21, User: "kunde", Pass: "geheim"}
	if err := validateFTP(gut); err != nil {
		t.Fatalf("eine gültige Konfiguration wurde abgelehnt: %v", err)
	}

	schlecht := map[string]FTPConfig{
		"ohne host":      {Port: 21, User: "k"},
		"port 0":         {Host: "h", Port: 0, User: "k"},
		"port zu gross":  {Host: "h", Port: 70000, User: "k"},
		"ohne benutzer":  {Host: "h", Port: 21},
		"umbruch im pw":  {Host: "h", Port: 21, User: "k", Pass: "a\r\nDELE x"},
		"umbruch im dir": {Host: "h", Port: 21, User: "k", BaseDir: "a\nQUIT"},
	}
	for name, cfg := range schlecht {
		if err := validateFTP(cfg); err == nil {
			t.Errorf("%s wurde angenommen", name)
		}
	}
}
