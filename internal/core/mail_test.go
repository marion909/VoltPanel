package core

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/marion909/voltpanel/internal/agent"
	"github.com/marion909/voltpanel/internal/store"
)

func mailService(env *testEnv) *MailService {
	return NewMailService(env.store, env.agent, env.cfg, env.secrets)
}

// Der eine Fehler, den dieser Dienst nicht machen darf.
//
// Die Postfix-Maps gelten für den ganzen Server. Wer sie im Scope des
// Aufrufers zusammenstellt, löscht mit jeder Änderung eines Mandanten die
// Postfächer aller anderen aus der Datei — lautlos, bis jemandem auffällt,
// dass seine Post abgewiesen wird.
func TestMailApplySammeltAlleMandanten(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	svc := mailService(env)

	alice := seedMailTenant(t, env, "alice")
	seedMailTenant(t, env, "bob")

	// Alice ändert etwas — in ihrem eigenen Scope.
	scA := store.Scope{TenantID: alice, Role: store.RoleOwner}
	domA := ersteDomain(t, env, alice)
	if _, err := svc.CreateMailbox(ctx, scA, domA, "neu", "ein-langes-passwort", 0); err != nil {
		// Ohne eingerichteten Mailspeicher scheitert das Schreiben. Die Zeile
		// steht trotzdem, und geprüft wird hier, was geschrieben *würde*.
		t.Logf("schreiben scheiterte (erwartbar ohne mail.setup): %v", err)
	}

	p, err := svc.collect(ctx)
	if err != nil {
		t.Fatal(err)
	}

	will := map[string]bool{
		"info@alice.example.at": false,
		"neu@alice.example.at":  false,
		"info@bob.example.at":   false,
	}
	for _, m := range p.Mailboxes {
		if _, ok := will[m.Address]; ok {
			will[m.Address] = true
		}
	}
	for adresse, drin := range will {
		if !drin {
			t.Errorf("%s fehlt in dem, was geschrieben würde: %+v", adresse, p.Mailboxes)
		}
	}
	if len(p.Domains) != 2 {
		t.Errorf("%d domänen, erwartet 2: %v", len(p.Domains), p.Domains)
	}
}

// Ein gesperrter Mandant nimmt keine Post mehr an — dieselbe Regel wie beim
// Anmelden. "Gesperrt" soll nicht nur ein Feld in der Oberfläche sein.
func TestGesperrterMandantBekommtKeinePost(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	svc := mailService(env)
	sys := store.SystemScope()

	alice := seedMailTenant(t, env, "alice")
	seedMailTenant(t, env, "bob")

	vorher, err := svc.collect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(vorher.Domains) != 2 {
		t.Fatalf("%d domänen vorher", len(vorher.Domains))
	}

	tenant, err := env.store.GetTenant(ctx, sys, alice)
	if err != nil {
		t.Fatal(err)
	}
	tenant.Status = store.TenantSuspended
	if err := env.store.UpdateTenant(ctx, sys, tenant); err != nil {
		t.Fatal(err)
	}

	nachher, err := svc.collect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range nachher.Domains {
		if d == "alice.example.at" {
			t.Error("die domäne des gesperrten mandanten nimmt weiter post an")
		}
	}
	for _, m := range nachher.Mailboxes {
		if m.Address == "info@alice.example.at" {
			t.Error("das postfach des gesperrten mandanten steht weiter in der datei")
		}
	}
	// Und bobs Postfach ist noch da — sonst hätte die Sperre alles erwischt.
	var bobDa bool
	for _, m := range nachher.Mailboxes {
		if m.Address == "info@bob.example.at" {
			bobDa = true
		}
	}
	if !bobDa {
		t.Error("die sperre hat auch den anderen mandanten erwischt")
	}
}

// Ein Catch-All darf nur auf ein eigenes Postfach zeigen. Sonst wäre er eine
// Weiterleitung fremder Post an eine beliebige Adresse — und der Absender
// sähe nur, dass die Mail angenommen wurde.
func TestCatchAllNurAufEigenePostfaecher(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	svc := mailService(env)

	alice := seedMailTenant(t, env, "alice")
	scA := store.Scope{TenantID: alice, Role: store.RoleOwner}
	domA := ersteDomain(t, env, alice)

	fremd := "wer-anders@example.com"
	if _, err := svc.SetDomain(ctx, scA, domA, nil, &fremd); err == nil {
		t.Error("ein catch-all auf eine fremde adresse wurde angenommen")
	}

	// Und auf das eigene Postfach geht es.
	if _, err := svc.CreateMailbox(ctx, scA, domA, "post", "ein-langes-passwort", 0); err != nil {
		t.Logf("schreiben scheiterte (erwartbar ohne postfix): %v", err)
	}
	// Beim eigenen Postfach geht es. Geprüft wird am Zustand und nicht an der
	// Fehlermeldung: das Schreiben scheitert auf einem Rechner ohne
	// Mailspeicher, die Zeile steht trotzdem.
	eigen := "post@alice.example.at"
	_, _ = svc.SetDomain(ctx, scA, domA, nil, &eigen)

	dom, err := env.store.GetMailDomain(ctx, scA, domA)
	if err != nil {
		t.Fatal(err)
	}
	if dom.CatchAll != eigen {
		t.Errorf("catch_all = %q, erwartet %q", dom.CatchAll, eigen)
	}

	// Und er landet als "@domain" in dem, was geschrieben würde.
	p, err := svc.collect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var gefunden bool
	for _, a := range p.Aliases {
		if a.Source == "@alice.example.at" && a.Destination == eigen {
			gefunden = true
		}
	}
	if !gefunden {
		t.Errorf("der catch-all fehlt in den weiterleitungen: %+v", p.Aliases)
	}
}

// Ein leeres Passwortfeld in der Dovecot-Datei ließe je nach Einstellung jeden
// herein. Also kommt es gar nicht erst so weit.
func TestMailPasswortMindestlaenge(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	svc := mailService(env)

	alice := seedMailTenant(t, env, "alice")
	scA := store.Scope{TenantID: alice, Role: store.RoleOwner}
	domA := ersteDomain(t, env, alice)

	for _, pw := range []string{"", "kurz", "123456789", "mit\nzeile"} {
		if _, err := svc.CreateMailbox(ctx, scA, domA, "test", pw, 0); err == nil {
			t.Errorf("%q wurde als passwort angenommen", pw)
		}
	}
}

// seedMailTenant legt einen Mandanten mit einer Maildomäne an.
func seedMailTenant(t *testing.T, env *testEnv, slug string) int64 {
	t.Helper()
	ctx := context.Background()
	sys := store.SystemScope()

	tenant := &store.Tenant{Name: slug, Slug: slug}
	if err := env.store.CreateTenant(ctx, sys, tenant); err != nil {
		t.Fatal(err)
	}
	dom := &store.MailDomain{TenantID: tenant.ID, Domain: slug + ".example.at", Active: true}
	if err := env.store.CreateMailDomain(ctx, sys, dom); err != nil {
		t.Fatal(err)
	}
	enc, err := env.secrets.Encrypt("ein-langes-passwort")
	if err != nil {
		t.Fatal(err)
	}
	box := &store.Mailbox{
		TenantID: tenant.ID, DomainID: dom.ID, LocalPart: "info",
		PasswordEnc: enc, Active: true,
	}
	if err := env.store.CreateMailbox(ctx, sys, box); err != nil {
		t.Fatal(err)
	}
	return tenant.ID
}

func ersteDomain(t *testing.T, env *testEnv, tenantID int64) int64 {
	t.Helper()
	liste, err := env.store.ListMailDomains(context.Background(),
		store.Scope{TenantID: tenantID, Role: store.RoleOwner})
	if err != nil || len(liste) == 0 {
		t.Fatalf("keine domäne für %d: %v", tenantID, err)
	}
	return liste[0].ID
}

// Der DKIM-Eintrag, den ein DNS-Anbieter bekommt.
//
// Geprüft wird, dass der öffentliche Teil wirklich zum privaten gehört — sonst
// unterschreibt der Server mit einem Schlüssel, den niemand prüfen kann, und
// das ist schlechter als gar keine Signatur: eine kaputte Unterschrift wertet
// eine Mail ab, eine fehlende nur nicht auf.
func TestDKIMSchluesselPaarPasstZusammen(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	svc := mailService(env)

	alice := seedMailTenant(t, env, "alice")
	scA := store.Scope{TenantID: alice, Role: store.RoleOwner}
	domA := ersteDomain(t, env, alice)

	info, err := svc.EnableDKIM(ctx, scA, domA)
	if err != nil {
		t.Logf("schreiben scheiterte (erwartbar ohne mail.setup): %v", err)
	}
	if info == nil {
		t.Fatal("kein dkim-eintrag zurückgegeben")
	}

	if info.Name != "volt._domainkey.alice.example.at" {
		t.Errorf("name = %q", info.Name)
	}
	if !strings.HasPrefix(info.Value, "v=DKIM1; h=sha256; k=rsa; p=") {
		t.Errorf("wert = %q", info.Value)
	}

	// Den privaten Teil aus der Datenbank holen und beide vergleichen.
	d, err := env.store.GetMailDomain(ctx, scA, domA)
	if err != nil {
		t.Fatal(err)
	}
	privPEM, err := env.secrets.Decrypt(d.DKIMPrivate)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(d.DKIMPrivate, "PRIVATE KEY") {
		t.Error("der private schlüssel liegt unverschlüsselt in der datenbank")
	}

	block, _ := pem.Decode([]byte(privPEM))
	if block == nil {
		t.Fatal("der private schlüssel ist kein pem")
	}
	priv, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if priv.N.BitLen() != 2048 {
		t.Errorf("schlüssellänge %d, erwartet 2048", priv.N.BitLen())
	}

	roh, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(info.Value,
		"v=DKIM1; h=sha256; k=rsa; p="))
	if err != nil {
		t.Fatal(err)
	}
	pub, err := x509.ParsePKIXPublicKey(roh)
	if err != nil {
		t.Fatal(err)
	}
	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		t.Fatalf("der öffentliche teil ist kein rsa-schlüssel (%T)", pub)
	}
	if !rsaPub.Equal(&priv.PublicKey) {
		t.Error("öffentlicher und privater teil gehören nicht zusammen")
	}
}

// Der private Schlüssel geht an den Agent — aber nur der einer aktiven Domäne,
// und nur entschlüsselt.
func TestDKIMKommtInDenSollzustand(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	svc := mailService(env)

	alice := seedMailTenant(t, env, "alice")
	scA := store.Scope{TenantID: alice, Role: store.RoleOwner}
	domA := ersteDomain(t, env, alice)
	if _, err := svc.EnableDKIM(ctx, scA, domA); err != nil {
		t.Logf("schreiben scheiterte (erwartbar): %v", err)
	}

	p, err := svc.collect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.DKIM) != 1 {
		t.Fatalf("%d dkim-einträge, erwartet 1", len(p.DKIM))
	}
	if p.DKIM[0].Domain != "alice.example.at" || p.DKIM[0].Selector != "volt" {
		t.Errorf("falscher eintrag: %+v", p.DKIM[0])
	}
	if !strings.Contains(p.DKIM[0].PrivateKey, "PRIVATE KEY") {
		t.Error("der schlüssel kommt nicht entschlüsselt beim agent an")
	}

	// Eine abgeschaltete Domäne unterschreibt nicht mehr.
	aus := false
	if _, err := svc.SetDomain(ctx, scA, domA, &aus, nil); err != nil {
		t.Logf("schreiben scheiterte (erwartbar): %v", err)
	}
	p, err = svc.collect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.DKIM) != 0 {
		t.Errorf("eine abgeschaltete domäne unterschreibt weiter: %+v", p.DKIM)
	}
}

// Ein DKIM-Eintrag, der zu einem anderen Schlüssel gehört, ist schlechter als
// keiner: die Unterschrift schlägt fehl, statt zu fehlen. Das muss die Prüfung
// als kritisch melden und nicht als Hinweis.
func TestCheckMeldetFalschenDKIMEintrag(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	svc := mailService(env)

	alice := seedMailTenant(t, env, "alice")
	scA := store.Scope{TenantID: alice, Role: store.RoleOwner}
	domA := ersteDomain(t, env, alice)
	if _, err := svc.EnableDKIM(ctx, scA, domA); err != nil {
		t.Logf("schreiben scheiterte (erwartbar): %v", err)
	}
	d, err := env.store.GetMailDomain(ctx, scA, domA)
	if err != nil {
		t.Fatal(err)
	}

	setzeDNS(t, map[string][]string{
		"volt._domainkey.alice.example.at": {"v=DKIM1; h=sha256; k=rsa; p=EinGanzAndererSchluessel"},
	})

	c := &MailCheck{}
	c.pruefeDKIM(ctx, d)
	if len(c.Befunde) != 1 {
		t.Fatalf("%d befunde", len(c.Befunde))
	}
	if c.Befunde[0].Stufe != BefundKritisch {
		t.Errorf("stufe = %q, erwartet %q — ein falscher eintrag ist schlechter als keiner",
			c.Befunde[0].Stufe, BefundKritisch)
	}

	// Und der richtige Eintrag ist in Ordnung.
	setzeDNS(t, map[string][]string{
		"volt._domainkey.alice.example.at": {"v=DKIM1; h=sha256; k=rsa; p=" + d.DKIMPublic},
	})
	c = &MailCheck{}
	c.pruefeDKIM(ctx, d)
	if c.Befunde[0].Stufe != BefundGut {
		t.Errorf("der passende eintrag wurde als %q gemeldet: %+v",
			c.Befunde[0].Stufe, c.Befunde[0])
	}

	// Fehlt er ganz, ist es ein Hinweis — nicht kritisch. Keine Unterschrift
	// wertet eine Mail nur nicht auf; eine kaputte wertet sie ab.
	setzeDNS(t, map[string][]string{})
	c = &MailCheck{}
	c.pruefeDKIM(ctx, d)
	if c.Befunde[0].Stufe != BefundWarnung {
		t.Errorf("ein fehlender eintrag wurde als %q gemeldet", c.Befunde[0].Stufe)
	}
}

// Ohne reject_unauth_destination ist der Server ein offenes Relay — und das
// merkt man daran, dass die IP binnen Stunden auf jeder Sperrliste steht.
func TestCheckMeldetOffenesRelay(t *testing.T) {
	ctx := context.Background()
	setzeDNS(t, map[string][]string{})

	offen := &agent.MailFacts{
		Hostname:          "mail.example.at",
		Listening:         []int{25, 587, 993},
		TLSCert:           "/x/fullchain.pem",
		RelayRestrictions: "permit_mynetworks,permit_sasl_authenticated",
	}
	c := &MailCheck{}
	c.pruefeServer(ctx, offen)
	if stufeVon(c, "Relay") != BefundKritisch {
		t.Errorf("ein offenes relay wurde als %q gemeldet", stufeVon(c, "Relay"))
	}

	zu := *offen
	zu.RelayRestrictions = "permit_mynetworks,permit_sasl_authenticated,reject_unauth_destination"
	c = &MailCheck{}
	c.pruefeServer(ctx, &zu)
	if stufeVon(c, "Relay") != BefundGut {
		t.Errorf("ein geschlossenes relay wurde als %q gemeldet", stufeVon(c, "Relay"))
	}
}

func stufeVon(c *MailCheck, was string) string {
	for _, b := range c.Befunde {
		if b.Was == was {
			return b.Stufe
		}
	}
	return ""
}

// setzeDNS ersetzt die Auskünfte für die Dauer eines Tests.
func setzeDNS(t *testing.T, txt map[string][]string) {
	t.Helper()
	altTXT, altMX, altHost, altAddr := dnsTXT, dnsMX, dnsHost, dnsAddr
	t.Cleanup(func() { dnsTXT, dnsMX, dnsHost, dnsAddr = altTXT, altMX, altHost, altAddr })

	dnsTXT = func(_ context.Context, name string) ([]string, error) {
		if v, ok := txt[name]; ok {
			return v, nil
		}
		return nil, errors.New("kein eintrag")
	}
	dnsMX = func(context.Context, string) ([]*net.MX, error) { return nil, errors.New("kein eintrag") }
	dnsHost = func(context.Context, string) ([]string, error) { return nil, errors.New("kein eintrag") }
	dnsAddr = func(context.Context, string) ([]string, error) { return nil, errors.New("kein eintrag") }
}
