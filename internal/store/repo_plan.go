package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

const planCols = `id, name, max_sites, max_databases, max_ftp, max_mailboxes,
	max_cronjobs, disk_quota_mb, traffic_quota_mb, description, is_default,
	created_at, updated_at`

// Pläne sind serverweit, nicht tenant-gebunden — deshalb nur für Rollen, die
// tenant-übergreifend arbeiten dürfen.
func (s *Store) CreatePlan(ctx context.Context, sc Scope, p *Plan) error {
	if !sc.IsSystem() && !sc.Role.CanCrossTenant() {
		return fmt.Errorf("%w: rolle %s darf keine pakete anlegen", ErrForbidden, sc.Role)
	}
	if strings.TrimSpace(p.Name) == "" {
		return errors.New("paket braucht einen namen")
	}
	p.CreatedAt, p.UpdatedAt = now(), now()

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO plans (name, max_sites, max_databases, max_ftp, max_mailboxes,
			max_cronjobs, disk_quota_mb, traffic_quota_mb, description, is_default,
			created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.Name, p.MaxSites, p.MaxDatabases, p.MaxFTP, p.MaxMailboxes,
		p.MaxCronjobs, p.DiskQuotaMB, p.TrafficQuotaMB, p.Description,
		boolToInt(p.IsDefault), p.CreatedAt, p.UpdatedAt)
	if err != nil {
		if isUnique(err) {
			return fmt.Errorf("%w: paket %s", ErrConflict, p.Name)
		}
		return err
	}
	p.ID, err = res.LastInsertId()
	return err
}

func (s *Store) ListPlans(ctx context.Context, sc Scope) ([]*Plan, error) {
	if err := sc.valid(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+planCols+` FROM plans ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*Plan{}
	for rows.Next() {
		p, err := scanPlan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PlanForTenant liefert das Paket eines Tenants; ohne Zuordnung ErrNotFound.
func (s *Store) PlanForTenant(ctx context.Context, sc Scope, tenantID int64) (*Plan, error) {
	if err := sc.owns(tenantID); err != nil {
		return nil, err
	}
	return scanPlan(s.db.QueryRowContext(ctx, `
		SELECT `+prefixCols(planCols, "p")+`
		FROM plans p JOIN tenants t ON t.plan_id = p.id
		WHERE t.id = ?`, tenantID))
}

// prefixCols qualifiziert eine Spaltenliste für einen Join.
func prefixCols(cols, alias string) string {
	parts := strings.Split(cols, ",")
	for i, c := range parts {
		parts[i] = alias + "." + strings.TrimSpace(strings.ReplaceAll(c, "\n\t", ""))
	}
	return strings.Join(parts, ", ")
}

func scanPlan(sc scanner) (*Plan, error) {
	var p Plan
	var isDefault int
	err := sc.Scan(&p.ID, &p.Name, &p.MaxSites, &p.MaxDatabases, &p.MaxFTP, &p.MaxMailboxes,
		&p.MaxCronjobs, &p.DiskQuotaMB, &p.TrafficQuotaMB, &p.Description, &isDefault,
		&p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	p.IsDefault = isDefault == 1
	return &p, nil
}

// UpdatePlan ändert ein Paket. Die Grenzwerte gelten sofort für alle Tenants,
// die es zugeordnet haben.
func (s *Store) UpdatePlan(ctx context.Context, sc Scope, p *Plan) error {
	if !sc.IsSystem() && !sc.Role.CanCrossTenant() {
		return fmt.Errorf("%w: rolle %s darf keine pakete ändern", ErrForbidden, sc.Role)
	}
	p.UpdatedAt = now()

	res, err := s.db.ExecContext(ctx, `
		UPDATE plans SET name = ?, max_sites = ?, max_databases = ?, max_ftp = ?,
			max_mailboxes = ?, max_cronjobs = ?, disk_quota_mb = ?, traffic_quota_mb = ?,
			description = ?, is_default = ?, updated_at = ?
		WHERE id = ?`,
		p.Name, p.MaxSites, p.MaxDatabases, p.MaxFTP, p.MaxMailboxes, p.MaxCronjobs,
		p.DiskQuotaMB, p.TrafficQuotaMB, p.Description, boolToInt(p.IsDefault),
		p.UpdatedAt, p.ID)
	return affected(res, err)
}

// DeletePlan entfernt ein Paket. Tenants, die es zugeordnet hatten, stehen
// danach ohne Limit da (ON DELETE SET NULL) — das ist die harmlosere Richtung
// als sie stillschweigend zu sperren.
func (s *Store) DeletePlan(ctx context.Context, sc Scope, id int64) error {
	if !sc.IsSystem() && !sc.Role.CanCrossTenant() {
		return fmt.Errorf("%w: rolle %s darf keine pakete löschen", ErrForbidden, sc.Role)
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM plans WHERE id = ?`, id)
	return affected(res, err)
}

// DefaultPlan liefert das Paket, das neuen Tenants zugeordnet wird.
func (s *Store) DefaultPlan(ctx context.Context) (*Plan, error) {
	return scanPlan(s.db.QueryRowContext(ctx,
		`SELECT `+planCols+` FROM plans WHERE is_default = 1 ORDER BY id LIMIT 1`))
}

// GetPlan liefert ein einzelnes Paket.
func (s *Store) GetPlan(ctx context.Context, sc Scope, id int64) (*Plan, error) {
	if err := sc.valid(); err != nil {
		return nil, err
	}
	return scanPlan(s.db.QueryRowContext(ctx, `SELECT `+planCols+` FROM plans WHERE id = ?`, id))
}

// CreateBackup hält ein erzeugtes Backup fest, damit es im Panel auftaucht.
func (s *Store) CreateBackup(ctx context.Context, sc Scope, b *Backup) error {
	if err := sc.owns(b.TenantID); err != nil {
		return err
	}
	// Site, Datenbank und Backup-Ziel müssen demselben Mandanten gehören —
	// sonst ließe sich ein Backup-Datensatz an eine fremde Ressource hängen.
	if b.SiteID != nil {
		site, err := s.GetSite(ctx, sc, *b.SiteID)
		if err != nil {
			return err
		}
		if site.TenantID != b.TenantID {
			return ErrNotFound
		}
	}
	if b.DatabaseID != nil {
		db, err := s.GetDatabase(ctx, sc, *b.DatabaseID)
		if err != nil {
			return err
		}
		if db.TenantID != b.TenantID {
			return ErrNotFound
		}
	}
	if b.TargetID != nil {
		target, err := s.GetBackupTarget(ctx, sc, *b.TargetID)
		if err != nil {
			return err
		}
		if target.TenantID != b.TenantID {
			return ErrNotFound
		}
	}
	b.CreatedAt = now()

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO backups (tenant_id, site_id, database_id, kind, destination, path,
			size_bytes, checksum, status, error, started_at, finished_at, created_at,
			target_id, remote_path)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		b.TenantID, nilIfEmpty(b.SiteID), nilIfEmpty(b.DatabaseID), b.Kind, b.Destination,
		b.Path, b.SizeBytes, b.Checksum, b.Status, b.Error, b.StartedAt, b.FinishedAt, b.CreatedAt,
		nilIfEmpty(b.TargetID), b.RemotePath)
	if err != nil {
		return err
	}
	b.ID, err = res.LastInsertId()
	return err
}

// ListBackups liefert die Backups des Scopes, neueste zuerst.
func (s *Store) ListBackups(ctx context.Context, sc Scope, limit int) ([]*Backup, error) {
	where, args, err := sc.where("backups")
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 50
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, tenant_id, site_id, database_id, kind, destination, path, size_bytes,
		       checksum, status, error, started_at, finished_at, created_at,
		       target_id, remote_path
		FROM backups`+where+` ORDER BY id DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*Backup{}
	for rows.Next() {
		var b Backup
		if err := rows.Scan(&b.ID, &b.TenantID, &b.SiteID, &b.DatabaseID, &b.Kind,
			&b.Destination, &b.Path, &b.SizeBytes, &b.Checksum, &b.Status, &b.Error,
			&b.StartedAt, &b.FinishedAt, &b.CreatedAt, &b.TargetID, &b.RemotePath); err != nil {
			return nil, err
		}
		out = append(out, &b)
	}
	return out, rows.Err()
}
