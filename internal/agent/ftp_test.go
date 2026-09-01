package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestFTPZugangNurUnterEinemSiteBenutzer ist die Kernzusage: ein FTP-Zugang
// bekommt die UID des Systembenutzers seiner Site — nie die eines Systemkontos.
//
// Ein Zugang mit der UID 0 wäre ein Vollzugriff auf den Server über ein
// gewöhnliches FTP-Programm, ganz ohne Lücke im Panel.
//
// Geprüft wird der Grund der Ablehnung, nicht nur dass eine kommt: auf einem
// Rechner ohne die Systembenutzer scheitert ohnehin jeder Aufruf, und ein Test
// auf "irgendein Fehler" wäre grün, auch wenn die Prüfung fehlte.
func TestFTPZugangNurUnterEinemSiteBenutzer(t *testing.T) {
	cases := []struct {
		name    string
		user    string
		enthält string
	}{
		{"root", "root", "reservierter systembenutzer"},
		{"webserver", "www-data", "reservierter systembenutzer"},
		{"gewöhnliches konto", "alice", "kein systembenutzer einer site"},
		{"panel selbst", "volt", "reservierter systembenutzer"},
		{"ungültiger name", "Site_X", "benutzername"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := siteUserIDs(OpFTPUserSet, tc.user)
			if err == nil {
				t.Fatalf("ein ftp-zugang als %q wurde erlaubt", tc.user)
			}
			if !strings.Contains(err.Error(), tc.enthält) {
				t.Errorf("abgelehnt, aber aus dem falschen Grund: %v", err)
			}
		})
	}
}

// TestFTPPasswortOhneFeldtrenner: das Passwort landet in der PureDB, in der der
// Doppelpunkt die Felder trennt, und geht über die Standardeingabe an pure-pw,
// wo der Zeilenumbruch die Eingabe beendet. Beides muss ausgeschlossen sein.
func TestFTPPasswortOhneFeldtrenner(t *testing.T) {
	abgelehnt := []string{
		"kurz",                    // zu kurz
		"mit:doppelpunkt1234",     // Feldtrenner der PureDB
		"mit\nzeilenumbruch12345", // beendet die Eingabe an pure-pw
		"mit leerzeichen 12345",
		"mit'anfuehrung1234",
		`mit"anfuehrung1234`,
		"mit\\backslash12345",
	}
	for _, pw := range abgelehnt {
		p := FTPUserParams{Username: "kunde_shop", Password: pw}
		if err := checkFTPUser(OpFTPUserSet, p); err == nil {
			t.Errorf("das passwort %q wurde angenommen", pw)
		}
	}

	ok := FTPUserParams{Username: "kunde_shop", Password: "Xk7#pQ2m!vR9tZ4w"}
	if err := checkFTPUser(OpFTPUserSet, ok); err != nil {
		t.Errorf("ein brauchbares passwort wurde abgelehnt: %v", err)
	}
}

// TestFTPHeimatverzeichnisBleibtInDenWurzeln: der Pfad geht durch dieselbe
// jail()-Prüfung wie jeder andere. Ein Zugang mit /etc als Heimatverzeichnis
// wäre trotz Chroot ein Leseblick auf die Systemkonfiguration.
func TestFTPHeimatverzeichnisBleibtInDenWurzeln(t *testing.T) {
	srv, _ := testServer(t)

	raw, _ := json.Marshal(FTPUserParams{
		Username: "kunde_shop", Password: "Xk7#pQ2m!vR9tZ4w",
		SystemUser: "site_example_at", HomeDir: "/etc",
	})
	_, err := srv.opFTPUserSet(context.Background(), raw)
	if err == nil {
		t.Fatal("/etc wurde als Heimatverzeichnis akzeptiert")
	}
	// Der Systembenutzer existiert auf einem Entwicklungsrechner nicht; dann
	// greift diese Prüfung zuerst. Beides ist eine Ablehnung aus dem richtigen
	// Grund — nur "irgendein Fehler" wäre keine Aussage.
	msg := err.Error()
	if !strings.Contains(msg, "außerhalb") && !strings.Contains(msg, "gibt es nicht") {
		t.Errorf("abgelehnt, aber aus dem falschen Grund: %v", err)
	}
}

// TestFTPEinstellungenSindVollstaendig hält die Werte fest, ohne die der Dienst
// unsicher wäre. Sie stehen in einer Tabelle, und eine Tabelle verliert leicht
// eine Zeile.
func TestFTPEinstellungenSindVollstaendig(t *testing.T) {
	settings := ftpSettings()

	müssen := map[string]string{
		// Ohne Chroot käme ein Kunde über .. in fremde Verzeichnisse.
		"ChrootEveryone": "yes",
		// Ohne diese Grenze könnte ein Eintrag mit falscher UID ein
		// Systemkonto ansprechen.
		"MinUID": "1000",
		// FTP ohne TLS schickt das Passwort im Klartext.
		"TLS": "2",
		// Anonymer Zugang ist nie gewollt.
		"NoAnonymous": "yes",
	}
	for name, want := range müssen {
		if settings[name].Value != want {
			t.Errorf("%s ist %q, muss %q sein", name, settings[name].Value, want)
		}
	}
}

// Der Debian-Wrapper liest /etc/pure-ftpd/conf und prüft jeden Wert. Was er
// nicht versteht, überspringt er nicht — er stirbt daran, und der Dienst
// startet nicht mehr. Aus debian/pure-ftpd-wrapper, wörtlich:
//
//	sub parse_number_2 { ... if ($val !~ /^(\d+)\s+(\d+)$/) { ... return; } }
//	sub parse_umask    { ... if ($val !~ /^([0-7]{3,3})\s+([0-7]{3,3})$/) { ... return; } }
//	sub parse_number_1 { ... }   sub parse_yesno { yes|1|on|no|0|off }
//	for (@conffiles) { next unless /^[A-Za-z][A-Za-z0-9]+$/; ... }
//
// Die Ausdrücke stehen hier ein zweites Mal, unabhängig von ftpValueRules
// abgeschrieben: sonst prüfte der Test die Tabelle gegen sich selbst.
var wrapperRegeln = map[ftpValueKind]*regexp.Regexp{
	ftpYesNo:      regexp.MustCompile(`^(?:[Yy][Ee][Ss]|[Nn][Oo]|[Oo][Nn]|[Oo][Ff][Ff]|0|1)$`),
	ftpNumber:     regexp.MustCompile(`^[0-9]+$`),
	ftpTwoNumbers: regexp.MustCompile(`^([0-9]+)\s+([0-9]+)$`),
	ftpUmask:      regexp.MustCompile(`^([0-7]{3,3})\s+([0-7]{3,3})$`),
}

// TestFTPWerteUeberlebenDenWrapper prüft jeden Wert der Tabelle gegen die
// Regel, nach der pure-ftpd-wrapper ihn liest.
//
// Zweimal ist genau das hier schiefgegangen: Umask stand als "137:027" und
// PassivePortRange als "30000:30100" — beide mit Doppelpunkt, weil das die
// Schreibweise ist, die pure-ftpd selbst auf der Kommandozeile nimmt. Der
// Wrapper will an dieser Stelle aber zwei durch Leerzeichen getrennte Zahlen
// und baut den Doppelpunkt selbst ein.
func TestFTPWerteUeberlebenDenWrapper(t *testing.T) {
	for name, set := range ftpSettings() {
		if !regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]+$`).MatchString(name) {
			t.Errorf("%q sieht der Wrapper gar nicht an — die Einstellung fiele "+
				"stillschweigend weg", name)
		}
		re, ok := wrapperRegeln[set.Kind]
		if !ok {
			continue // ftpFilename: erst zur Laufzeit prüfbar
		}
		if !re.MatchString(set.Value) {
			t.Errorf("%s=%q: daran stirbt der Wrapper", name, set.Value)
		}
	}
}

// TestDoppelpunktWirdAbgelehnt hält die beiden Fehler fest, die den Dienst
// wirklich lahmgelegt haben. Ohne diesen Test fiele ein Rückfall erst auf dem
// Server auf.
func TestDoppelpunktWirdAbgelehnt(t *testing.T) {
	falsch := map[ftpValueKind]string{
		ftpUmask:      "137:027",
		ftpTwoNumbers: "30000:30100",
	}
	for kind, wert := range falsch {
		if ftpValueRules[kind].MatchString(wert) {
			t.Errorf("%q wird durchgelassen, der Wrapper lehnt es ab", wert)
		}
	}
}

// TestFTPEinstellungenWerdenGeprueft: checkFTPSettings muss die Tabelle
// annehmen, wie sie ist — sonst richtet der Agent gar nichts mehr ein.
func TestFTPEinstellungenWerdenGeprueft(t *testing.T) {
	if err := checkFTPSettings(); err != nil {
		t.Fatalf("die eigene Tabelle wird abgelehnt: %v", err)
	}
}

// TestPEMBekommtSeinenZeilenumbruch: endet der Schlüssel ohne Umbruch, stünden
// Ende und Anfang in derselben Zeile, und pure-ftpd könnte die Datei nicht
// lesen. Mit TLS=2 heißt das: der Dienst startet nicht.
func TestPEMBekommtSeinenZeilenumbruch(t *testing.T) {
	key := []byte("-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----")
	cert := []byte("-----BEGIN CERTIFICATE-----\nBBBB\n-----END CERTIFICATE-----\n")

	got := string(joinPEM(key, cert))
	if strings.Contains(got, "-----END PRIVATE KEY----------BEGIN CERTIFICATE-----") {
		t.Errorf("Ende und Anfang kleben aneinander:\n%s", got)
	}
	if !strings.Contains(got, "-----END PRIVATE KEY-----\n-----BEGIN CERTIFICATE-----") {
		t.Errorf("der Umbruch fehlt:\n%s", got)
	}
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("die Datei endet ohne Umbruch:\n%s", got)
	}
	// Ein schon sauber endender Schlüssel darf keine Leerzeile bekommen.
	zwei := string(joinPEM([]byte("A\n\n"), []byte("B\n")))
	if zwei != "A\nB\n" {
		t.Errorf("aus A und B wurde %q", zwei)
	}
}

// TestUnbrauchbaresZertifikatWirdNichtGeschrieben: ein Paar, das nicht
// zusammengehört, darf nicht nach /etc/ssl/private wandern. Sonst startet
// pure-ftpd nicht, und die Meldung dazu käme aus pure-ftpd statt von hier.
func TestUnbrauchbaresZertifikatWirdNichtGeschrieben(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "panel"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, inhalt := range map[string]string{
		"fullchain.pem": "-----BEGIN CERTIFICATE-----\nkein zertifikat\n-----END CERTIFICATE-----\n",
		"privkey.pem":   "-----BEGIN PRIVATE KEY-----\nkein schlüssel\n-----END PRIVATE KEY-----\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, "panel", name), []byte(inhalt), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	srv, _ := testServer(t)
	srv.certDir = dir
	srv.panelDomain = ""

	err := srv.writeFTPCert()
	if err == nil {
		t.Fatal("das Paar wurde angenommen")
	}
	if !strings.Contains(err.Error(), "passen nicht zusammen") {
		t.Errorf("abgelehnt, aber aus dem falschen Grund: %v", err)
	}
}
