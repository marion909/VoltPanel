package core

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"

	"github.com/marion909/voltpanel/internal/agent"
	"github.com/marion909/voltpanel/internal/authn"
	"github.com/marion909/voltpanel/internal/config"
	"github.com/marion909/voltpanel/internal/store"
	"github.com/marion909/voltpanel/internal/templates"
)

// MailService verwaltet Domänen, Postfächer und Weiterleitungen.
//
// Die Besonderheit gegenüber allem anderen in diesem Panel: die Dateien, die
// Postfix und Dovecot lesen, gelten für den ganzen Server. Ein Vhost gehört zu
// einer Site; eine Postfix-Map kennt alle Adressen aller Mandanten.
//
// Daraus folgt der Ablauf hier. Geändert wird im Scope des Aufrufers — wer
// eine Domäne anlegt, legt sie unter seiner eigenen tenant_id an, und wer eine
// fremde ändern will, findet sie nicht. Geschrieben wird danach der ganze
// Stand, eingesammelt im Systemscope. Beides zu vermischen wäre der Fehler:
// wer den Scope des Aufrufers zum Schreiben nähme, löschte mit jeder Änderung
// die Postfächer aller anderen aus der Datei.
type MailService struct {
	store   *store.Store
	agent   *agent.Client
	cfg     *config.Config
	secrets *authn.SecretBox
	quota   *QuotaService
	certs   *CertService
}

func NewMailService(st *store.Store, ag *agent.Client, cfg *config.Config,
	secrets *authn.SecretBox) *MailService {

	return &MailService{
		store: st, agent: ag, cfg: cfg, secrets: secrets,
		quota: NewQuotaService(st, ag, cfg, nil),
		certs: NewCertService(cfg, st, ag, secrets, nil),
	}
}

// Status sagt, was auf diesem Server bereitsteht.
func (s *MailService) Status(ctx context.Context) (*agent.MailStatus, error) {
	return s.agent.MailStatusOf(ctx)
}

// SpamStats sagt, was Rspamd tatsächlich aussortiert.
func (s *MailService) SpamStats(ctx context.Context) (*agent.RspamdStats, error) {
	return s.agent.RspamdStatsOf(ctx)
}

// Setup richtet den Mailspeicher ein.
//
// Wie bei FTP nicht bei der Installation: ein Mailserver gehört nicht auf
// jeden Server, und einer, der nur läuft, weil er mitinstalliert wurde, ist
// offene Angriffsfläche ohne Nutzen — bei Mail sogar ein offenes Relay in
// spe.
func (s *MailService) Setup(ctx context.Context) (string, error) {
	out, err := s.agent.MailSetup(ctx)
	if err != nil {
		return "", err
	}
	// Direkt danach den vorhandenen Stand schreiben: nach einem Restore stehen
	// die Zeilen schon in der Datenbank, und leere Maps daneben wären der
	// Zustand, in dem alle Postfächer verschwunden sind.
	if _, err := s.Apply(ctx); err != nil {
		return out, fmt.Errorf("%s — der stand konnte aber nicht geschrieben werden: %w", out, err)
	}
	return out, nil
}

// --- Domänen ---------------------------------------------------------------

// CreateDomain legt eine Maildomäne an.
func (s *MailService) CreateDomain(ctx context.Context, sc store.Scope,
	tenantID int64, domain string) (*store.MailDomain, error) {

	d := &store.MailDomain{TenantID: tenantID, Domain: domain, Active: true}
	if err := s.store.CreateMailDomain(ctx, sc, d); err != nil {
		return nil, err
	}
	if _, err := s.Apply(ctx); err != nil {
		return d, err
	}
	return d, nil
}

// SetDomain ändert, was an einer Domäne einstellbar ist.
func (s *MailService) SetDomain(ctx context.Context, sc store.Scope, id int64,
	active *bool, catchAll *string) (*store.MailDomain, error) {

	d, err := s.store.GetMailDomain(ctx, sc, id)
	if err != nil {
		return nil, err
	}
	if active != nil {
		d.Active = *active
	}
	if catchAll != nil {
		ziel := strings.TrimSpace(*catchAll)
		// Ein Catch-All darf nur auf ein eigenes Postfach zeigen. Sonst wäre
		// er eine Weiterleitung fremder Post an eine beliebige Adresse — und
		// der Absender sähe nur, dass die Mail angenommen wurde.
		if ziel != "" {
			if err := s.zielGehoertDazu(ctx, sc, d.TenantID, ziel); err != nil {
				return nil, err
			}
		}
		d.CatchAll = ziel
	}
	if err := s.store.UpdateMailDomain(ctx, sc, d); err != nil {
		return nil, err
	}
	_, err = s.Apply(ctx)
	return d, err
}

// DeleteDomain entfernt eine Domäne samt Postfächern und Aliasen.
//
// Die Maildirs auf der Platte bleiben stehen. Das ist Absicht: eine gelöschte
// Domäne ist oft ein Versehen, und Post ist das eine, was sich nicht
// nachbauen lässt. Aufgeräumt wird von Hand — mit einem Hinweis, wo.
func (s *MailService) DeleteDomain(ctx context.Context, sc store.Scope, id int64) (string, error) {
	d, err := s.store.GetMailDomain(ctx, sc, id)
	if err != nil {
		return "", err
	}
	if err := s.store.DeleteMailDomain(ctx, sc, id); err != nil {
		return "", err
	}
	if _, err := s.Apply(ctx); err != nil {
		return "", err
	}
	return fmt.Sprintf("die post von %s liegt weiterhin unter /var/vmail/%s "+
		"und muss von Hand entfernt werden", d.Domain, d.Domain), nil
}

// --- Postfächer ------------------------------------------------------------

// CreateMailbox legt ein Postfach an.
func (s *MailService) CreateMailbox(ctx context.Context, sc store.Scope,
	domainID int64, localPart, password string, quotaMB int64) (*store.Mailbox, error) {

	dom, err := s.store.GetMailDomain(ctx, sc, domainID)
	if err != nil {
		return nil, err
	}
	if err := s.quota.CheckCount(ctx, sc, dom.TenantID, ResourceMailboxes); err != nil {
		return nil, err
	}
	if err := pruefePasswort(password); err != nil {
		return nil, err
	}
	enc, err := s.secrets.Encrypt(password)
	if err != nil {
		return nil, err
	}

	m := &store.Mailbox{
		TenantID: dom.TenantID, DomainID: dom.ID, LocalPart: localPart,
		PasswordEnc: enc, QuotaMB: quotaMB, Active: true,
	}
	if err := s.store.CreateMailbox(ctx, sc, m); err != nil {
		return nil, err
	}
	if _, err := s.Apply(ctx); err != nil {
		return m, err
	}
	return m, nil
}

// SetMailbox ändert Passwort, Quota oder Zustand.
func (s *MailService) SetMailbox(ctx context.Context, sc store.Scope, id int64,
	password string, quotaMB *int64, active *bool) error {

	m, err := s.store.GetMailbox(ctx, sc, id)
	if err != nil {
		return err
	}
	if password != "" {
		if err := pruefePasswort(password); err != nil {
			return err
		}
		if m.PasswordEnc, err = s.secrets.Encrypt(password); err != nil {
			return err
		}
	}
	if quotaMB != nil {
		m.QuotaMB = *quotaMB
	}
	if active != nil {
		m.Active = *active
	}
	if err := s.store.UpdateMailbox(ctx, sc, m); err != nil {
		return err
	}
	_, err = s.Apply(ctx)
	return err
}

// Reveal gibt das Passwort eines Postfachs heraus.
//
// Anders als bei einem Panel-Konto und aus demselben Grund wie bei FTP: ein
// Mailkonto wird in einem Mailprogramm eingetragen, und "wie war noch mein
// Passwort" ist dort eine echte Frage. Wer sie stellt, muss ohnehin Zugriff
// auf den Mandanten haben — sonst findet er das Postfach nicht.
func (s *MailService) Reveal(ctx context.Context, sc store.Scope, id int64) (string, error) {
	m, err := s.store.GetMailbox(ctx, sc, id)
	if err != nil {
		return "", err
	}
	if m.PasswordEnc == "" {
		return "", errors.New("für dieses postfach ist kein passwort hinterlegt")
	}
	return s.secrets.Decrypt(m.PasswordEnc)
}

// DeleteMailbox entfernt ein Postfach.
func (s *MailService) DeleteMailbox(ctx context.Context, sc store.Scope, id int64) (string, error) {
	m, err := s.store.GetMailbox(ctx, sc, id)
	if err != nil {
		return "", err
	}
	if err := s.store.DeleteMailbox(ctx, sc, id); err != nil {
		return "", err
	}
	if _, err := s.Apply(ctx); err != nil {
		return "", err
	}
	return fmt.Sprintf("die post von %s bleibt auf der platte liegen", m.Address), nil
}

// --- Weiterleitungen -------------------------------------------------------

func (s *MailService) CreateAlias(ctx context.Context, sc store.Scope,
	domainID int64, source, destination string) (*store.MailAlias, error) {

	dom, err := s.store.GetMailDomain(ctx, sc, domainID)
	if err != nil {
		return nil, err
	}
	a := &store.MailAlias{
		TenantID: dom.TenantID, DomainID: dom.ID,
		Source: source, Destination: destination, Active: true,
	}
	if err := s.store.CreateMailAlias(ctx, sc, a); err != nil {
		return nil, err
	}
	if _, err := s.Apply(ctx); err != nil {
		return a, err
	}
	return a, nil
}

func (s *MailService) DeleteAlias(ctx context.Context, sc store.Scope, id int64) error {
	if err := s.store.DeleteMailAlias(ctx, sc, id); err != nil {
		return err
	}
	_, err := s.Apply(ctx)
	return err
}

// --- Schreiben -------------------------------------------------------------

// Apply schreibt den ganzen Stand des Servers in die Map-Dateien.
//
// Im Systemscope, und das ist hier richtig: die Dateien gelten für den ganzen
// Server. Nähme man den Scope des Aufrufers, verschwänden mit jeder Änderung
// die Postfächer aller anderen Mandanten aus der Datei — und zwar lautlos, bis
// jemandem auffällt, dass seine Post abgewiesen wird.
func (s *MailService) Apply(ctx context.Context) (string, error) {
	p, err := s.collect(ctx)
	if err != nil {
		return "", err
	}
	return s.agent.ApplyMail(ctx, p)
}

// collect stellt zusammen, was in die Dateien gehört.
//
// Getrennt vom Schicken, damit sich prüfen lässt, *was* geschrieben würde —
// ohne einen Postfix auf der Maschine. Der Fehler, um den es geht, ist nicht
// im Schreiben, sondern im Einsammeln.
func (s *MailService) collect(ctx context.Context) (agent.MailApplyParams, error) {
	sys := store.SystemScope()

	var p agent.MailApplyParams

	domains, err := s.store.ListMailDomains(ctx, sys)
	if err != nil {
		return p, err
	}
	boxen, err := s.store.ListMailboxes(ctx, sys, 0)
	if err != nil {
		return p, err
	}
	aliase, err := s.store.ListMailAliases(ctx, sys, 0)
	if err != nil {
		return p, err
	}

	// Ein gesperrter Mandant nimmt keine Post mehr an. Dieselbe Regel wie beim
	// Anmelden: "gesperrt" soll nicht nur ein Feld in der Oberfläche sein.
	gesperrt, err := s.gesperrteMandanten(ctx)
	if err != nil {
		return p, err
	}

	aktiveDomain := map[int64]string{}
	for _, d := range domains {
		if !d.Active || gesperrt[d.TenantID] {
			continue
		}
		aktiveDomain[d.ID] = d.Domain
		p.Domains = append(p.Domains, d.Domain)
		if d.CatchAll != "" {
			// "@domain" ist die Schreibweise, die Postfix als Catch-All liest.
			p.Aliases = append(p.Aliases, agent.MailAliasParams{
				Source: "@" + d.Domain, Destination: d.CatchAll,
			})
		}
	}

	for _, m := range boxen {
		if !m.Active || aktiveDomain[m.DomainID] == "" {
			continue
		}
		klartext, err := s.secrets.Decrypt(m.PasswordEnc)
		if err != nil {
			// Ein Postfach ohne lesbares Passwort wegzulassen wäre die stille
			// Variante; es mit leerem Passwort zu schreiben die gefährliche.
			// Also weglassen und es sagen.
			return p, fmt.Errorf("das passwort von %s ist nicht lesbar: %w", m.Address, err)
		}
		p.Mailboxes = append(p.Mailboxes, agent.MailboxParams{
			Address: m.Address, Password: klartext, QuotaMB: m.QuotaMB,
		})
	}

	for _, a := range aliase {
		if !a.Active || aktiveDomain[a.DomainID] == "" {
			continue
		}
		p.Aliases = append(p.Aliases, agent.MailAliasParams{
			Source: a.Source, Destination: a.Destination,
		})
	}

	// Die DKIM-Schlüssel zuletzt. Der private Teil geht im Klartext an den
	// Agent — wie ein Mailpasswort, und aus demselben Grund: er muss in eine
	// Datei, die OpenDKIM lesen kann, und der Agent ist der einzige, der dort
	// schreiben darf.
	for _, d := range domains {
		if aktiveDomain[d.ID] == "" || d.DKIMPrivate == "" {
			continue
		}
		schluessel, err := s.secrets.Decrypt(d.DKIMPrivate)
		if err != nil {
			return p, fmt.Errorf("der dkim-schlüssel von %s ist nicht lesbar: %w", d.Domain, err)
		}
		p.DKIM = append(p.DKIM, agent.DKIMParams{
			Domain: d.Domain, Selector: d.DKIMSelector, PrivateKey: schluessel,
		})
	}

	return p, nil
}

// gesperrteMandanten sind die, deren Post nicht mehr angenommen wird.
func (s *MailService) gesperrteMandanten(ctx context.Context) (map[int64]bool, error) {
	tenants, err := s.store.ListTenants(ctx, store.SystemScope())
	if err != nil {
		return nil, err
	}
	out := map[int64]bool{}
	for _, t := range tenants {
		if t.Status != store.TenantActive {
			out[t.ID] = true
		}
	}
	return out, nil
}

// zielGehoertDazu prüft, ob eine Adresse dem Mandanten gehört.
func (s *MailService) zielGehoertDazu(ctx context.Context, sc store.Scope,
	tenantID int64, adresse string) error {

	if !store.ValidMailAddress(strings.ToLower(adresse)) {
		return fmt.Errorf("%q ist keine adresse", adresse)
	}
	boxen, err := s.store.ListMailboxes(ctx, sc, 0)
	if err != nil {
		return err
	}
	for _, m := range boxen {
		if m.TenantID == tenantID && strings.EqualFold(m.Address, adresse) {
			return nil
		}
	}
	return fmt.Errorf("%s ist kein postfach dieses mandanten", adresse)
}

// pruefePasswort ist die Mindestanforderung an ein Mailpasswort.
//
// Kürzer als beim Panel-Konto und ohne Zeichenklassen: ein Mailpasswort steht
// im Mailprogramm und wird nicht getippt. Was hier zählt, ist Länge — und
// dass es nicht leer ist, denn ein leeres Feld in der Dovecot-Datei ließe je
// nach Einstellung jeden herein.
func pruefePasswort(p string) error {
	if len([]rune(p)) < 10 {
		return errors.New("ein mailpasswort braucht mindestens 10 zeichen")
	}
	if strings.ContainsAny(p, "\n\r\x00") {
		return errors.New("ein passwort enthält keine zeilenumbrüche")
	}
	return nil
}

// MailSettings ist, was ein Kunde in sein Mailprogramm einträgt.
//
// Eine eigene Auskunft und nicht Teil des Zustandsberichts: den darf nur ein
// Administrator sehen, diese hier braucht jeder, der ein Postfach hat. Ohne
// sie steht ein Kunde vor einem angelegten Postfach und weiß nicht, wohin
// damit — der häufigste Grund für eine Rückfrage, die es nicht bräuchte.
type MailSettings struct {
	Host string `json:"host"`
	// IMAP über TLS, SMTP über STARTTLS auf der Einlieferung. Beides sind
	// keine Einstellungen, sondern das, was mail.setup eingerichtet hat.
	IMAPPort int    `json:"imap_port"`
	IMAPEnc  string `json:"imap_encryption"`
	SMTPPort int    `json:"smtp_port"`
	SMTPEnc  string `json:"smtp_encryption"`
	// Username sagt, was in das Feld gehört: die ganze Adresse, nicht der
	// Teil davor.
	Username string `json:"username"`
}

// Settings sagt, wie ein Mailprogramm sich verbindet.
func (s *MailService) Settings(ctx context.Context) *MailSettings {
	host := ""
	if facts, err := s.agent.MailFactsOf(ctx); err == nil {
		host = strings.TrimSpace(facts.Hostname)
	}
	if host == "" {
		// Ohne Auskunft vom Server der Name des Panels: er zeigt hierher, und
		// das Zertifikat gilt für ihn. Geraten wird nichts.
		host = s.cfg.PanelDomain
	}
	return &MailSettings{
		Host:     host,
		IMAPPort: 993, IMAPEnc: "SSL/TLS",
		SMTPPort: 587, SMTPEnc: "STARTTLS",
		Username: "die vollständige Mailadresse",
	}
}

// --- DKIM -------------------------------------------------------------------

// DKIMInfo ist der DNS-Eintrag, den eine Domäne braucht.
type DKIMInfo struct {
	Domain   string `json:"domain"`
	Selector string `json:"selector"`
	// Name ist der Name des TXT-Eintrags, Value sein Inhalt. Beides so, wie es
	// bei einem DNS-Anbieter eingetragen wird.
	Name  string `json:"name"`
	Value string `json:"value"`
}

// dkimSelector ist der Name, unter dem der Schlüssel im DNS steht.
//
// Fest und nicht einstellbar: er landet in einem DNS-Namen, in der KeyTable
// und im Dateipfad des Schlüssels. Ein Eingabefeld dafür wäre drei Wege, an
// denen etwas schiefgehen kann, für einen Namen, den niemand je liest.
const dkimSelector = "volt"

// EnableDKIM erzeugt einen Schlüssel für eine Domäne.
//
// Der private Teil bleibt in der Panel-Datenbank, verschlüsselt, und geht von
// dort an den Agent — wie ein Mailpasswort. Wer ihn hat, unterschreibt Mail im
// Namen dieser Domäne; er soll deshalb nicht daneben im Klartext liegen.
//
// 2048 Bit RSA. Nicht ed25519: das kann kaum ein empfangender Server prüfen,
// und ein DKIM, das niemand prüft, ist keines. Nicht 4096: der öffentliche
// Teil passt dann nicht mehr in einen TXT-Eintrag, ohne ihn zu teilen, und
// mancher DNS-Anbieter macht das falsch.
func (s *MailService) EnableDKIM(ctx context.Context, sc store.Scope, domainID int64) (
	*DKIMInfo, error) {

	d, err := s.store.GetMailDomain(ctx, sc, domainID)
	if err != nil {
		return nil, err
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("schlüssel erzeugen: %w", err)
	}
	privPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return nil, err
	}

	enc, err := s.secrets.Encrypt(string(privPEM))
	if err != nil {
		return nil, err
	}
	d.DKIMSelector = dkimSelector
	d.DKIMPrivate = enc
	d.DKIMPublic = base64.StdEncoding.EncodeToString(pubDER)
	if err := s.store.UpdateMailDomain(ctx, sc, d); err != nil {
		return nil, err
	}
	if _, err := s.Apply(ctx); err != nil {
		return s.dkimInfo(d), err
	}
	return s.dkimInfo(d), nil
}

// DKIMOf liefert den DNS-Eintrag einer Domäne, falls sie einen Schlüssel hat.
func (s *MailService) DKIMOf(ctx context.Context, sc store.Scope, domainID int64) (
	*DKIMInfo, error) {

	d, err := s.store.GetMailDomain(ctx, sc, domainID)
	if err != nil {
		return nil, err
	}
	if d.DKIMPublic == "" {
		return nil, errors.New("für diese domäne gibt es noch keinen dkim-schlüssel")
	}
	return s.dkimInfo(d), nil
}

func (s *MailService) dkimInfo(d *store.MailDomain) *DKIMInfo {
	return &DKIMInfo{
		Domain:   d.Domain,
		Selector: d.DKIMSelector,
		Name:     d.DKIMSelector + "._domainkey." + d.Domain,
		// h=sha256 ausgeschrieben: manche Prüfer nehmen sonst an, jeder
		// Algorithmus sei erlaubt, und das ist schwächer als gemeint.
		Value: "v=DKIM1; h=sha256; k=rsa; p=" + d.DKIMPublic,
	}
}

// --- DNS über Cloudflare ----------------------------------------------------

// DNSErgebnis sagt je Eintrag, was geschehen ist.
type DNSErgebnis struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Text   string `json:"text"`
}

// PublishDNS setzt die Einträge, die zu einer Maildomäne gehören.
//
// Drei Einträge, drei verschiedene Regeln:
//
//   - DKIM gehört dem Panel. Es hat den Schlüssel erzeugt, es kennt den
//     richtigen Wert, und ein alter Eintrag daneben wäre schädlich — also
//     überschreiben.
//   - SPF gehört dem Kunden. Steht schon einer da, bleibt er: er zählt
//     womöglich einen Newsletter-Versand auf, und ihn zu ersetzen sperrte den
//     aus. Nur wenn keiner da ist, wird einer angelegt.
//   - DMARC ebenso, und mit p=none: das sammelt Berichte, ohne etwas
//     abzuweisen. Eine schärfere Regel gehört dem, der die Berichte gelesen
//     hat.
func (s *MailService) PublishDNS(ctx context.Context, sc store.Scope, domainID int64) (
	[]DNSErgebnis, error) {

	d, err := s.store.GetMailDomain(ctx, sc, domainID)
	if err != nil {
		return nil, err
	}
	tenant, err := s.store.GetTenant(ctx, sc, d.TenantID)
	if err != nil {
		return nil, err
	}
	if tenant.CloudflareToken == "" {
		return nil, errors.New("für diesen mandanten ist kein cloudflare-token hinterlegt — " +
			"die einträge stehen im panel und lassen sich von hand eintragen")
	}
	token, err := s.secrets.Decrypt(tenant.CloudflareToken)
	if err != nil {
		return nil, fmt.Errorf("cloudflare-token: %w", err)
	}

	cf := newCloudflareClient(token)
	zone, err := cf.zoneID(ctx, d.Domain)
	if err != nil {
		return nil, err
	}

	var out []DNSErgebnis
	melde := func(name, status, text string) {
		out = append(out, DNSErgebnis{Name: name, Status: status, Text: text})
	}

	// DKIM
	if d.DKIMPublic == "" {
		melde("DKIM", BefundWarnung, "Für diese Domäne gibt es noch keinen Schlüssel.")
	} else {
		info := s.dkimInfo(d)
		if err := cf.setzeTXT(ctx, zone, info.Name, info.Value); err != nil {
			melde("DKIM", BefundKritisch, err.Error())
		} else {
			melde("DKIM", BefundGut, info.Name+" gesetzt.")
		}
	}

	// SPF — nur, wenn keiner dasteht.
	spf, err := cf.txtRecords(ctx, zone, d.Domain)
	if err != nil {
		melde("SPF", BefundKritisch, err.Error())
	} else if vorhandenesSPF(spf) != "" {
		melde("SPF", BefundGut, "Es steht schon einer da: "+vorhandenesSPF(spf)+
			" — unverändert gelassen.")
	} else if err := cf.setzeTXT(ctx, zone, d.Domain, "v=spf1 mx -all"); err != nil {
		melde("SPF", BefundKritisch, err.Error())
	} else {
		melde("SPF", BefundGut, "v=spf1 mx -all gesetzt — der MX darf senden, sonst niemand.")
	}

	// DMARC — ebenso.
	dmarcName := "_dmarc." + d.Domain
	dmarc, err := cf.txtRecords(ctx, zone, dmarcName)
	if err != nil {
		melde("DMARC", BefundKritisch, err.Error())
	} else if len(dmarc) > 0 {
		melde("DMARC", BefundGut, "Es steht schon einer da — unverändert gelassen.")
	} else {
		wert := "v=DMARC1; p=none; rua=mailto:postmaster@" + d.Domain
		if err := cf.setzeTXT(ctx, zone, dmarcName, wert); err != nil {
			melde("DMARC", BefundKritisch, err.Error())
		} else {
			melde("DMARC", BefundGut, "p=none gesetzt — sammelt Berichte, weist nichts ab.")
		}
	}

	return out, nil
}

// PublishAutoconfig richtet Autokonfiguration für Thunderbird und Outlook ein.
//
// Beide Programme fragen — bevor ein Kunde irgendetwas von Hand einträgt —
// eine feste Adresse: Thunderbird https://autoconfig.<domain>/mail/…,
// Outlook https://autodiscover.<domain>/autodiscover/…. Damit dort etwas
// antwortet, braucht es drei Dinge, in dieser Reihenfolge: den Inhalt (zwei
// generierte XML-Dateien, geschrieben über den Agent), ein Zertifikat für
// beide Namen (DNS-01, weil eine Nginx-Config ohne Zertifikat nicht gilt),
// und zuletzt den Vhost, der beides zusammenbringt. Die DNS-Einträge kommen
// als Letztes — sonst zeigte eine schon aufgelöste Adresse für einen Moment
// auf einen Server, der noch nicht antworten kann.
//
// Wie bei PublishDNS: kein Rückbau bei einem Fehler auf halbem Weg. Ein
// Zertifikat, das schon ausgestellt ist, wird beim nächsten Versuch einfach
// wiederverwendet — derselbe Ablauf wie beim Zertifikat einer Anmeldedomain.
func (s *MailService) PublishAutoconfig(ctx context.Context, sc store.Scope, domainID int64) (
	[]DNSErgebnis, error) {

	d, err := s.store.GetMailDomain(ctx, sc, domainID)
	if err != nil {
		return nil, err
	}
	tenant, err := s.store.GetTenant(ctx, sc, d.TenantID)
	if err != nil {
		return nil, err
	}
	if tenant.CloudflareToken == "" {
		return nil, errors.New("für diesen mandanten ist kein cloudflare-token hinterlegt — " +
			"ohne token lässt sich weder das zertifikat noch der dns-eintrag automatisch setzen")
	}
	token, err := s.secrets.Decrypt(tenant.CloudflareToken)
	if err != nil {
		return nil, fmt.Errorf("cloudflare-token: %w", err)
	}

	facts, err := s.agent.MailFactsOf(ctx)
	if err != nil {
		return nil, fmt.Errorf("serveradresse: %w", err)
	}
	if len(facts.PublicIPs) == 0 {
		return nil, errors.New("der server hat keine öffentliche adresse gemeldet — " +
			"ohne sie lässt sich kein dns-eintrag setzen")
	}
	ip := facts.PublicIPs[0]

	settings := s.Settings(ctx)
	autoconfigHost := "autoconfig." + d.Domain
	autodiscoverHost := "autodiscover." + d.Domain

	var out []DNSErgebnis
	melde := func(name, status, text string) {
		out = append(out, DNSErgebnis{Name: name, Status: status, Text: text})
	}

	mozillaPath, microsoftPath, err := s.agent.WriteMailAutoconfig(ctx, d.Domain, settings.Host,
		settings.IMAPPort, settings.SMTPPort)
	if err != nil {
		melde("Konfiguration", BefundKritisch, err.Error())
		return out, nil
	}
	melde("Konfiguration", BefundGut, "für "+d.Domain+" geschrieben.")

	cert, err := s.certs.Issue(ctx, sc, IssueOptions{
		Domains:         []string{autoconfigHost, autodiscoverHost},
		CloudflareToken: token, TenantID: d.TenantID,
	})
	if err != nil {
		melde("Zertifikat", BefundKritisch, err.Error())
		return out, nil
	}
	melde("Zertifikat", BefundGut, "für "+autoconfigHost+" und "+autodiscoverHost+" ausgestellt.")

	vhost, err := templates.RenderAutoconfigVhost(templates.AutoconfigVhostData{
		AutoconfigHost: autoconfigHost, AutodiscoverHost: autodiscoverHost,
		CertPath: cert.CertPath, KeyPath: cert.KeyPath,
		MozillaPath: mozillaPath, MicrosoftPath: microsoftPath,
	})
	if err != nil {
		melde("Vhost", BefundKritisch, err.Error())
		return out, nil
	}
	if err := s.agent.WriteVhost(ctx, autoconfigHost, vhost); err != nil {
		melde("Vhost", BefundKritisch, err.Error())
		return out, nil
	}
	melde("Vhost", BefundGut, "aktiv.")

	cf := newCloudflareClient(token)
	zone, err := cf.zoneID(ctx, d.Domain)
	if err != nil {
		melde("DNS", BefundKritisch, err.Error())
		return out, nil
	}
	for _, host := range []string{autoconfigHost, autodiscoverHost} {
		if err := cf.setzeA(ctx, zone, host, ip); err != nil {
			melde(host, BefundKritisch, err.Error())
		} else {
			melde(host, BefundGut, "zeigt jetzt auf "+ip+".")
		}
	}

	return out, nil
}

// vorhandenesSPF sucht den SPF-Eintrag unter den TXT-Einträgen einer Domäne.
func vorhandenesSPF(records []cfRecord) string {
	for _, r := range records {
		if strings.HasPrefix(strings.ToLower(r.Content), "v=spf1") {
			return r.Content
		}
	}
	return ""
}
