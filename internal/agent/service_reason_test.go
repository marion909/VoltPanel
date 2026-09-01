package agent

import (
	"strings"
	"testing"
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
		if got := srv.serviceFailure(t.Context(), unit); got != "" {
			t.Errorf("für %q wurde etwas gelesen: %q", unit, got)
		}
	}
}
