package agent

import (
	"context"
	"encoding/json"
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
		if settings[name] != want {
			t.Errorf("%s ist %q, muss %q sein", name, settings[name], want)
		}
	}
}
