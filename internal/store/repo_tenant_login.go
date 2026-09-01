package store

import (
	"context"
	"fmt"
	"strings"
)

// Eigene Anmeldeseite je Mandant.
//
// Bis hierher bekommt ein Kunde dieselbe Adresse genannt wie der Betreiber —
// samt dessen zufälligem Zugriffspfad, der genau deshalb zufällig ist. Mit
// einer eigenen Domain führt der Weg an die Anmeldung, die zu ihm gehört.
//
// Die Zusage dahinter steht in LoginTenantFor: unter dieser Domain kommt nur
// herein, wer zu diesem Mandanten gehört.

// NormalizeLoginDomain bringt eine Domain in die Form, in der sie gespeichert
// und verglichen wird.
//
// Klein und ohne Punkt am Ende. "Kunde.de.", "kunde.de" und "KUNDE.DE" sind
// derselbe Name; lägen sie als drei Zeilen in der Tabelle, entschiede die
// Reihenfolge der Abfrage, wessen Anmeldung erscheint.
func NormalizeLoginDomain(d string) (string, error) {
	d = strings.ToLower(strings.TrimSpace(d))
	d = strings.TrimSuffix(d, ".")
	if d == "" {
		return "", nil
	}
	// Kein Wildcard: eine Anmeldeseite hat einen Namen, unter dem sie erreichbar
	// ist. "*.kunde.de" beantwortete jede Adresse darunter und wäre ein
	// bequemer Ort für eine gefälschte Anmeldung.
	if strings.HasPrefix(d, "*.") || !ValidDomain(d) {
		return "", fmt.Errorf("%q ist kein gültiger hostname für eine anmeldeseite", d)
	}
	return d, nil
}

// SetTenantLoginDomain setzt oder löscht die Anmeldedomain eines Mandanten.
//
// Der leere String löscht sie: dann melden sich seine Leute wieder am Panel des
// Betreibers an.
func (s *Store) SetTenantLoginDomain(ctx context.Context, sc Scope, tenantID int64, domain string) error {
	// Wer einen Mandanten verwaltet, darf auch dessen Anmeldedomain setzen —
	// dieselbe Schranke wie bei jeder anderen Eigenschaft des Mandanten.
	if err := sc.owns(tenantID); err != nil {
		return err
	}
	clean, err := NormalizeLoginDomain(domain)
	if err != nil {
		return err
	}

	res, err := s.db.ExecContext(ctx, `
		UPDATE tenants SET login_domain = ?, updated_at = ? WHERE id = ?`,
		clean, now(), tenantID)
	if err != nil {
		if isUnique(err) {
			// Welcher Mandant sie schon hat, steht bewusst nicht in der
			// Meldung: das wäre eine Auskunft über einen fremden Mandanten an
			// jemanden, der sie nicht bekommen soll.
			return fmt.Errorf("%w: die domain %q ist bereits vergeben", ErrConflict, clean)
		}
		return err
	}
	return affected(res, err)
}

// LoginTenantFor sucht den Mandanten zu einer Anmeldedomain.
//
// Ohne Scope, und das ist Absicht: gefragt wird, bevor sich jemand angemeldet
// hat — es gibt noch keinen. Zurück kommt deshalb auch nur, was auf einer
// Anmeldeseite ohnehin steht: die Nummer und der Name des Mandanten.
//
// Ein leerer oder unbekannter Host liefert ErrNotFound, kein Fehler: die
// allermeisten Aufrufe gelten dem Panel des Betreibers, und das hat keine
// Anmeldedomain.
func (s *Store) LoginTenantFor(ctx context.Context, host string) (*Tenant, error) {
	clean, err := NormalizeLoginDomain(host)
	if err != nil || clean == "" {
		// Ein Host, der kein Hostname ist, ist keine Anmeldedomain. Dass er
		// ungültig war, geht niemanden etwas an — die Antwort ist dieselbe wie
		// bei einem gültigen, der nicht eingetragen ist.
		return nil, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, slug, plan_id, status, login_domain, cloudflare_token,
			created_at, updated_at
		FROM tenants WHERE login_domain <> '' AND lower(login_domain) = ?`, clean)
	return scanTenant(row)
}

// LoginDomains liefert alle Mandanten mit eigener Anmeldedomain.
//
// Gedacht für einen Zwischenspeicher im Panel: die Zuordnung wird bei jeder
// Anfrage gebraucht — auch für jede Bilddatei —, ändert sich aber nur, wenn
// jemand eine Domain einträgt. Eine Abfrage je Anfrage wäre für eine Antwort,
// die fast immer "gehört zu keinem" lautet, zu viel.
func (s *Store) LoginDomains(ctx context.Context) ([]*Tenant, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, slug, plan_id, status, login_domain, cloudflare_token,
			created_at, updated_at
		FROM tenants WHERE login_domain <> ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*Tenant{}
	for rows.Next() {
		t, err := scanTenant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
