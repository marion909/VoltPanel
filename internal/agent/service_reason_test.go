package agent

import (
	"strings"
	"testing"
	"time"
)

// statusAusgabe ist die Form, in der systemctl den Zustand eines
// fehlgeschlagenen Dienstes zeigt. Das meiste davon ist Gerüst.
const statusAusgabe = `● pure-ftpd.service - Pure-FTPd FTP server
     Loaded: loaded (/lib/systemd/system/pure-ftpd.service; enabled; preset: enabled)
     Active: failed (Result: exit-code) since Mon 2026-09-01 07:12:03 CEST; 2s ago
       Docs: man:pure-ftpd(8)
    Process: 5123 ExecStart=/usr/sbin/pure-ftpd-wrapper (code=exited, status=1/FAILURE)
   Main PID: 5123 (code=exited, status=1/FAILURE)
        CPU: 6ms

Sep 01 07:12:03 web01 systemd[1]: Starting pure-ftpd.service - Pure-FTPd FTP server...
Sep 01 07:12:03 web01 pure-ftpd-wrapper[5123]: Invalid port range: 30000 30100
Sep 01 07:12:03 web01 systemd[1]: pure-ftpd.service: Control process exited, code=exited, status=1/FAILURE
Sep 01 07:12:03 web01 systemd[1]: Failed to start pure-ftpd.service - Pure-FTPd FTP server.`

// journalAusgabe ist das, was `journalctl -o short` nach vier Startversuchen
// liefert — und zwar genau so, wie es der Fall war, der diesen Code ausgelöst
// hat: die eine Zeile, die den Fehler nennt, kommt vom Wrapper.
const journalAusgabe = `Sep 01 07:12:01 web01 systemd[1]: Starting pure-ftpd.service - Pure-FTPd FTP server...
Sep 01 07:12:01 web01 pure-ftpd-wrapper[5123]: /usr/sbin/pure-ftpd-wrapper: Invalid configuration file /etc/pure-ftpd/conf/Umask: "137:027" not two octal numbers
Sep 01 07:12:01 web01 systemd[1]: pure-ftpd.service: Control process exited, code=exited, status=1/FAILURE
Sep 01 07:12:01 web01 systemd[1]: pure-ftpd.service: Failed with result 'exit-code'.
Sep 01 07:12:01 web01 systemd[1]: Failed to start pure-ftpd.service.
Sep 01 07:12:02 web01 systemd[1]: pure-ftpd.service: Scheduled restart job, restart counter is at 1.
Sep 01 07:12:02 web01 systemd[1]: Starting pure-ftpd.service - Pure-FTPd FTP server...
Sep 01 07:12:02 web01 pure-ftpd-wrapper[5140]: /usr/sbin/pure-ftpd-wrapper: Invalid configuration file /etc/pure-ftpd/conf/Umask: "137:027" not two octal numbers
Sep 01 07:12:02 web01 systemd[1]: pure-ftpd.service: Failed with result 'exit-code'.
Sep 01 07:12:02 web01 systemd[1]: Failed to start pure-ftpd.service.`

// TestGrundStehtInDerMeldung: systemctl antwortet mit "see journalctl" und
// verweist damit auf einen Befehl, den derjenige, der die Meldung im Panel
// liest, gar nicht ausführen kann — er hat keine Shell auf dem Server.
//
// Was übrig bleiben muss, ist die eine Zeile, die den Fehler benennt.
func TestGrundStehtInDerMeldung(t *testing.T) {
	msg := interestingLines(statusAusgabe)

	if !strings.Contains(msg, "Invalid port range") {
		t.Errorf("der Grund fehlt: %q", msg)
	}
	for _, geruest := range []string{"Loaded:", "Docs:", "CPU:", "Starting pure-ftpd"} {
		if strings.Contains(msg, geruest) {
			t.Errorf("%q steht noch in der Meldung: %q", geruest, msg)
		}
	}
}

// TestDienstZaehltMehrAlsSystemd ist der Fehler, der im Panel ankam: die
// Meldung bestand aus vier Wiederholungen von "Failed with result 'exit-code'"
// und "Failed to start" — Buchhaltung über den Fehlschlag, kein Wort über den
// Grund.
//
// Zwei Ursachen, beide hier festgehalten: `--priority err..warning` ließ die
// Zeile des Dienstes weg (systemd protokolliert dessen Ausgabe mit "info"), und
// die Wiederholungen der Startversuche standen ungekürzt darin.
func TestDienstZaehltMehrAlsSystemd(t *testing.T) {
	msg := interestingLines(journalAusgabe)

	if !strings.Contains(msg, "not two octal numbers") {
		t.Errorf("der Grund fehlt: %q", msg)
	}
	for _, geschwaetz := range []string{
		"Failed with result", "Failed to start", "Starting pure-ftpd",
		"Scheduled restart job", "Control process exited",
	} {
		if strings.Contains(msg, geschwaetz) {
			t.Errorf("%q verdrängt den Grund: %q", geschwaetz, msg)
		}
	}
	if n := strings.Count(msg, "not two octal numbers"); n != 1 {
		t.Errorf("die Zeile steht %d-mal in der Meldung: %q", n, msg)
	}
}

// TestAbbruchcodeBleibtWennDerDienstSchweigt: sagt der Dienst selbst nichts —
// weil er gar nicht erst startete —, ist systemds Notiz alles, was es gibt. Der
// Abbruchcode gehört dann in die Meldung, das "Failed to start" nicht.
func TestAbbruchcodeBleibtWennDerDienstSchweigt(t *testing.T) {
	nurSystemd := `Sep 01 07:12:01 web01 systemd[1]: Starting nginx.service...
Sep 01 07:12:01 web01 systemd[1]: nginx.service: Main process exited, code=exited, status=203/EXEC
Sep 01 07:12:01 web01 systemd[1]: nginx.service: Failed with result 'exit-code'.
Sep 01 07:12:01 web01 systemd[1]: Failed to start nginx.service.`

	msg := interestingLines(nurSystemd)
	if !strings.Contains(msg, "203/EXEC") {
		t.Errorf("der Abbruchcode fehlt: %q", msg)
	}
	if strings.Contains(msg, "Failed to start") {
		t.Errorf("Gerüst statt Grund: %q", msg)
	}
}

// TestKeineMeldungOhneInhalt: eine Ausgabe ohne verwertbare Zeile darf keine
// leeren Trennzeichen hinterlassen — sonst steht im Panel " — ".
func TestKeineMeldungOhneInhalt(t *testing.T) {
	nur := `● nginx.service - A high performance web server
     Loaded: loaded (/lib/systemd/system/nginx.service; enabled)
        CPU: 1ms
`
	if got := interestingLines(nur); got != "" {
		t.Errorf("aus reinem Gerüst wurde %q", got)
	}
	if got := interestingLines("-- No entries --"); got != "" {
		t.Errorf("aus einem leeren Journal wurde %q", got)
	}
	if got := interestingLines(""); got != "" {
		t.Errorf("aus einer leeren Ausgabe wurde %q", got)
	}
}

// TestNurWhitelistDiensteWerdenGelesen: das Journal eines fremden Dienstes kann
// Pfade, Adressen und Fehlermeldungen enthalten, die niemanden im Panel etwas
// angehen. Die Whitelist der Units gilt auch fürs Lesen.
func TestNurWhitelistDiensteWerdenGelesen(t *testing.T) {
	srv, _ := testServer(t)

	for _, unit := range []string{"ssh", "sshd", "systemd-journald", "../etc/passwd", ""} {
		if got := srv.serviceFailure(t.Context(), unit, time.Now()); got != "" {
			t.Errorf("für %q wurde etwas gelesen: %q", unit, got)
		}
	}
}

// TestZeitpunktInJournalSchreibweise: journalctl legt einen Zeitpunkt ohne Zone
// als Ortszeit aus — dieselbe, in der der Agent time.Now() bekommt.
//
// Die Sekunde Vorlauf ist nicht Zierde: journalctl vergleicht auf die Sekunde
// genau, und der erste Eintrag eines Startversuchs trägt denselben Zeitstempel
// wie der Aufruf, der ihn ausgelöst hat.
func TestZeitpunktInJournalSchreibweise(t *testing.T) {
	tp := time.Date(2026, 9, 1, 7, 12, 3, 0, time.Local)
	if got, want := journalSince(tp), "2026-09-01 07:12:02"; got != want {
		t.Errorf("journalSince = %q, erwartet %q", got, want)
	}
	// Ohne Zeitpunkt darf keine leere Angabe herauskommen — journalctl
	// verstünde sie nicht und liefe auf einen Fehler.
	if got := journalSince(time.Time{}); len(got) != len("2006-01-02 15:04:05") {
		t.Errorf("ohne Zeitpunkt kam %q heraus", got)
	}
}

// TestJournalOhnePrioritaetsfilter hält die Entscheidung fest, an der es hing.
//
// systemd protokolliert die Ausgabe eines Dienstes — auch die nach stderr — mit
// der Priorität aus SyslogLevel=, voreingestellt "info". Ein Filter auf
// err..warning lässt damit genau die Zeile weg, wegen der man nachsieht, und
// behält systemds eigenes "Failed with result 'exit-code'". Der Filter stand
// hier, und im Panel kam er als vierfach wiederholte Nichtaussage an.
func TestJournalOhnePrioritaetsfilter(t *testing.T) {
	args := journalArgs("pure-ftpd", time.Now())

	for _, a := range args {
		if strings.HasPrefix(a, "--priority") || strings.HasPrefix(a, "-p") {
			t.Errorf("Prioritätsfilter in %v — die Zeile des Dienstes fällt weg", args)
		}
	}
	// Ohne den Absender je Zeile lässt sich nicht trennen, was der Dienst
	// gesagt hat, von dem, was systemd darüber notiert hat.
	if !strings.Contains(strings.Join(args, " "), "--output short") {
		t.Errorf("ohne Absender je Zeile: %v", args)
	}
}
