package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestAnmeldedomainWirdVereinheitlicht: "Kunde.de.", "kunde.de" und "KUNDE.DE"
// sind derselbe Name. Lägen sie als drei Zeilen in der Tabelle, entschiede die
// Reihenfolge der Abfrage, wessen Anmeldung erscheint.
func TestAnmeldedomainWirdVereinheitlicht(t *testing.T) {
	for _, roh := range []string{"Panel.Kunde.de", "panel.kunde.de.", "  PANEL.KUNDE.DE  "} {
		got, err := NormalizeLoginDomain(roh)
		if err != nil {
			t.Errorf("%q: %v", roh, err)
			continue
		}
		if got != "panel.kunde.de" {
			t.Errorf("%q wurde zu %q", roh, got)
		}
	}
}

// TestKeinWildcardAlsAnmeldedomain: eine Anmeldeseite hat einen Namen, unter
// dem sie erreichbar ist. "*.kunde.de" beantwortete jede Adresse darunter und
// wäre ein bequemer Ort für eine gefälschte Anmeldung — mit gültigem
// Zertifikat, weil das Panel es für diesen Namen ausliefern würde.
func TestKeinWildcardAlsAnmeldedomain(t *testing.T) {
	schlecht := []string{
		"*.kunde.de",
		"kunde",             // kein Punkt, kein Hostname
		"panel.kunde.de/x",  // Pfad
		"panel.kunde.de:80", // Port
		"192.168.0.1",       // keine Domain
		"pan el.kunde.de",   // Leerzeichen
		"-kunde.de",
	}
	for _, d := range schlecht {
		if got, err := NormalizeLoginDomain(d); err == nil {
			t.Errorf("%q wurde als %q angenommen", d, got)
		}
	}
}

// TestAnmeldedomainNurEinmalVergeben: zwei Mandanten mit derselben Domain
// hieße, dass die Reihenfolge der Abfrage entscheidet, wessen Leute sich dort
// anmelden dürfen.
func TestAnmeldedomainNurEinmalVergeben(t *testing.T) {
	st := newTestStore(t)
	ctx, sys := context.Background(), SystemScope()

	a := &Tenant{Name: "Kanzlei Wittgenstein", Slug: "wittgenstein"}
	b := &Tenant{Name: "Baumarkt Zirbenweg", Slug: "zirbenweg"}
	for _, tenant := range []*Tenant{a, b} {
		if err := st.CreateTenant(ctx, sys, tenant); err != nil {
			t.Fatal(err)
		}
	}

	if err := st.SetTenantLoginDomain(ctx, sys, a.ID, "panel.kunde.de"); err != nil {
		t.Fatal(err)
	}
	// Auch in anderer Schreibweise: der Index vergleicht kleingeschrieben.
	err := st.SetTenantLoginDomain(ctx, sys, b.ID, "Panel.Kunde.DE")
	if err == nil {
		t.Fatal("die Domain wurde zweimal vergeben")
	}
	if !errors.Is(err, ErrConflict) {
		t.Errorf("abgelehnt, aber aus dem falschen Grund: %v", err)
	}
	// Welcher Mandant sie hat, darf nicht in der Meldung stehen — das wäre eine
	// Auskunft über einen fremden Mandanten.
	if strings.Contains(err.Error(), a.Slug) || strings.Contains(err.Error(), a.Name) {
		t.Errorf("die Meldung nennt den anderen Mandanten: %v", err)
	}

	// Zwei Mandanten ohne Domain sind der Normalfall und dürfen sich nicht
	// gegenseitig blockieren.
	if err := st.SetTenantLoginDomain(ctx, sys, a.ID, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTenantLoginDomain(ctx, sys, b.ID, ""); err != nil {
		t.Errorf("zwei Mandanten ohne Domain: %v", err)
	}
}

// TestUnbekannterHostIstKeinFehler: die allermeisten Aufrufe gelten dem Panel
// des Betreibers, und das hat keine Anmeldedomain.
func TestUnbekannterHostIstKeinFehler(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	for _, host := range []string{"", "panel.example.at", "*.boese.de", "../etc/passwd"} {
		_, err := st.LoginTenantFor(ctx, host)
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("%q: %v, erwartet ErrNotFound", host, err)
		}
	}
}

// TestIndexHaeltAuchOhneNormalisierung prüft die letzte Schranke für sich.
//
// SetTenantLoginDomain schreibt nur kleingeschrieben, deshalb fiele ein Index
// ohne lower() im gewöhnlichen Ablauf nicht auf. Er ist trotzdem nicht
// überflüssig: er ist das, was hält, wenn eine Zeile einmal anders in die
// Tabelle kommt — durch eine spätere Migration, eine Reparatur von Hand, einen
// zweiten Schreibweg. Deshalb wird hier am Setter vorbei eingefügt.
func TestIndexHaeltAuchOhneNormalisierung(t *testing.T) {
	st := newTestStore(t)
	ctx, sys := context.Background(), SystemScope()

	a := &Tenant{Name: "Kanzlei Wittgenstein", Slug: "wittgenstein"}
	b := &Tenant{Name: "Baumarkt Zirbenweg", Slug: "zirbenweg"}
	for _, tenant := range []*Tenant{a, b} {
		if err := st.CreateTenant(ctx, sys, tenant); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := st.db.ExecContext(ctx,
		`UPDATE tenants SET login_domain = ? WHERE id = ?`, "Panel.Kunde.DE", a.ID); err != nil {
		t.Fatal(err)
	}
	_, err := st.db.ExecContext(ctx,
		`UPDATE tenants SET login_domain = ? WHERE id = ?`, "panel.kunde.de", b.ID)
	if err == nil {
		t.Fatal("zwei Schreibweisen derselben Domain nebeneinander angenommen")
	}
	if !isUnique(err) {
		t.Errorf("abgelehnt, aber nicht vom Index: %v", err)
	}
}
