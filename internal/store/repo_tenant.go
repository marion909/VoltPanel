package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,30}[a-z0-9]$`)

// CreateTenant legt einen Mandanten an. Nur mit tenant-übergreifendem Scope
// erlaubt — ein Kunde darf sich keine Nachbarn erzeugen.
func (s *Store) CreateTenant(ctx context.Context, sc Scope, t *Tenant) error {
	if !sc.IsSystem() && !sc.Role.CanCrossTenant() {
		return fmt.Errorf("%w: rolle %s darf keine tenants anlegen", ErrForbidden, sc.Role)
	}
	t.Slug = strings.ToLower(strings.TrimSpace(t.Slug))
	if !slugRe.MatchString(t.Slug) {
		return fmt.Errorf("slug %q: erlaubt sind 3–32 zeichen a-z, 0-9 und bindestrich", t.Slug)
	}
	if strings.TrimSpace(t.Name) == "" {
		return errors.New("tenant braucht einen namen")
	}
	if t.Status == "" {
		t.Status = "active"
	}

	t.CreatedAt, t.UpdatedAt = now(), now()
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO tenants (name, slug, plan_id, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		t.Name, t.Slug, t.PlanID, t.Status, t.CreatedAt, t.UpdatedAt)
	if err != nil {
		if isUnique(err) {
			return fmt.Errorf("%w: tenant %q", ErrConflict, t.Slug)
		}
		return err
	}
	t.ID, err = res.LastInsertId()
	return err
}

func (s *Store) GetTenant(ctx context.Context, sc Scope, id int64) (*Tenant, error) {
	// Der Tenant selbst trägt keine tenant_id-Spalte; der Scope wird deshalb
	// gegen die id geprüft.
	if err := sc.owns(id); err != nil {
		return nil, err
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, slug, plan_id, status, created_at, updated_at
		FROM tenants WHERE id = ?`, id)
	return scanTenant(row)
}

func (s *Store) ListTenants(ctx context.Context, sc Scope) ([]*Tenant, error) {
	if err := sc.valid(); err != nil {
		return nil, err
	}
	q := `SELECT id, name, slug, plan_id, status, created_at, updated_at FROM tenants`
	var args []any
	if !sc.IsSystem() {
		q += ` WHERE id = ?`
		args = append(args, sc.TenantID)
	}
	q += ` ORDER BY name`

	rows, err := s.db.QueryContext(ctx, q, args...)
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

func (s *Store) UpdateTenant(ctx context.Context, sc Scope, t *Tenant) error {
	if err := sc.owns(t.ID); err != nil {
		return err
	}
	t.UpdatedAt = now()
	res, err := s.db.ExecContext(ctx, `
		UPDATE tenants SET name = ?, plan_id = ?, status = ?, updated_at = ? WHERE id = ?`,
		t.Name, t.PlanID, t.Status, t.UpdatedAt, t.ID)
	return affected(res, err)
}

// DeleteTenant entfernt den Mandanten samt allem, was per ON DELETE CASCADE
// daran hängt. Der Aufrufer muss vorher die Systemressourcen (Vhosts, Linux-User,
// Datenbanken) über den Agent abgeräumt haben — SQL räumt keine Dateien weg.
func (s *Store) DeleteTenant(ctx context.Context, sc Scope, id int64) error {
	if !sc.IsSystem() && !sc.Role.CanCrossTenant() {
		return fmt.Errorf("%w: rolle %s darf keine tenants löschen", ErrForbidden, sc.Role)
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM tenants WHERE id = ?`, id)
	return affected(res, err)
}

func scanTenant(sc scanner) (*Tenant, error) {
	var t Tenant
	err := sc.Scan(&t.ID, &t.Name, &t.Slug, &t.PlanID, &t.Status, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}
