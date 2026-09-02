package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestFirewallRegelWirdGebautNichtDurchgereicht ist der Kern.
//
// Eine Regel als Text entgegenzunehmen hieße, ufws eigene Sprache
// durchzureichen: "allow from 1.2.3.4 to any port 22 proto tcp" ist gültig, und
// so ist es auch allerlei, was niemand gemeint hat. Aus Teilen gebaut gibt es
// nichts, was sich anders zusammensetzen ließe.
func TestFirewallRegelWirdGebautNichtDurchgereicht(t *testing.T) {
	gut := map[FirewallRuleParams]string{
		{Action: "allow", Port: 22, Proto: "tcp"}:                   "22/tcp",
		{Action: "deny", Port: 3306, Proto: "tcp"}:                  "3306/tcp",
		{Action: "allow", Port: 30000, PortTo: 30100, Proto: "tcp"}: "30000:30100/tcp",
		{Action: "allow", Port: 53, Proto: "udp"}:                   "53/udp",
	}
	for p, want := range gut {
		got, err := ufwRule(p)
		if err != nil {
			t.Errorf("%+v wurde abgelehnt: %v", p, err)
			continue
		}
		if got != want {
			t.Errorf("%+v → %q, erwartet %q", p, got, want)
		}
	}

	schlecht := []FirewallRuleParams{
		{Action: "", Port: 22, Proto: "tcp"},
		{Action: "allow; deny 443", Port: 22, Proto: "tcp"},
		{Action: "ALLOW", Port: 22, Proto: "tcp"},
		{Action: "reject", Port: 22, Proto: "tcp"},
		{Action: "allow", Port: 0, Proto: "tcp"},
		{Action: "allow", Port: 70000, Proto: "tcp"},
		{Action: "allow", Port: -1, Proto: "tcp"},
		{Action: "allow", Port: 22, Proto: ""},
		{Action: "allow", Port: 22, Proto: "tcp from any"},
		{Action: "allow", Port: 22, Proto: "any"},
		{Action: "allow", Port: 100, PortTo: 50, Proto: "tcp"},
		{Action: "allow", Port: 100, PortTo: 70000, Proto: "tcp"},
	}
	for _, p := range schlecht {
		if got, err := ufwRule(p); err == nil {
			t.Errorf("%+v wurde angenommen als %q", p, got)
		}
	}
}

// TestJailNameIstKeinSchalter: der Name geht als Argument an fail2ban-client.
// Ein führender Bindestrich wäre dort ein Schalter, kein Name.
func TestJailNameIstKeinSchalter(t *testing.T) {
	srv, _ := testServer(t)

	for _, jail := range []string{
		"", "-h", "--help", "sshd extra", "sshd;id", "../etc/passwd",
		strings.Repeat("x", 70),
	} {
		raw, _ := json.Marshal(Fail2banUnbanParams{Jail: jail, IP: "1.2.3.4"})
		if _, err := srv.opFail2banUnban(t.Context(), raw); err == nil {
			t.Errorf("der Jail-Name %q wurde angenommen", jail)
		}
	}
}

// TestNurEchteAdressenWerdenEntsperrt: die Adresse geht als Argument weiter.
// Geprüft wird sie über netip, nicht über ein Muster — eine Adresse ist
// entweder eine oder nicht.
func TestNurEchteAdressenWerdenEntsperrt(t *testing.T) {
	srv, _ := testServer(t)

	for _, ip := range []string{
		"", "keine-ip", "1.2.3.4.5", "1.2.3.4/24", "1.2.3.4 --help",
		"999.1.1.1", "-1.2.3.4", "1.2.3.4;id",
	} {
		raw, _ := json.Marshal(Fail2banUnbanParams{Jail: "sshd", IP: ip})
		_, err := srv.opFail2banUnban(t.Context(), raw)
		if err == nil {
			t.Errorf("%q wurde als Adresse angenommen", ip)
			continue
		}
		if !strings.Contains(err.Error(), "keine ip-adresse") {
			t.Errorf("%q abgelehnt, aber aus dem falschen Grund: %v", ip, err)
		}
	}
}

// TestUfwStatusWirdGelesen prüft das Zerlegen an einer echten Ausgabe.
func TestUfwStatusWirdGelesen(t *testing.T) {
	out := `Status: active
Logging: on (low)
Default: deny (incoming), allow (outgoing), disabled (routed)
New profiles: skip

To                         Action      From
--                         ------      ----
22/tcp                     ALLOW IN    Anywhere
80,443/tcp                 ALLOW IN    Anywhere
30000:30100/tcp            ALLOW IN    Anywhere
3306/tcp                   DENY IN     Anywhere`

	res := parseUfwStatus(out)
	if !res.Active {
		t.Error("der Status wurde als inaktiv gelesen")
	}
	if len(res.Rules) != 4 {
		t.Fatalf("%d Regeln gelesen, erwartet 4: %+v", len(res.Rules), res.Rules)
	}
	if res.Rules[0].Port != "22" || res.Rules[0].Proto != "tcp" || res.Rules[0].Action != "ALLOW" {
		t.Errorf("erste Regel: %+v", res.Rules[0])
	}
	if res.Rules[2].Port != "30000:30100" {
		t.Errorf("Bereich wurde als %q gelesen", res.Rules[2].Port)
	}

	// Eine ausgeschaltete Firewall ist nicht dasselbe wie keine.
	aus := parseUfwStatus("Status: inactive")
	if aus.Active {
		t.Error("inactive wurde als aktiv gelesen")
	}
	if aus.Backend != "ufw" {
		t.Errorf("Backend ist %q", aus.Backend)
	}
}

// TestGesperrteAdressenWerdenGelesen: die Liste geht ins Panel und von dort als
// Argument zurück. Was keine Adresse ist, hat darin nichts zu suchen.
func TestGesperrteAdressenWerdenGelesen(t *testing.T) {
	out := `Status for the jail: sshd
|- Filter
|  |- Currently failed: 2
|  |- Total failed:     417
|  ` + "`" + `- File list:        /var/log/auth.log
` + "`" + `- Actions
   |- Currently banned: 3
   |- Total banned:     42
   ` + "`" + `- Banned IP list:   1.2.3.4 5.6.7.8 2001:db8::1`

	currently, total, banned := parseJailStatus(out)
	if currently != 3 || total != 42 {
		t.Errorf("currently=%d total=%d, erwartet 3 und 42", currently, total)
	}
	if len(banned) != 3 {
		t.Fatalf("%d Adressen gelesen: %v", len(banned), banned)
	}

	// Was in der Zeile steht, aber keine Adresse ist, fliegt raus.
	_, _, gemischt := parseJailStatus("`- Banned IP list:   1.2.3.4 --help kaputt 5.6.7.8")
	if len(gemischt) != 2 {
		t.Errorf("aus einer gemischten Zeile kamen %v", gemischt)
	}
}

// TestJailListeWirdGefiltert: der Name aus der Ausgabe geht gleich als Argument
// weiter. Was nicht wie ein Jail-Name aussieht, wird übergangen.
func TestJailListeWirdGefiltert(t *testing.T) {
	namen := parseJailList("Status\n|- Number of jail: 2\n`- Jail list:\tsshd, nginx-http-auth")
	if len(namen) != 2 || namen[0] != "sshd" || namen[1] != "nginx-http-auth" {
		t.Errorf("gelesen: %v", namen)
	}

	kaputt := parseJailList("`- Jail list:\tsshd, --help, mit leerzeichen, ../etc")
	if len(kaputt) != 1 || kaputt[0] != "sshd" {
		t.Errorf("aus einer kaputten Liste kamen %v", kaputt)
	}
}
