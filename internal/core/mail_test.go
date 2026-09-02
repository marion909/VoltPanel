package core

import (
	"context"
	"testing"

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
