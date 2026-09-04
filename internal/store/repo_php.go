package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

const poolCols = `id, tenant_id, site_id, php_version, pool_name, socket_path, pm,
	max_children, memory_limit, max_execution_time, upload_max_filesize,
	open_basedir, disable_functions, extra_ini, created_at, updated_at`

func (s *Store) CreatePHPPool(ctx context.Context, sc Scope, p *PHPPool) error {
	if err := sc.owns(p.TenantID); err != nil {
		return err
	}
	// Die Site muss demselben Mandanten gehören — sonst ließe sich ein
	// PHP-FPM-Pool unter der site_id eines fremden Mandanten anlegen.
	site, err := s.GetSite(ctx, sc, p.SiteID)
	if err != nil {
		return err
	}
	if site.TenantID != p.TenantID {
		return ErrNotFound
	}
	p.CreatedAt, p.UpdatedAt = now(), now()

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO php_pools (tenant_id, site_id, php_version, pool_name, socket_path,
			pm, max_children, memory_limit, max_execution_time, upload_max_filesize,
			open_basedir, disable_functions, extra_ini, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.TenantID, p.SiteID, p.PHPVersion, p.PoolName, p.SocketPath, p.PM,
		p.MaxChildren, p.MemoryLimit, p.MaxExecutionTime, p.UploadMaxFilesize,
		p.OpenBasedir, p.DisableFunctions, p.ExtraINI, p.CreatedAt, p.UpdatedAt)
	if err != nil {
		if isUnique(err) {
			return fmt.Errorf("%w: pool %s", ErrConflict, p.PoolName)
		}
		return err
	}
	p.ID, err = res.LastInsertId()
	return err
}

// PHPPoolBySite liefert den Pool einer Site. Jede PHP-Site hat genau einen.
func (s *Store) PHPPoolBySite(ctx context.Context, sc Scope, siteID int64) (*PHPPool, error) {
	where, args, err := sc.where("php_pools", "site_id = ?")
	if err != nil {
		return nil, err
	}
	return scanPool(s.db.QueryRowContext(ctx, `SELECT `+poolCols+` FROM php_pools`+where,
		append(args, siteID)...))
}

func (s *Store) UpdatePHPPool(ctx context.Context, sc Scope, p *PHPPool) error {
	if err := sc.owns(p.TenantID); err != nil {
		return err
	}
	where, args, err := sc.where("php_pools", "id = ?")
	if err != nil {
		return err
	}
	p.UpdatedAt = now()

	set := []any{p.PHPVersion, p.PM, p.MaxChildren, p.MemoryLimit, p.MaxExecutionTime,
		p.UploadMaxFilesize, p.OpenBasedir, p.DisableFunctions, p.ExtraINI, p.UpdatedAt}
	res, err := s.db.ExecContext(ctx, `
		UPDATE php_pools SET php_version = ?, pm = ?, max_children = ?, memory_limit = ?,
			max_execution_time = ?, upload_max_filesize = ?, open_basedir = ?,
			disable_functions = ?, extra_ini = ?, updated_at = ?`+where,
		append(set, append(args, p.ID)...)...)
	return affected(res, err)
}

func scanPool(sc scanner) (*PHPPool, error) {
	var p PHPPool
	err := sc.Scan(&p.ID, &p.TenantID, &p.SiteID, &p.PHPVersion, &p.PoolName, &p.SocketPath,
		&p.PM, &p.MaxChildren, &p.MemoryLimit, &p.MaxExecutionTime, &p.UploadMaxFilesize,
		&p.OpenBasedir, &p.DisableFunctions, &p.ExtraINI, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}
