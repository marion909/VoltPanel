package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
)

// Firewall und Fail2ban.
//
// Bis hierher öffnete install.sh Ports in ufw, sofern ufw überhaupt läuft, und
// bei nftables gab es eine Warnung und sonst nichts. Wer danach etwas ändern
// wollte, brauchte eine Shell.
//
// Zwei Dinge sind hier bewusst ungleich behandelt. ufw kennt eine klare
// Regelform — Port, Protokoll, erlauben oder sperren —, und die lässt sich
// vollständig aus geprüften Werten bauen. nftables kennt ein Regelwerk, in das
// sich eine Zeile nicht gefahrlos einfügen lässt, ohne zu wissen, wie der
// Betreiber es aufgebaut hat. Deshalb: ufw schreibend, nftables nur lesend.
//
// Das ist keine Bequemlichkeit. Eine halb verstandene Regel in einer fremden
// nftables-Kette ist der Weg, sich vom eigenen Server auszusperren.

var (
	// reJail ist ein Jail-Name aus der fail2ban-Konfiguration.
	//
	// Er geht als Argument an fail2ban-client. Ein führender Bindestrich wäre
	// dort ein Schalter, kein Name.
	reJail = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)

	// reUfwStatusLine liest eine Zeile aus `ufw status numbered`.
	reUfwPort = regexp.MustCompile(`^([0-9]{1,5})(:[0-9]{1,5})?/(tcp|udp)$`)
)

// FirewallStatus ist die Auskunft über die Firewall.
type FirewallStatus struct {
	// Backend ist "ufw", "nftables" oder "" — letzteres heißt: keines von
	// beiden ist erkennbar.
	Backend string `json:"backend"`
	Active  bool   `json:"active"`
	// Writable sagt, ob das Panel Regeln ändern kann.
	Writable bool           `json:"writable"`
	Rules    []FirewallRule `json:"rules"`
	Hinweis  string         `json:"hinweis,omitempty"`
}

type FirewallRule struct {
	// Raw ist die Zeile, wie das Werkzeug sie ausgibt. Sie wird angezeigt,
	// nicht ausgewertet.
	Raw    string `json:"raw"`
	Port   string `json:"port,omitempty"`
	Proto  string `json:"proto,omitempty"`
	Action string `json:"action,omitempty"`
	From   string `json:"from,omitempty"`
}

// opFirewallStatus sagt, was läuft und was offen ist.
func (s *Server) opFirewallStatus(ctx context.Context, _ json.RawMessage) (any, error) {
	if fileExists(allowedBinaries["ufw"]) {
		out, err := run(ctx, shortTimeout, "ufw", "status", "verbose")
		if err == nil {
			res := parseUfwStatus(out)
			res.Writable = res.Active
			if !res.Active {
				res.Hinweis = "ufw ist installiert, aber nicht eingeschaltet. " +
					"Solange es aus ist, gilt keine dieser Regeln."
			}
			return res, nil
		}
	}

	if fileExists(allowedBinaries["nft"]) {
		out, err := run(ctx, shortTimeout, "nft", "list", "ruleset")
		if err == nil {
			return FirewallStatus{
				Backend: "nftables", Active: strings.TrimSpace(out) != "",
				Rules: []FirewallRule{{Raw: truncate(out, 16<<10)}},
				Hinweis: "nftables wird nur gelesen. In ein gewachsenes Regelwerk " +
					"lässt sich keine Zeile gefahrlos einfügen, ohne zu wissen, wie es " +
					"aufgebaut ist — und eine halb verstandene Regel in einer fremden " +
					"Kette ist der Weg, sich vom eigenen Server auszusperren.",
			}, nil
		}
	}

	return FirewallStatus{
		Hinweis: "Weder ufw noch nftables sind erreichbar. Auf diesem Server ist " +
			"keine Firewall eingerichtet, die das Panel sehen könnte.",
	}, nil
}

// parseUfwStatus liest die Ausgabe von `ufw status verbose`.
func parseUfwStatus(out string) FirewallStatus {
	res := FirewallStatus{Backend: "ufw", Rules: []FirewallRule{}}

	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "Status:"):
			// Auf den Wert geprüft, nicht auf ein Vorkommen: "inactive" enthält
			// "active". Der Test hat genau das gefunden.
			res.Active = strings.TrimSpace(strings.TrimPrefix(line, "Status:")) == "active"
			continue
		case line == "", strings.HasPrefix(line, "Logging:"),
			strings.HasPrefix(line, "Default:"), strings.HasPrefix(line, "New profiles:"),
			strings.HasPrefix(line, "To "), strings.HasPrefix(line, "--"):
			continue
		}

		// "22/tcp   ALLOW IN   Anywhere"
		felder := strings.Fields(line)
		if len(felder) < 2 {
			continue
		}
		rule := FirewallRule{Raw: line}
		if m := reUfwPort.FindStringSubmatch(felder[0]); m != nil {
			rule.Port, rule.Proto = m[1]+m[2], m[3]
		}
		rule.Action = felder[1]
		if len(felder) >= 4 {
			rule.From = strings.Join(felder[3:], " ")
		}
		res.Rules = append(res.Rules, rule)
	}
	return res
}

// FirewallRuleParams beschreibt eine Regel — nicht als Zeichenkette, sondern in
// Teilen.
//
// Eine Regel als Text entgegenzunehmen hieße, ufws eigene Sprache
// durchzureichen: "allow from 1.2.3.4 to any port 22 proto tcp" ist gültig, und
// so ist es auch "allow 22; deny 443". Aus Teilen gebaut gibt es nichts, was
// jemand anders zusammensetzen könnte, als es gemeint war.
type FirewallRuleParams struct {
	// Action ist "allow" oder "deny".
	Action string `json:"action"`
	Port   int    `json:"port"`
	// PortTo > 0 macht daraus einen Bereich.
	PortTo int    `json:"port_to"`
	Proto  string `json:"proto"`
	// Remove entfernt die Regel, statt sie zu setzen.
	Remove bool `json:"remove"`
}

// opFirewallRule setzt oder entfernt eine ufw-Regel.
func (s *Server) opFirewallRule(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[FirewallRuleParams](raw, OpFirewallRule)
	if err != nil {
		return nil, err
	}
	regel, err := ufwRule(p)
	if err != nil {
		return nil, err
	}
	if !fileExists(allowedBinaries["ufw"]) {
		return nil, opInputErr(OpFirewallRule,
			"ufw ist auf diesem server nicht installiert — regeln lassen sich nur dort setzen")
	}

	args := []string{p.Action, regel}
	if p.Remove {
		args = append([]string{"delete"}, args...)
	}
	out, err := run(ctx, longTimeout, "ufw", args...)
	if err != nil {
		return nil, opErr(OpFirewallRule, "regel setzen: %s", truncate(out, 300))
	}
	return TextResult{Text: strings.TrimSpace(truncate(out, 300))}, nil
}

// ufwRule baut den Regelteil aus geprüften Werten.
func ufwRule(p FirewallRuleParams) (string, error) {
	switch p.Action {
	case "allow", "deny":
	default:
		return "", opInputErr(OpFirewallRule, "%q ist weder allow noch deny", p.Action)
	}
	switch p.Proto {
	case "tcp", "udp":
	default:
		return "", opInputErr(OpFirewallRule, "%q ist weder tcp noch udp", p.Proto)
	}
	if p.Port < 1 || p.Port > 65535 {
		return "", opInputErr(OpFirewallRule, "der port muss zwischen 1 und 65535 liegen")
	}
	if p.PortTo == 0 {
		return strconv.Itoa(p.Port) + "/" + p.Proto, nil
	}
	if p.PortTo < p.Port || p.PortTo > 65535 {
		return "", opInputErr(OpFirewallRule, "der bereich endet vor seinem anfang")
	}
	return fmt.Sprintf("%d:%d/%s", p.Port, p.PortTo, p.Proto), nil
}

// Fail2banStatus ist die Auskunft über fail2ban.
type Fail2banStatus struct {
	Available bool           `json:"available"`
	Active    bool           `json:"active"`
	Jails     []Fail2banJail `json:"jails"`
	Hinweis   string         `json:"hinweis,omitempty"`
}

type Fail2banJail struct {
	Name string `json:"name"`
	// Currently ist die Zahl der gerade gesperrten Adressen, Total die seit
	// dem Start insgesamt.
	Currently int      `json:"currently"`
	Total     int      `json:"total"`
	Banned    []string `json:"banned"`
}

// opFail2banStatus liest die Jails und die gesperrten Adressen.
func (s *Server) opFail2banStatus(ctx context.Context, _ json.RawMessage) (any, error) {
	res := Fail2banStatus{Jails: []Fail2banJail{}}
	if !fileExists(allowedBinaries["fail2ban-client"]) {
		res.Hinweis = "Fail2ban ist auf diesem Server nicht installiert."
		return res, nil
	}
	res.Available = true

	out, err := run(ctx, shortTimeout, "fail2ban-client", "status")
	if err != nil {
		res.Hinweis = "Fail2ban ist installiert, der Dienst antwortet aber nicht: " +
			truncate(out, 200)
		return res, nil
	}
	res.Active = true

	for _, name := range parseJailList(out) {
		jail := Fail2banJail{Name: name, Banned: []string{}}
		if detail, err := run(ctx, shortTimeout, "fail2ban-client", "status", name); err == nil {
			jail.Currently, jail.Total, jail.Banned = parseJailStatus(detail)
		}
		res.Jails = append(res.Jails, jail)
	}
	return res, nil
}

// parseJailList liest die Zeile "Jail list: sshd, nginx-http-auth".
func parseJailList(out string) []string {
	for _, line := range strings.Split(out, "\n") {
		_, liste, ok := strings.Cut(line, "Jail list:")
		if !ok {
			continue
		}
		var names []string
		for _, name := range strings.Split(liste, ",") {
			name = strings.TrimSpace(name)
			// Was nicht wie ein Jail-Name aussieht, wird übergangen: der Name
			// geht gleich als Argument weiter.
			if reJail.MatchString(name) {
				names = append(names, name)
			}
		}
		return names
	}
	return nil
}

// parseJailStatus liest die Zahlen und die Adressen aus `status <jail>`.
func parseJailStatus(out string) (currently, total int, banned []string) {
	banned = []string{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.Contains(line, "Currently banned:"):
			currently = letzteZahl(line)
		case strings.Contains(line, "Total banned:"):
			total = letzteZahl(line)
		case strings.Contains(line, "Banned IP list:"):
			_, liste, _ := strings.Cut(line, "Banned IP list:")
			for _, ip := range strings.Fields(liste) {
				// Nur, was wirklich eine Adresse ist. Die Liste geht ins Panel
				// und von dort als Argument zurück.
				if _, err := netip.ParseAddr(ip); err == nil {
					banned = append(banned, ip)
				}
			}
		}
	}
	return currently, total, banned
}

func letzteZahl(line string) int {
	felder := strings.Fields(line)
	if len(felder) == 0 {
		return 0
	}
	n, _ := strconv.Atoi(felder[len(felder)-1])
	return n
}

type Fail2banUnbanParams struct {
	Jail string `json:"jail"`
	IP   string `json:"ip"`
}

// opFail2banUnban hebt eine Sperre auf.
//
// Der häufigste Fall im Betrieb: jemand hat sein Passwort dreimal falsch
// eingegeben und kommt jetzt gar nicht mehr an den Server. Ohne diese Operation
// bräuchte er dafür eine Shell — die er gerade nicht bekommt.
func (s *Server) opFail2banUnban(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[Fail2banUnbanParams](raw, OpFail2banUnban)
	if err != nil {
		return nil, err
	}
	if !reJail.MatchString(p.Jail) {
		return nil, opInputErr(OpFail2banUnban, "%q ist kein jail-name", p.Jail)
	}
	// Über netip statt über ein Muster: eine Adresse ist entweder eine oder
	// nicht, und ein Muster dafür schreibt man dreimal falsch, bevor es sitzt.
	addr, err := netip.ParseAddr(strings.TrimSpace(p.IP))
	if err != nil {
		return nil, opInputErr(OpFail2banUnban, "%q ist keine ip-adresse", p.IP)
	}

	out, err := run(ctx, shortTimeout, "fail2ban-client", "set", p.Jail, "unbanip", addr.String())
	if err != nil {
		return nil, opErr(OpFail2banUnban, "sperre aufheben: %s", truncate(out, 300))
	}
	return TextResult{Text: addr.String() + " ist wieder frei"}, nil
}
