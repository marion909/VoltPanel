package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestAbfrageDSNLaesstNurEineAnweisungZu hält die Verbindungsparameter fest,
// unter denen die Anweisung eines Kunden läuft.
//
// Es ist eine Zeichenkette, und genau deshalb steht sie hier: ein späteres
// `multiStatements=true` — für ein anderes Feature vielleicht sogar sinnvoll —
// würde aus jeder Abfrage eine Kette machen, in der hinter dem SELECT noch ein
// DROP stehen darf. Nichts im Code würde sich sonst dagegen wehren.
func TestAbfrageDSNLaesstNurEineAnweisungZu(t *testing.T) {
	verboten := map[string]string{
		"multiStatements":   "mehrere Anweisungen in einem Aufruf",
		"allowAllFiles":     "Dateizugriff über LOAD DATA LOCAL",
		"allowOldPasswords": "veraltete, schwache Passwortübertragung",
	}
	for param, warum := range verboten {
		if strings.Contains(queryConnectDSN, param) {
			t.Errorf("%s steht im DSN und erlaubt %s", param, warum)
		}
	}

	// Über den Unix-Socket, nicht über das Netz: eine TCP-Verbindung ginge
	// über eine Schnittstelle, die auch von außen erreichbar sein kann.
	if !strings.Contains(queryConnectDSN, "unix(/var/run/mysqld/mysqld.sock)") {
		t.Errorf("die abfrage läuft nicht über den unix-socket: %s", queryConnectDSN)
	}
}

// TestAbfrageBrauchtEinenSauberenDatenbanknamen: der Name geht in den DSN und
// in ein GRANT. Beides sind Stellen, an denen ein Sonderzeichen etwas anderes
// bedeutet als gemeint.
//
// Der Web-Prozess leitet den Namen aus der Datenbank-ID ab, aber der Agent darf
// sich darauf nicht verlassen.
func TestAbfrageBrauchtEinenSauberenDatenbanknamen(t *testing.T) {
	srv, _ := testServer(t)

	abgelehnt := []string{
		"",
		"fremde_db; DROP DATABASE x",
		"db`name",
		"db'name",
		"db/name",
		"../etc",
		"DB_GROSS",
		"a",
		strings.Repeat("d", 80),
	}
	for _, name := range abgelehnt {
		raw, _ := json.Marshal(MySQLQueryParams{Database: name, Statement: "SELECT 1"})
		if _, err := srv.opMySQLQuery(context.Background(), raw); err == nil {
			t.Errorf("der datenbankname %q wurde angenommen", name)
		} else if !strings.Contains(err.Error(), "datenbankname") {
			t.Errorf("%q abgelehnt, aber aus dem falschen Grund: %v", name, err)
		}
	}
}

// TestLeereAnweisungWirdVorDerVerbindungAbgewiesen: die Prüfungen der Eingabe
// stehen vor dem ersten Kontakt mit MariaDB. Sonst legte jede leere Eingabe ein
// Wegwerf-Konto an, das sie gar nicht braucht.
func TestLeereAnweisungWirdVorDerVerbindungAbgewiesen(t *testing.T) {
	srv, _ := testServer(t)

	for _, statement := range []string{"", "   ", "\n\t "} {
		raw, _ := json.Marshal(MySQLQueryParams{Database: "kunde_shop", Statement: statement})
		_, err := srv.opMySQLQuery(context.Background(), raw)
		if err == nil {
			t.Fatalf("die leere anweisung %q wurde ausgeführt", statement)
		}
		if !strings.Contains(err.Error(), "leer") {
			t.Errorf("abgelehnt, aber aus dem falschen Grund: %v", err)
		}
	}

	// Und eine übergroße ebenso — vor der Verbindung.
	raw, _ := json.Marshal(MySQLQueryParams{
		Database: "kunde_shop", Statement: strings.Repeat("a", queryMaxLength+1),
	})
	if _, err := srv.opMySQLQuery(context.Background(), raw); err == nil ||
		!strings.Contains(err.Error(), "länger als") {
		t.Errorf("eine übergroße anweisung kam durch: %v", err)
	}
}

// TestWegwerfkontenWerdenAlleAufgeraeumt: bricht ein Lauf ab, bleibt ein Konto
// mit Rechten auf einer Kundendatenbank liegen. Aufgeräumt wird beim nächsten
// Lauf — aber nur, wenn dessen Suche auch die andere Sorte kennt.
func TestWegwerfkontenWerdenAlleAufgeraeumt(t *testing.T) {
	for _, prefix := range []string{importUserPrefix, queryUserPrefix} {
		var gefunden bool
		for _, p := range throwawayPrefixes {
			if p == prefix {
				gefunden = true
			}
		}
		if !gefunden {
			t.Errorf("%q fehlt in throwawayPrefixes — konten dieser art bleiben liegen", prefix)
		}
	}

	// Die Präfixe müssen durch reMyUser passen, sonst überspringt das
	// Aufräumen die eigenen Konten wieder.
	for _, prefix := range throwawayPrefixes {
		if !reMyUser.MatchString(prefix + "abc123") {
			t.Errorf("konten mit dem präfix %q passen nicht durch reMyUser", prefix)
		}
	}
}

// TestAbfragefehlerErklaertDieGrenze: „Access denied“ sieht nach einem Fehler
// im Panel aus. Es ist aber die Zusage, die die Operation gibt.
func TestAbfragefehlerErklaertDieGrenze(t *testing.T) {
	msg := queryHint(
		errString("Error 1044: Access denied for user 'volt_query_ab12'@'localhost' to database 'fremde'"),
		"kunde_shop")
	if !strings.Contains(msg, "nur auf kunde_shop") {
		t.Errorf("die Meldung erklärt die Grenze nicht: %q", msg)
	}

	// Ein gewöhnlicher Fehler bleibt, wie er ist.
	plain := queryHint(errString("Error 1146: Table 'kunde_shop.gibtsnicht' doesn't exist"), "kunde_shop")
	if strings.Contains(plain, "nur auf") {
		t.Errorf("ein gewöhnlicher Fehler wurde um eine unpassende Erklärung ergänzt: %q", plain)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
