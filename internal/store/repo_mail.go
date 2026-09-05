package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Mail: Domänen, Postfächer, Aliase.
//
// Alles hier wird später eine Zeile in einer Map-Datei, die Postfix oder
// Dovecot liest. Das ist der Grund für die strengen Muster: eine Map ist
// zeilenweise aufgebaut, und was einen Zeilenumbruch in einen Wert bekommt,
// schreibt die nächste Zuordnung selbst. Derselbe Mechanismus wie bei einer
// systemd-Unit, nur mit einer anderen Grammatik.
//
// Geprüft wird deshalb hier, im Store, und nicht erst beim Schreiben: was gar
// nicht erst in der Datenbank steht, kann auch aus ihr nicht in eine Datei
// geraten.

var (
	// reMailDomain ist ein Domainname. Kein führender Bindestrich, kein
	// Unterstrich, mindestens ein Punkt — eine Maildomäne ohne Punkt gibt es
	// im Internet nicht, und lokal soll dieses Panel keine Post ausliefern.
	reMailDomain = regexp.MustCompile(
		`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)

	// reLocalPart ist der Teil vor dem @.
	//
	// Deutlich enger als RFC 5321 erlaubt: dort sind Anführungszeichen,
	// Leerzeichen und beinahe alles andere in einem quoted-string zulässig.
	// Wer das unterstützen will, muss es durch jede Map-Datei, jedes
	// Log-Format und jede Shell-freie Kommandozeile tragen. Der Gewinn wäre
	// eine Adresse, die ohnehin bei der Hälfte aller Absender scheitert.
	reLocalPart = regexp.MustCompile(`^[a-z0-9]([a-z0-9._+-]{0,62}[a-z0-9])?$`)

	// reDKIMSelector steht im DNS-Namen und in der OpenDKIM-Tabelle.
	reDKIMSelector = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`)
)

// MailDomain ist eine Domäne, für die dieser Server Post annimmt.
type MailDomain struct {
	ID       int64  `json:"id"`
	TenantID int64  `json:"tenant_id"`
	Domain   string `json:"domain"`
	Active   bool   `json:"active"`
	// CatchAll ist die Adresse für alles, was kein Postfach trifft. Leer heißt
	// abweisen — ein Catch-All sammelt vor allem Spam.
	CatchAll     string `json:"catch_all"`
	DKIMSelector string `json:"dkim_selector"`
	// DKIMPrivate liegt verschlüsselt und wird nie serialisiert. Wer ihn hat,
	// unterschreibt Mail im Namen dieser Domäne.
	DKIMPrivate string `json:"-"`
	DKIMPublic  string `json:"dkim_public"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
}

// Mailbox ist ein Postfach.
type Mailbox struct {
	ID        int64  `json:"id"`
	TenantID  int64  `json:"tenant_id"`
	DomainID  int64  `json:"domain_id"`
	LocalPart string `json:"local_part"`
	Address   string `json:"address"`
	// PasswordEnc liegt verschlüsselt und wird nie serialisiert.
	PasswordEnc string `json:"-"`
	QuotaMB     int64  `json:"quota_mb"`
	Active      bool   `json:"active"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
}

// MailAlias leitet eine Adresse an eine andere weiter.
type MailAlias struct {
	ID          int64  `json:"id"`
	TenantID    int64  `json:"tenant_id"`
	DomainID    int64  `json:"domain_id"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Active      bool   `json:"active"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
}

const (
	mailDomainCols = `id, tenant_id, domain, active, catch_all,
		dkim_selector, dkim_private, dkim_public, created_at, updated_at`
	mailboxCols = `id, tenant_id, domain_id, local_part, address, password_enc,
		quota_mb, active, created_at, updated_at`
	mailAliasCols = `id, tenant_id, domain_id, source, destination, active,
		created_at, updated_at`
)

// ValidMailDomain prüft einen Domainnamen für Mail.
func ValidMailDomain(d string) bool { return reMailDomain.MatchString(d) }

// ValidLocalPart prüft den Teil vor dem @.
func ValidLocalPart(l string) bool { return reLocalPart.MatchString(l) }

// ValidMailAddress prüft eine vollständige Adresse.
//
// Eine Funktion und nicht zwei Aufrufe an der Fundstelle: die Frage "ist das
// eine Adresse" wird an mehreren Stellen gestellt, und zwei Antworten darauf
// wären zwei Stellen, an denen die eine strenger ist als die andere.
func ValidMailAddress(a string) bool {
	local, domain, ok := strings.Cut(a, "@")
	return ok && ValidLocalPart(local) && ValidMailDomain(domain)
}

// MailAddress setzt eine Adresse zusammen — die einzige Stelle, die das tut.
func MailAddress(localPart, domain string) string { return localPart + "@" + domain }

func validateMailDomain(d *MailDomain) error {
	d.Domain = strings.ToLower(strings.TrimSpace(d.Domain))
	d.CatchAll = strings.ToLower(strings.TrimSpace(d.CatchAll))
	d.DKIMSelector = strings.ToLower(strings.TrimSpace(d.DKIMSelector))

	switch {
	case !ValidMailDomain(d.Domain):
		return fmt.Errorf("%q ist keine maildomäne", d.Domain)
	case d.CatchAll != "" && !ValidMailAddress(d.CatchAll):
		return fmt.Errorf("%q ist keine adresse für catch-all", d.CatchAll)
	case d.DKIMSelector != "" && !reDKIMSelector.MatchString(d.DKIMSelector):
		return fmt.Errorf("%q ist kein dkim-selector", d.DKIMSelector)
	}
	return nil
}

func validateMailbox(m *Mailbox) error {
	m.LocalPart = strings.ToLower(strings.TrimSpace(m.LocalPart))
	m.Address = strings.ToLower(strings.TrimSpace(m.Address))

	switch {
	case !ValidLocalPart(m.LocalPart):
		return fmt.Errorf("%q ist kein zulässiger name vor dem @", m.LocalPart)
	case !ValidMailAddress(m.Address):
		return fmt.Errorf("%q ist keine adresse", m.Address)
	case !strings.HasPrefix(m.Address, m.LocalPart+"@"):
		// Die zusammengesetzte Adresse muss zum Namen passen. Sonst stünde in
		// der Map eine Adresse, die zu einem anderen Postfach gehört.
		return fmt.Errorf("%q gehört nicht zu %q", m.Address, m.LocalPart)
	case m.QuotaMB < 0:
		return errors.New("eine quota ist nicht negativ")
	}
	return nil
}

func validateMailAlias(a *MailAlias) error {
	a.Source = strings.ToLower(strings.TrimSpace(a.Source))
	a.Destination = strings.ToLower(strings.TrimSpace(a.Destination))

	switch {
	case !ValidMailAddress(a.Source):
		return fmt.Errorf("%q ist keine adresse", a.Source)
	case !ValidMailAddress(a.Destination):
		return fmt.Errorf("%q ist kein ziel", a.Destination)
	case a.Source == a.Destination:
		// Postfix liefe darauf im Kreis, bis es aufgibt.
		return errors.New("ein alias auf sich selbst")
	}
	return nil
}

// --- Domänen ---------------------------------------------------------------

func (s *Store) CreateMailDomain(ctx context.Context, sc Scope, d *MailDomain) error {
	if err := sc.owns(d.TenantID); err != nil {
		return err
	}
	if err := validateMailDomain(d); err != nil {
		return err
	}
	d.CreatedAt, d.UpdatedAt = now(), now()

	res, err := s.db.ExecContext(ctx, `INSERT INTO mail_domains
		(tenant_id, domain, active, catch_all, dkim_selector, dkim_private,
		 dkim_public, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.TenantID, d.Domain, boolToInt(d.Active), d.CatchAll, d.DKIMSelector,
		d.DKIMPrivate, d.DKIMPublic, d.CreatedAt, d.UpdatedAt)
	if err != nil {
		return mailConflict(err, "die domäne "+d.Domain+" ist auf diesem server schon vergeben")
	}
	d.ID, err = res.LastInsertId()
	return err
}

func (s *Store) GetMailDomain(ctx context.Context, sc Scope, id int64) (*MailDomain, error) {
	d, err := scanMailDomain(s.db.QueryRowContext(ctx,
		`SELECT `+mailDomainCols+` FROM mail_domains WHERE id = ?`, id))
	if err != nil {
		return nil, err
	}
	if err := sc.owns(d.TenantID); err != nil {
		return nil, ErrNotFound
	}
	return d, nil
}

func (s *Store) ListMailDomains(ctx context.Context, sc Scope) ([]*MailDomain, error) {
	if err := sc.valid(); err != nil {
		return nil, err
	}
	q := `SELECT ` + mailDomainCols + ` FROM mail_domains`
	var args []any
	if !sc.IsSystem() {
		q += ` WHERE tenant_id = ?`
		args = append(args, sc.TenantID)
	}
	q += ` ORDER BY domain`

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*MailDomain{}
	for rows.Next() {
		d, err := scanMailDomain(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) UpdateMailDomain(ctx context.Context, sc Scope, d *MailDomain) error {
	if err := sc.owns(d.TenantID); err != nil {
		return err
	}
	if err := validateMailDomain(d); err != nil {
		return err
	}
	d.UpdatedAt = now()

	res, err := s.db.ExecContext(ctx, `UPDATE mail_domains SET
		domain = ?, active = ?, catch_all = ?, dkim_selector = ?,
		dkim_private = ?, dkim_public = ?, updated_at = ?
		WHERE id = ? AND tenant_id = ?`,
		d.Domain, boolToInt(d.Active), d.CatchAll, d.DKIMSelector,
		d.DKIMPrivate, d.DKIMPublic, d.UpdatedAt, d.ID, d.TenantID)
	if err != nil {
		return mailConflict(err, "die domäne "+d.Domain+" ist auf diesem server schon vergeben")
	}
	return affected(res, nil)
}

// DeleteMailDomain entfernt eine Domäne samt allem, was daran hängt.
//
// Anders als bei einem Mandanten kein "nur wenn nichts mehr dranhängt": eine
// Domäne ohne ihre Postfächer zu behalten ergibt keinen Zustand, den jemand
// haben will. Die Postfächer gehen über ON DELETE CASCADE mit — die Dateien
// auf der Platte räumt der Dienst weg, nicht der Store.
func (s *Store) DeleteMailDomain(ctx context.Context, sc Scope, id int64) error {
	d, err := s.GetMailDomain(ctx, sc, id)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM mail_domains WHERE id = ? AND tenant_id = ?`, d.ID, d.TenantID)
	return affected(res, err)
}

// --- Postfächer ------------------------------------------------------------

func (s *Store) CreateMailbox(ctx context.Context, sc Scope, m *Mailbox) error {
	if err := sc.owns(m.TenantID); err != nil {
		return err
	}
	// Die Domäne muss demselben Mandanten gehören. Ohne diese Zeile könnte
	// jemand ein Postfach in einer fremden Domäne anlegen — mit seiner eigenen
	// tenant_id daran, also unauffällig in jeder Liste.
	dom, err := s.GetMailDomain(ctx, sc, m.DomainID)
	if err != nil {
		return err
	}
	if dom.TenantID != m.TenantID {
		return ErrNotFound
	}
	m.Address = MailAddress(strings.ToLower(strings.TrimSpace(m.LocalPart)), dom.Domain)
	if err := validateMailbox(m); err != nil {
		return err
	}
	m.CreatedAt, m.UpdatedAt = now(), now()

	res, err := s.db.ExecContext(ctx, `INSERT INTO mailboxes
		(tenant_id, domain_id, local_part, address, password_enc, quota_mb,
		 active, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.TenantID, m.DomainID, m.LocalPart, m.Address, m.PasswordEnc,
		m.QuotaMB, boolToInt(m.Active), m.CreatedAt, m.UpdatedAt)
	if err != nil {
		return mailConflict(err, "das postfach "+m.Address+" gibt es schon")
	}
	m.ID, err = res.LastInsertId()
	return err
}

func (s *Store) GetMailbox(ctx context.Context, sc Scope, id int64) (*Mailbox, error) {
	m, err := scanMailbox(s.db.QueryRowContext(ctx,
		`SELECT `+mailboxCols+` FROM mailboxes WHERE id = ?`, id))
	if err != nil {
		return nil, err
	}
	if err := sc.owns(m.TenantID); err != nil {
		return nil, ErrNotFound
	}
	return m, nil
}

// ListMailboxes liefert die Postfächer im Scope, optional nur die einer Domäne.
func (s *Store) ListMailboxes(ctx context.Context, sc Scope, domainID int64) ([]*Mailbox, error) {
	if err := sc.valid(); err != nil {
		return nil, err
	}
	q := `SELECT ` + mailboxCols + ` FROM mailboxes`
	var bed []string
	var args []any
	if !sc.IsSystem() {
		bed = append(bed, `tenant_id = ?`)
		args = append(args, sc.TenantID)
	}
	if domainID > 0 {
		bed = append(bed, `domain_id = ?`)
		args = append(args, domainID)
	}
	if len(bed) > 0 {
		q += ` WHERE ` + strings.Join(bed, " AND ")
	}
	q += ` ORDER BY address`

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*Mailbox{}
	for rows.Next() {
		m, err := scanMailbox(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) UpdateMailbox(ctx context.Context, sc Scope, m *Mailbox) error {
	if err := sc.owns(m.TenantID); err != nil {
		return err
	}
	if err := validateMailbox(m); err != nil {
		return err
	}
	m.UpdatedAt = now()

	res, err := s.db.ExecContext(ctx, `UPDATE mailboxes SET
		password_enc = ?, quota_mb = ?, active = ?, updated_at = ?
		WHERE id = ? AND tenant_id = ?`,
		m.PasswordEnc, m.QuotaMB, boolToInt(m.Active), m.UpdatedAt, m.ID, m.TenantID)
	return affected(res, err)
}

func (s *Store) DeleteMailbox(ctx context.Context, sc Scope, id int64) error {
	m, err := s.GetMailbox(ctx, sc, id)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM mailboxes WHERE id = ? AND tenant_id = ?`, m.ID, m.TenantID)
	return affected(res, err)
}

// --- Aliase ----------------------------------------------------------------

func (s *Store) CreateMailAlias(ctx context.Context, sc Scope, a *MailAlias) error {
	if err := sc.owns(a.TenantID); err != nil {
		return err
	}
	dom, err := s.GetMailDomain(ctx, sc, a.DomainID)
	if err != nil {
		return err
	}
	if dom.TenantID != a.TenantID {
		return ErrNotFound
	}
	if err := validateMailAlias(a); err != nil {
		return err
	}
	// Die Quelle muss in der Domäne liegen, zu der der Alias gehört. Sonst
	// könnte ein Mandant Post umleiten, die an eine fremde Domäne geht.
	if !strings.HasSuffix(a.Source, "@"+dom.Domain) {
		return fmt.Errorf("%q liegt nicht in %s", a.Source, dom.Domain)
	}
	a.CreatedAt, a.UpdatedAt = now(), now()

	res, err := s.db.ExecContext(ctx, `INSERT INTO mail_aliases
		(tenant_id, domain_id, source, destination, active, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		a.TenantID, a.DomainID, a.Source, a.Destination, boolToInt(a.Active),
		a.CreatedAt, a.UpdatedAt)
	if err != nil {
		return mailConflict(err, "die weiterleitung von "+a.Source+" nach "+
			a.Destination+" gibt es schon")
	}
	a.ID, err = res.LastInsertId()
	return err
}

func (s *Store) GetMailAlias(ctx context.Context, sc Scope, id int64) (*MailAlias, error) {
	a, err := scanMailAlias(s.db.QueryRowContext(ctx,
		`SELECT `+mailAliasCols+` FROM mail_aliases WHERE id = ?`, id))
	if err != nil {
		return nil, err
	}
	if err := sc.owns(a.TenantID); err != nil {
		return nil, ErrNotFound
	}
	return a, nil
}

func (s *Store) ListMailAliases(ctx context.Context, sc Scope, domainID int64) ([]*MailAlias, error) {
	if err := sc.valid(); err != nil {
		return nil, err
	}
	q := `SELECT ` + mailAliasCols + ` FROM mail_aliases`
	var bed []string
	var args []any
	if !sc.IsSystem() {
		bed = append(bed, `tenant_id = ?`)
		args = append(args, sc.TenantID)
	}
	if domainID > 0 {
		bed = append(bed, `domain_id = ?`)
		args = append(args, domainID)
	}
	if len(bed) > 0 {
		q += ` WHERE ` + strings.Join(bed, " AND ")
	}
	q += ` ORDER BY source, destination`

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*MailAlias{}
	for rows.Next() {
		a, err := scanMailAlias(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) DeleteMailAlias(ctx context.Context, sc Scope, id int64) error {
	a, err := s.GetMailAlias(ctx, sc, id)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM mail_aliases WHERE id = ? AND tenant_id = ?`, a.ID, a.TenantID)
	return affected(res, err)
}

// --- Zählen, für die Quota des Pakets --------------------------------------

// CountMailboxes zählt die Postfächer eines Mandanten.
func (s *Store) CountMailboxes(ctx context.Context, tenantID int64) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM mailboxes WHERE tenant_id = ?`, tenantID).Scan(&n)
	return n, err
}

// --- Hilfsmittel -----------------------------------------------------------

// mailConflict macht aus einem verletzten eindeutigen Index eine Meldung, die
// jemandem hilft. "UNIQUE constraint failed: mailboxes.address" hilft nicht.
func mailConflict(err error, wenn string) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return fmt.Errorf("%w: %s", ErrConflict, wenn)
	}
	return err
}

func scanMailDomain(row scanner) (*MailDomain, error) {
	var d MailDomain
	var active int
	err := row.Scan(&d.ID, &d.TenantID, &d.Domain, &active, &d.CatchAll,
		&d.DKIMSelector, &d.DKIMPrivate, &d.DKIMPublic, &d.CreatedAt, &d.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	d.Active = active != 0
	return &d, nil
}

func scanMailbox(row scanner) (*Mailbox, error) {
	var m Mailbox
	var active int
	err := row.Scan(&m.ID, &m.TenantID, &m.DomainID, &m.LocalPart, &m.Address,
		&m.PasswordEnc, &m.QuotaMB, &active, &m.CreatedAt, &m.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	m.Active = active != 0
	return &m, nil
}

func scanMailAlias(row scanner) (*MailAlias, error) {
	var a MailAlias
	var active int
	err := row.Scan(&a.ID, &a.TenantID, &a.DomainID, &a.Source, &a.Destination,
		&active, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	a.Active = active != 0
	return &a, nil
}
