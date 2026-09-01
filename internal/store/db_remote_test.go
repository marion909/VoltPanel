package store

import (
	"strings"
	"testing"
)

// TestWhitelistNimmtNurAdressen ist die Kernzusage der Herkunftsliste.
//
// Der Eintrag wird in MariaDB zu 'benutzer'@'HIER'. Was hier durchkommt,
// entscheidet, von wo aus sich jemand an einer Kundendatenbank anmelden kann —
// aus dem Internet, ohne Panel, ohne Sitzung, ohne 2FA.
//
// Geprüft wird jeweils der Grund der Ablehnung, nicht nur dass eine kommt.
func TestWhitelistNimmtNurAdressen(t *testing.T) {
	abgelehnt := []struct {
		name, input, enthält string
	}{
		// Der Platzhalter von MySQL. 'kunde'@'%' nimmt Verbindungen von
		// überall an — die Whitelist wäre leer in dem Moment, in dem sie
		// angelegt wird.
		{"platzhalter", "%", "platzhalter"},
		{"platzhalter im netz", "192.168.1.%", "platzhalter"},
		{"platzhalter am anfang", "%.example.at", "platzhalter"},

		// Hostnamen löst MariaDB rückwärts über DNS auf. Wer den PTR-Eintrag
		// seiner Adresse setzen kann, bestimmt damit selbst, für welchen
		// Eintrag er gehalten wird.
		{"hostname", "buero.example.at", "hostnamen sind nicht erlaubt"},
		{"nackter name", "localhost.localdomain", "hostnamen sind nicht erlaubt"},

		// Dasselbe wie %, nur anders geschrieben.
		{"alles v4", "0.0.0.0/0", "0.0.0.0"},
		{"alles v6", "::/0", "0.0.0.0"},

		// Zu weite Netze.
		{"halbes internet", "10.0.0.0/8", "zu weit gefasst"},
		{"v6 zu weit", "2001:db8::/32", "zu weit gefasst"},

		// Sinnlose Einträge.
		{"loopback", "127.0.0.1", "localhost braucht keinen eintrag"},
		{"leer", "   ", "fehlt"},
		{"tippfehler im netz", "10.0.0.5/24", "keine netzadresse"},
		{"unsinn", "kein-netz/24", "kein gültiges netz"},
	}

	for _, tc := range abgelehnt {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeRemoteHost(tc.input)
			if err == nil {
				t.Fatalf("%q wurde als Herkunft angenommen und ergab %q", tc.input, got)
			}
			if !strings.Contains(err.Error(), tc.enthält) {
				t.Errorf("abgelehnt, aber aus dem falschen Grund: %v", err)
			}
		})
	}
}

// TestWhitelistFormtUm: MariaDB versteht bei IPv4 nur die Netzmaske, nicht die
// Präfixlänge. Eingeben lässt sich trotzdem die kurze Form — sie ist die, die
// jemand kennt.
func TestWhitelistFormtUm(t *testing.T) {
	cases := map[string]string{
		"203.0.113.5":     "203.0.113.5",
		"  203.0.113.5  ": "203.0.113.5",
		// /32 ist derselbe einzelne Rechner, nur umständlicher geschrieben.
		"203.0.113.5/32": "203.0.113.5",
		"10.0.0.0/24":    "10.0.0.0/255.255.255.0",
		"10.0.0.0/16":    "10.0.0.0/255.255.0.0",
		"192.168.0.0/22": "192.168.0.0/255.255.252.0",
		"2001:db8::1":    "2001:db8::1",
		"2001:db8::/64":  "2001:db8::/64",
	}
	for input, want := range cases {
		got, err := NormalizeRemoteHost(input)
		if err != nil {
			t.Errorf("%q wurde abgelehnt: %v", input, err)
			continue
		}
		if got != want {
			t.Errorf("%q ergab %q, erwartet %q", input, got, want)
		}
	}
}

// TestWhitelistTrenntKeineFelder: der Eintrag geht als 'benutzer'@'HIER' in
// eine SQL-Anweisung. Ein Anführungszeichen darin wäre ein Ausbruch daraus.
func TestWhitelistTrenntKeineFelder(t *testing.T) {
	böse := []string{
		"1.2.3.4' OR '1'='1",
		"1.2.3.4`",
		"1.2.3.4\\",
		"1.2.3.4; DROP USER x",
		"1.2.3.4\n5.6.7.8",
		strings.Repeat("1", 200),
	}
	for _, input := range böse {
		if got, err := NormalizeRemoteHost(input); err == nil {
			t.Errorf("%q wurde angenommen und ergab %q", input, got)
		}
	}
}

// TestWhitelistPruefungGiltAuchImRepository: die Prüfung nützt nichts, wenn
// sie sich am Repository vorbei umgehen lässt.
//
// Der Aufrufer schickt beim Anlegen einen Rohwert; normalisiert wird er in
// CreateRemoteHost. Ohne diesen Test wäre die Funktion oben gut geprüft und
// trotzdem nie aufgerufen — dieselbe Art Lücke, die dieses Projekt schon bei
// mysql.import und den FTP-Zugängen hatte.
func TestWhitelistPruefungGiltAuchImRepository(t *testing.T) {
	s := newTestStore(t)
	ctx, sys := t.Context(), SystemScope()

	tenant := &Tenant{Name: "kunde", Slug: "kunde"}
	if err := s.CreateTenant(ctx, sys, tenant); err != nil {
		t.Fatal(err)
	}
	db := &Database{TenantID: tenant.ID, Name: "kunde_shop", Charset: "utf8mb4"}
	if err := s.CreateDatabase(ctx, sys, db); err != nil {
		t.Fatal(err)
	}
	user := &DBUser{
		TenantID: tenant.ID, DatabaseID: db.ID, Username: "kunde_shop",
		HostPattern: "localhost", Grants: "ALL",
	}
	if err := s.CreateDBUser(ctx, sys, user); err != nil {
		t.Fatal(err)
	}

	böse := &DBRemoteHost{TenantID: tenant.ID, DBUserID: user.ID, Host: "%"}
	if err := s.CreateRemoteHost(ctx, sys, böse); err == nil {
		t.Fatal("% wurde in die Herkunftsliste geschrieben")
	}

	// Und die gültige Form landet normalisiert in der Zeile, nicht so, wie
	// sie eingegeben wurde.
	gut := &DBRemoteHost{TenantID: tenant.ID, DBUserID: user.ID, Host: "10.0.0.0/24"}
	if err := s.CreateRemoteHost(ctx, sys, gut); err != nil {
		t.Fatalf("10.0.0.0/24 wurde abgelehnt: %v", err)
	}
	hosts, err := s.ListRemoteHosts(ctx, sys, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 1 || hosts[0].Host != "10.0.0.0/255.255.255.0" {
		t.Errorf("in der Datenbank steht %+v, erwartet 10.0.0.0/255.255.255.0", hosts)
	}
}
