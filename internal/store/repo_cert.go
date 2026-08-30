package store

import (
	"context"
	"database/sql"
	"errors"
)

const certCols = `id, tenant_id, site_id, domains, issuer, challenge, cert_path, key_path,
	not_before, not_after, last_renewal_at, last_error, auto_renew, status, created_at, updated_at`

func (s *Store) CreateCert(ctx context.Context, sc Scope, c *Cert) error {
	if err := sc.owns(c.TenantID); err != nil {
		return err
	}
	c.CreatedAt, c.UpdatedAt = now(), now()
	if c.Status == "" {
		c.Status = "pending"
	}

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO certs (tenant_id, site_id, domains, issuer, challenge, cert_path,
			key_path, not_before, not_after, last_renewal_at, last_error, auto_renew,
			status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.TenantID, nilIfEmpty(c.SiteID), encodeList(c.Domains), c.Issuer, c.Challenge,
		c.CertPath, c.KeyPath, c.NotBefore, c.NotAfter, c.LastRenewalAt, c.LastError,
		boolToInt(c.AutoRenew), c.Status, c.CreatedAt, c.UpdatedAt)
	if err != nil {
		return err
	}
	c.ID, err = res.LastInsertId()
	return err
}

func (s *Store) GetCert(ctx context.Context, sc Scope, id int64) (*Cert, error) {
	where, args, err := sc.where("certs", "id = ?")
	if err != nil {
		return nil, err
	}
	return scanCert(s.db.QueryRowContext(ctx, `SELECT `+certCols+` FROM certs`+where, append(args, id)...))
}

func (s *Store) CertBySite(ctx context.Context, sc Scope, siteID int64) (*Cert, error) {
	where, args, err := sc.where("certs", "site_id = ?")
	if err != nil {
		return nil, err
	}
	return scanCert(s.db.QueryRowContext(ctx,
		`SELECT `+certCols+` FROM certs`+where+` ORDER BY id DESC LIMIT 1`, append(args, siteID)...))
}

func (s *Store) ListCerts(ctx context.Context, sc Scope) ([]*Cert, error) {
	where, args, err := sc.where("certs")
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+certCols+` FROM certs`+where+` ORDER BY not_after`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*Cert{}
	for rows.Next() {
		c, err := scanCert(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CertsDueForRenewal liefert alle Zertifikate, die in den nächsten
// beforeDays ablaufen — die Arbeitsliste des Erneuerungs-Jobs.
func (s *Store) CertsDueForRenewal(ctx context.Context, beforeDays int) ([]*Cert, error) {
	deadline := now() + int64(beforeDays)*86400
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+certCols+` FROM certs
		WHERE auto_renew = 1 AND (not_after IS NULL OR not_after <= ?)
		ORDER BY not_after`, deadline)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*Cert{}
	for rows.Next() {
		c, err := scanCert(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) UpdateCert(ctx context.Context, sc Scope, c *Cert) error {
	if err := sc.owns(c.TenantID); err != nil {
		return err
	}
	where, args, err := sc.where("certs", "id = ?")
	if err != nil {
		return err
	}
	c.UpdatedAt = now()

	set := []any{encodeList(c.Domains), c.Challenge, c.CertPath, c.KeyPath, c.NotBefore,
		c.NotAfter, c.LastRenewalAt, c.LastError, boolToInt(c.AutoRenew), c.Status, c.UpdatedAt}
	res, err := s.db.ExecContext(ctx, `
		UPDATE certs SET domains = ?, challenge = ?, cert_path = ?, key_path = ?,
			not_before = ?, not_after = ?, last_renewal_at = ?, last_error = ?,
			auto_renew = ?, status = ?, updated_at = ?`+where,
		append(set, append(args, c.ID)...)...)
	return affected(res, err)
}

func (s *Store) DeleteCert(ctx context.Context, sc Scope, id int64) error {
	where, args, err := sc.where("certs", "id = ?")
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM certs`+where, append(args, id)...)
	return affected(res, err)
}

func scanCert(sc scanner) (*Cert, error) {
	var c Cert
	var domains string
	var autoRenew int
	err := sc.Scan(&c.ID, &c.TenantID, &c.SiteID, &domains, &c.Issuer, &c.Challenge,
		&c.CertPath, &c.KeyPath, &c.NotBefore, &c.NotAfter, &c.LastRenewalAt,
		&c.LastError, &autoRenew, &c.Status, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	c.Domains, c.AutoRenew = decodeList(domains), autoRenew == 1
	return &c, nil
}
