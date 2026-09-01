package agent

import (
	"strings"
	"testing"
)

// TestHerkunftWirdImAgentNochmalGeprueft: der Agent verlässt sich nicht darauf,
// dass der Web-Prozess geprüft hat.
//
// Er ist der einzige Prozess, der root an MariaDB ist. Wäre der Web-Prozess
// über eine Lücke übernommen, wäre `'kunde'@'%'` ohne diese Prüfung ein Konto,
// das Verbindungen von jeder Adresse der Welt annimmt — und das Passwort steht
// entschlüsselbar in derselben Datenbank, die der Angreifer schon hat.
//
// Das ist dieselbe Regel wie in store.NormalizeRemoteHost, absichtlich zweimal.
func TestHerkunftWirdImAgentNochmalGeprueft(t *testing.T) {
	abgelehnt := []string{
		"%",            // von überall
		"192.168.1.%",  // MySQL-Platzhalter im Netz
		"%.example.at", // Platzhalter im Namen
		"buero.example.at",
		"0.0.0.0",
		"::",
		"",
		"1.2.3.4' OR '1'='1",
		"1.2.3.4`",
		"1.2.3.4 ",
		"10.0.0.0/255.0.255.0", // Maske mit Loch
		// Wohlgeformt und zusammenhängend — und bedeutet in MariaDB trotzdem
		// jede Adresse. Die Form allein genügt als Prüfung also nicht.
		"1.2.3.4/0.0.0.0",
		"10.0.0.0/255.0.0.0", // /8, zu weit gefasst
		"2001:db8::/32",      // ebenfalls zu weit
		"10.0.0.0/keine-maske",
		"2001:db8::/999",
		strings.Repeat("9", 100),
	}
	for _, host := range abgelehnt {
		if err := checkMySQLHost(host); err == nil {
			t.Errorf("host-muster %q wurde vom Agent angenommen", host)
		}
	}

	// Genau die Formen, die der Store erzeugt — und localhost, das jedes
	// bestehende Konto trägt.
	erlaubt := []string{
		"localhost",
		"203.0.113.5",
		"10.0.0.0/255.255.255.0",
		"192.168.0.0/255.255.252.0",
		"2001:db8::1",
		"2001:db8::/64",
	}
	for _, host := range erlaubt {
		if err := checkMySQLHost(host); err != nil {
			t.Errorf("host-muster %q wurde abgelehnt: %v", host, err)
		}
	}
}

// TestBindAddressWirdRichtigGelesen: aus dieser Auskunft entscheidet die
// Oberfläche, ob sie einen Zugang von außen überhaupt anbietet. Ein falsches
// "horcht nicht" wäre nur lästig — ein falsches "horcht" wäre eine Zusage, die
// der Server nicht hält.
func TestBindAddressWirdRichtigGelesen(t *testing.T) {
	draußen := []string{
		// Leer heißt bei MariaDB "alle Schnittstellen". Das ist kein
		// Sonderfall, sondern der Zustand jedes Servers, an dem schon einmal
		// jemand von Hand geschraubt hat.
		"",
		"*",
		"0.0.0.0",
		"::",
		"203.0.113.5",
		"127.0.0.1,203.0.113.5",
	}
	for _, bind := range draußen {
		if !mysqlListensOutside(bind) {
			t.Errorf("bind_address %q wurde als nur-lokal gelesen", bind)
		}
	}

	nurLokal := []string{"127.0.0.1", "::1", "127.0.0.1,::1"}
	for _, bind := range nurLokal {
		if mysqlListensOutside(bind) {
			t.Errorf("bind_address %q wurde als von-außen-erreichbar gelesen", bind)
		}
	}
}
