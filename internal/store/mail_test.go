package store

import (
	"context"
	"errors"
	"testing"
)

// Alles in diesen Tabellen wird später eine Zeile in einer Map-Datei, die
// Postfix oder Dovecot liest. Eine Map ist zeilenweise aufgebaut: was einen
// Zeilenumbruch in einen Wert bekommt, schreibt die nächste Zuordnung selbst.
//
// Geprüft wird deshalb hier und nicht erst beim Schreiben — was gar nicht in
// der Datenbank steht, kann auch nicht aus ihr in eine Datei geraten.
func TestMailAdressenPruefung(t *testing.T) {
	gut := []string{
		"post@example.at",
		"a@example.at",
		"vor.nach@example.at",
		"info+shop@example.at",
		"x_y@sub.example.co.uk",
	}
	for _, a := range gut {
		if !ValidMailAddress(a) {
			t.Errorf("%q wurde abgelehnt", a)
		}
	}

	schlecht := []string{
		"",
		"post",
		"post@",
		"@example.at",
		"post@example",              // keine Punktdomäne
		"post@-example.at",          // führender Bindestrich
		"post@example.at\nfoo@x.at", // die nächste Zuordnung
		"post\n@example.at",
		"po st@example.at",
		"\"post\"@example.at", // RFC-konform, hier trotzdem nicht
		"post@example.at ",    // ValidMailAddress trimmt nicht — der Aufrufer tut es
		".post@example.at",
		"post.@example.at",
		"post@exa mple.at",
		"post@example.at;rm -rf /",
	}
	for _, a := range schlecht {
		if ValidMailAddress(a) {
			t.Errorf("%q wurde angenommen", a)
		}
	}
}

// Ein Postfach in einer fremden Domäne wäre mit der eigenen tenant_id daran in
// jeder Liste unauffällig — und bekäme trotzdem die Post des anderen.
func TestMailboxNurInEigenerDomaene(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	tenantA, _, _ := seedTenant(t, st, "alice")
	tenantB, _, _ := seedTenant(t, st, "bob")
	a, b := tenantA.ID, tenantB.ID

	domA := &MailDomain{TenantID: a, Domain: "alice.example.at", Active: true}
	if err := st.CreateMailDomain(ctx, SystemScope(), domA); err != nil {
		t.Fatal(err)
	}

	// Bob versucht, ein Postfach in Alices Domäne anzulegen.
	scB := Scope{TenantID: b, Role: RoleOwner}
	err := st.CreateMailbox(ctx, scB, &Mailbox{
		TenantID: b, DomainID: domA.ID, LocalPart: "post",
	})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("ein postfach in einer fremden domäne wurde angelegt: %v", err)
	}

	// Und Alice sieht Bobs Domänen nicht.
	scA := Scope{TenantID: a, Role: RoleOwner}
	domB := &MailDomain{TenantID: b, Domain: "bob.example.at", Active: true}
	if err := st.CreateMailDomain(ctx, SystemScope(), domB); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetMailDomain(ctx, scA, domB.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("alice sieht bobs domäne: %v", err)
	}
	liste, err := st.ListMailDomains(ctx, scA)
	if err != nil {
		t.Fatal(err)
	}
	if len(liste) != 1 || liste[0].Domain != "alice.example.at" {
		t.Errorf("die liste enthält fremde domänen: %+v", liste)
	}
}

// Die Adresse setzt der Store zusammen, nicht der Aufrufer. Sonst stünde in
// der Map eine Adresse, die zu einem anderen Postfach gehört.
func TestMailboxAdresseKommtAusDerDomaene(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	tenant, _, _ := seedTenant(t, st, "alice")
	tid := tenant.ID
	sc := Scope{TenantID: tid, Role: RoleOwner}

	dom := &MailDomain{TenantID: tid, Domain: "example.at", Active: true}
	if err := st.CreateMailDomain(ctx, sc, dom); err != nil {
		t.Fatal(err)
	}

	m := &Mailbox{
		TenantID: tid, DomainID: dom.ID, LocalPart: "Post",
		// Untergeschoben — und wirkungslos.
		Address: "root@andere-domain.at",
	}
	if err := st.CreateMailbox(ctx, sc, m); err != nil {
		t.Fatal(err)
	}
	if m.Address != "post@example.at" {
		t.Errorf("adresse = %q, erwartet post@example.at", m.Address)
	}

	// Dieselbe Adresse ein zweites Mal: die Datenbank sagt nein, nicht der Code.
	zweites := &Mailbox{TenantID: tid, DomainID: dom.ID, LocalPart: "post"}
	if err := st.CreateMailbox(ctx, sc, zweites); !errors.Is(err, ErrConflict) {
		t.Errorf("dasselbe postfach zweimal: %v", err)
	}
}

// Ein Alias darf nur Post umleiten, die an die eigene Domäne geht.
func TestAliasNurAusDerEigenenDomaene(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	tenant, _, _ := seedTenant(t, st, "alice")
	tid := tenant.ID
	sc := Scope{TenantID: tid, Role: RoleOwner}

	dom := &MailDomain{TenantID: tid, Domain: "example.at", Active: true}
	if err := st.CreateMailDomain(ctx, sc, dom); err != nil {
		t.Fatal(err)
	}

	// Die Quelle liegt woanders — das wäre das Abfangen fremder Post.
	err := st.CreateMailAlias(ctx, sc, &MailAlias{
		TenantID: tid, DomainID: dom.ID,
		Source: "post@fremde-domain.at", Destination: "ich@example.at",
	})
	if err == nil {
		t.Error("ein alias auf eine fremde quelle wurde angelegt")
	}

	// Ein Alias auf sich selbst liefe im Kreis.
	err = st.CreateMailAlias(ctx, sc, &MailAlias{
		TenantID: tid, DomainID: dom.ID,
		Source: "post@example.at", Destination: "post@example.at",
	})
	if err == nil {
		t.Error("ein alias auf sich selbst wurde angelegt")
	}

	// Und der richtige Fall geht.
	if err := st.CreateMailAlias(ctx, sc, &MailAlias{
		TenantID: tid, DomainID: dom.ID, Active: true,
		Source: "info@example.at", Destination: "post@example.at",
	}); err != nil {
		t.Fatalf("ein gültiger alias wurde abgelehnt: %v", err)
	}
}

// Die Domäne gehört serverweit einem: zwei Mandanten mit derselben Maildomäne
// wären zwei Postfächer für dieselbe Adresse, und die Zustellung entschiede,
// wer die Post bekommt.
func TestMaildomaeneGehoertGenauEinemMandanten(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	tenantA, _, _ := seedTenant(t, st, "alice")
	tenantB, _, _ := seedTenant(t, st, "bob")
	a, b := tenantA.ID, tenantB.ID

	if err := st.CreateMailDomain(ctx, SystemScope(),
		&MailDomain{TenantID: a, Domain: "example.at", Active: true}); err != nil {
		t.Fatal(err)
	}
	err := st.CreateMailDomain(ctx, SystemScope(),
		&MailDomain{TenantID: b, Domain: "example.at", Active: true})
	if !errors.Is(err, ErrConflict) {
		t.Errorf("dieselbe domäne für zwei mandanten: %v", err)
	}
}
