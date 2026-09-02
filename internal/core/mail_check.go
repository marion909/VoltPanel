package core

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strings"

	"github.com/marion909/voltpanel/internal/agent"
	"github.com/marion909/voltpanel/internal/store"
)

// Die Zustellbarkeitsprüfung.
//
// Der schwierige Teil eines Mailservers ist nicht der Code, sondern die
// Frage, ob eine Mail bei Gmail im Posteingang landet oder im Spam. Daran
// hängen ein Dutzend Kleinigkeiten, die alle woanders stehen: im DNS, beim
// Anbieter, in der Postfix-Konfiguration. Wer sie einzeln nachsieht, vergisst
// eine — und merkt es an einem Kunden, der sich beschwert.
//
// Deshalb steht hier eine Liste mit einem Befund je Zeile. Was das Panel selbst
// herstellen kann, stellt es her; was im DNS steht, kann es nur nachsehen und
// sagen, was einzutragen wäre.

// Die DNS-Auskünfte als Variablen, damit der Test sie ersetzen kann.
//
// Was hier geprüft wird, sind Regeln über Antworten des DNS. Sie an echtem DNS
// zu prüfen hieße, den Test von fremden Namen abhängig zu machen, die heute so
// und morgen anders auflösen — und in einer CI ohne Netz gar nicht.
var (
	dnsTXT = func(ctx context.Context, name string) ([]string, error) {
		return net.DefaultResolver.LookupTXT(ctx, name)
	}
	dnsMX = func(ctx context.Context, name string) ([]*net.MX, error) {
		return net.DefaultResolver.LookupMX(ctx, name)
	}
	dnsHost = func(ctx context.Context, name string) ([]string, error) {
		return net.DefaultResolver.LookupHost(ctx, name)
	}
	dnsAddr = func(ctx context.Context, ip string) ([]string, error) { return net.DefaultResolver.LookupAddr(ctx, ip) }
)

// Befund ist das Ergebnis einer einzelnen Prüfung.
type Befund struct {
	// Was geprüft wurde, in einem Wort — für die Zeile in der Oberfläche.
	Was string `json:"was"`
	// Stufe ist "gut", "warnung" oder "kritisch". Kritisch heißt: so kommt
	// keine Post an oder keine geht raus.
	Stufe string `json:"stufe"`
	// Text sagt, was gefunden wurde. Rat sagt, was zu tun wäre — leer, wenn
	// nichts zu tun ist.
	Text string `json:"text"`
	Rat  string `json:"rat,omitempty"`
	// Domain ist gesetzt, wenn der Befund eine einzelne Domäne betrifft.
	Domain string `json:"domain,omitempty"`
}

const (
	BefundGut      = "gut"
	BefundWarnung  = "warnung"
	BefundKritisch = "kritisch"
)

// MailCheck ist die ganze Prüfung.
type MailCheck struct {
	Hostname  string   `json:"hostname"`
	PublicIPs []string `json:"public_ips"`
	Befunde   []Befund `json:"befunde"`
}

// Check prüft, was der Zustellbarkeit im Weg steht.
func (s *MailService) Check(ctx context.Context, sc store.Scope) (*MailCheck, error) {
	facts, err := s.agent.MailFactsOf(ctx)
	if err != nil {
		return nil, err
	}
	res := &MailCheck{Hostname: facts.Hostname, PublicIPs: facts.PublicIPs, Befunde: []Befund{}}

	res.pruefeServer(ctx, facts)

	domains, err := s.store.ListMailDomains(ctx, sc)
	if err != nil {
		return nil, err
	}
	for _, d := range domains {
		if !d.Active {
			continue
		}
		res.pruefeDomain(ctx, d, facts)
	}
	return res, nil
}

// pruefeServer prüft, was für alle Domänen gilt.
func (c *MailCheck) pruefeServer(ctx context.Context, f *agent.MailFacts) {
	// Horcht überhaupt etwas?
	fehlend := []string{}
	for _, p := range []int{25, 587, 993} {
		if !enthaeltInt(f.Listening, p) {
			fehlend = append(fehlend, fmt.Sprint(p))
		}
	}
	switch {
	case len(fehlend) == 3:
		c.add("Dienste", BefundKritisch, "Auf 25, 587 und 993 antwortet nichts.",
			"Postfix und Dovecot müssen laufen — `mail.setup` richtet sie ein.")
	case len(fehlend) > 0:
		c.add("Dienste", BefundWarnung,
			"Auf "+strings.Join(fehlend, ", ")+" antwortet nichts.",
			"25 nimmt Post von anderen Servern an, 587 die Einlieferung durch "+
				"Kunden, 993 den Abruf. Was fehlt, fehlt jemandem.")
	default:
		c.add("Dienste", BefundGut, "25, 587 und 993 antworten.", "")
	}

	// TLS
	if f.TLSCert == "" {
		c.add("TLS", BefundWarnung, "Postfix hat kein Zertifikat.",
			"Ohne TLS geht das Passwort eines Kunden im Klartext über das Netz, "+
				"und viele Anbieter werten die Zustellung ab.")
	} else {
		c.add("TLS", BefundGut, "Zertifikat: "+f.TLSCert, "")
	}

	// Offenes Relay — als Konfigurationsprüfung, nicht als Versuch von außen.
	// Ein echter Test müsste sich von einem fremden Netz aus einliefern
	// lassen; das kann das Panel nicht, und ein Test von innen sagt nichts.
	if !strings.Contains(f.RelayRestrictions, "reject_unauth_destination") {
		c.add("Relay", BefundKritisch,
			"In smtpd_relay_restrictions fehlt reject_unauth_destination.",
			"So nimmt der Server Post für fremde Ziele an und schickt sie weiter. "+
				"Die IP steht dann binnen Stunden auf jeder Sperrliste.")
	} else {
		c.add("Relay", BefundGut, "Weiterleitung nur für ausgewiesene Absender.", "")
	}

	// PTR: der Rückwärtseintrag der eigenen Adresse.
	if len(f.PublicIPs) == 0 {
		c.add("PTR", BefundWarnung, "Keine öffentliche Adresse an einer Schnittstelle.",
			"Der Server steht vermutlich hinter NAT. Welche Adresse die Welt sieht "+
				"und ob ihr PTR-Eintrag passt, lässt sich von hier nicht sagen.")
		return
	}
	for _, roh := range f.PublicIPs {
		c.pruefePTR(ctx, roh, f.Hostname)
	}
}

// pruefePTR sieht nach, ob die Adresse zurückzeigt — und zwar auf denselben
// Namen, unter dem sich der Server meldet.
//
// Das ist der Punkt, an dem die meiste Post scheitert. Ein Empfänger schlägt
// die Adresse rückwärts nach, bekommt den Namen, schlägt den vorwärts nach und
// erwartet dieselbe Adresse. Fehlt einer der beiden Schritte, gilt der Server
// als verdächtig — und keine Zeile Code ändert daran etwas.
func (c *MailCheck) pruefePTR(ctx context.Context, ip, hostname string) {
	namen, err := dnsAddr(ctx, ip)
	if err != nil || len(namen) == 0 {
		c.add("PTR", BefundKritisch, ip+" hat keinen Rückwärtseintrag.",
			"Beim Anbieter (Hetzner, Netcup, …) einen PTR auf "+hostname+" setzen. "+
				"Ohne ihn landet Post bei den großen Anbietern im Spam oder wird "+
				"gleich abgewiesen.")
		return
	}
	name := strings.TrimSuffix(namen[0], ".")

	// Vorwärts wieder zurück: ein PTR, der auf einen Namen zeigt, der woanders
	// hinführt, zählt nicht.
	adressen, err := dnsHost(ctx, name)
	if err != nil || !enthaelt(adressen, ip) {
		c.add("PTR", BefundWarnung, ip+" zeigt auf "+name+", der Name aber nicht zurück.",
			"Der Empfänger prüft beides. Solange der Vorwärtseintrag fehlt, hilft "+
				"der PTR nichts.")
		return
	}
	if hostname != "" && !strings.EqualFold(name, hostname) {
		c.add("PTR", BefundWarnung,
			ip+" zeigt auf "+name+", der Server meldet sich als "+hostname+".",
			"Beide sollten übereinstimmen — `postconf -e myhostname="+name+"` oder "+
				"den PTR ändern.")
		return
	}
	c.add("PTR", BefundGut, ip+" ⇄ "+name, "")
}

// pruefeDomain prüft die DNS-Einträge einer Maildomäne.
func (c *MailCheck) pruefeDomain(ctx context.Context, d *store.MailDomain, f *agent.MailFacts) {
	// MX: zeigt die Domäne überhaupt hierher?
	mx, err := dnsMX(ctx, d.Domain)
	switch {
	case err != nil || len(mx) == 0:
		c.addDomain(d.Domain, "MX", BefundKritisch, "Kein MX-Eintrag.",
			"Ohne ihn schickt niemand Post an diese Domäne. Ziel ist "+
				orElseHost(f.Hostname, "dieser Server")+".")
	case !zeigtHierher(ctx, mx, f.PublicIPs):
		ziele := make([]string, 0, len(mx))
		for _, m := range mx {
			ziele = append(ziele, strings.TrimSuffix(m.Host, "."))
		}
		c.addDomain(d.Domain, "MX", BefundWarnung,
			"Der MX zeigt auf "+strings.Join(ziele, ", ")+", nicht hierher.",
			"Post für diese Domäne kommt woanders an. Das kann gewollt sein — "+
				"während eines Umzugs etwa.")
	default:
		c.addDomain(d.Domain, "MX", BefundGut, "Der MX zeigt hierher.", "")
	}

	txt := txtEintraege(ctx, d.Domain)

	// SPF
	var spf string
	for _, t := range txt {
		if strings.HasPrefix(strings.ToLower(t), "v=spf1") {
			spf = t
		}
	}
	switch {
	case spf == "":
		c.addDomain(d.Domain, "SPF", BefundWarnung, "Kein SPF-Eintrag.",
			"Empfehlung: TXT auf "+d.Domain+" mit \"v=spf1 mx -all\" — dann gilt "+
				"der MX als berechtigter Absender und sonst niemand.")
	case strings.Contains(spf, "+all"):
		c.addDomain(d.Domain, "SPF", BefundKritisch, "Der SPF-Eintrag endet auf +all.",
			"Damit darf jeder im Namen dieser Domäne senden. \"-all\" oder \"~all\".")
	default:
		c.addDomain(d.Domain, "SPF", BefundGut, spf, "")
	}

	// DKIM: der Eintrag im DNS muss zu dem passen, was hier liegt.
	switch {
	case d.DKIMPublic == "":
		c.addDomain(d.Domain, "DKIM", BefundWarnung, "Kein Schlüssel erzeugt.",
			"Ohne DKIM landet Post bei den großen Anbietern häufiger im Spam.")
	default:
		c.pruefeDKIM(ctx, d)
	}

	// DMARC
	dmarc := txtEintraege(ctx, "_dmarc."+d.Domain)
	var gefunden bool
	for _, t := range dmarc {
		if strings.HasPrefix(strings.ToLower(t), "v=dmarc1") {
			gefunden = true
			c.addDomain(d.Domain, "DMARC", BefundGut, t, "")
		}
	}
	if !gefunden {
		c.addDomain(d.Domain, "DMARC", BefundWarnung, "Kein DMARC-Eintrag.",
			"Empfehlung: TXT auf _dmarc."+d.Domain+" mit "+
				"\"v=DMARC1; p=none; rua=mailto:postmaster@"+d.Domain+"\". "+
				"p=none sammelt erst einmal Berichte, ohne etwas abzuweisen.")
	}
}

// pruefeDKIM vergleicht den DNS-Eintrag mit dem hinterlegten Schlüssel.
//
// Der Vergleich ist der Punkt: ein DKIM-Eintrag, der zu einem anderen
// Schlüssel gehört — weil jemand ihn neu erzeugt und den alten stehen gelassen
// hat —, ist schlechter als keiner. Eine kaputte Unterschrift wertet eine Mail
// ab, eine fehlende wertet sie nur nicht auf.
func (c *MailCheck) pruefeDKIM(ctx context.Context, d *store.MailDomain) {
	name := d.DKIMSelector + "._domainkey." + d.Domain
	eintraege := txtEintraege(ctx, name)
	if len(eintraege) == 0 {
		c.addDomain(d.Domain, "DKIM", BefundWarnung, "Der DNS-Eintrag fehlt.",
			"TXT auf "+name+" anlegen — der Wert steht im Panel bei der Domäne.")
		return
	}
	for _, t := range eintraege {
		if strings.Contains(t, "p="+d.DKIMPublic) {
			c.addDomain(d.Domain, "DKIM", BefundGut, "Der Eintrag passt zum Schlüssel.", "")
			return
		}
	}
	c.addDomain(d.Domain, "DKIM", BefundKritisch,
		"Der DNS-Eintrag gehört zu einem anderen Schlüssel.",
		"Ein falscher DKIM-Eintrag ist schlechter als keiner: die Unterschrift "+
			"schlägt fehl, statt zu fehlen. Den Wert aus dem Panel neu eintragen.")
}

func (c *MailCheck) add(was, stufe, text, rat string) {
	c.Befunde = append(c.Befunde, Befund{Was: was, Stufe: stufe, Text: text, Rat: rat})
}

func (c *MailCheck) addDomain(domain, was, stufe, text, rat string) {
	c.Befunde = append(c.Befunde, Befund{
		Was: was, Stufe: stufe, Text: text, Rat: rat, Domain: domain,
	})
}

// txtEintraege holt die TXT-Einträge eines Namens; Fehler ergeben eine leere
// Liste. Ein DNS, das gerade nicht antwortet, ist kein Befund über die Domäne.
func txtEintraege(ctx context.Context, name string) []string {
	out, err := dnsTXT(ctx, name)
	if err != nil {
		return nil
	}
	return out
}

// zeigtHierher sagt, ob einer der MX-Einträge auf eine unserer Adressen zeigt.
func zeigtHierher(ctx context.Context, mx []*net.MX, eigene []string) bool {
	for _, m := range mx {
		adressen, err := dnsHost(ctx, strings.TrimSuffix(m.Host, "."))
		if err != nil {
			continue
		}
		for _, a := range adressen {
			if enthaelt(eigene, a) {
				return true
			}
		}
	}
	return false
}

func enthaelt(liste []string, wert string) bool {
	gesucht, err := netip.ParseAddr(wert)
	for _, e := range liste {
		if e == wert {
			return true
		}
		if err == nil {
			if a, err2 := netip.ParseAddr(e); err2 == nil && a.Unmap() == gesucht.Unmap() {
				return true
			}
		}
	}
	return false
}

func enthaeltInt(liste []int, wert int) bool {
	for _, e := range liste {
		if e == wert {
			return true
		}
	}
	return false
}

func orElseHost(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}
