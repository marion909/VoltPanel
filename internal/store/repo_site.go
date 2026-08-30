package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// domainRe akzeptiert Hostnamen inkl. Wildcard-Präfix. Bewusst streng: dieser
// Wert landet in Nginx-Configs und in Dateipfaden.
var domainRe = regexp.MustCompile(`^(\*\.)?([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

// ValidDomain prüft einen Hostnamen, bevor er irgendwo in ein Template oder
// einen Pfad wandert.
func ValidDomain(d string) bool {
	d = strings.ToLower(strings.TrimSpace(d))
	return len(d) <= 253 && domainRe.MatchString(d)
}

const siteCols = `id, tenant_id, domain, aliases, type, system_user, root_path,
	document_root, php_version, proxy_target, ssl_enabled, force_https, hsts,
	status, created_at, updated_at`

func (s *Store) CreateSite(ctx context.Context, sc Scope, site *Site) error {
	if err := sc.owns(site.TenantID); err != nil {
		return err
	}
	if err := validateSite(site); err != nil {
		return err
	}
	site.CreatedAt, site.UpdatedAt = now(), now()
	if site.Status == "" {
		site.Status = "active"
	}

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO sites (tenant_id, domain, aliases, type, system_user, root_path,
			document_root, php_version, proxy_target, ssl_enabled, force_https, hsts,
			status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		site.TenantID, site.Domain, encodeList(site.Aliases), string(site.Type),
		site.SystemUser, site.RootPath, site.DocumentRoot, site.PHPVersion, site.ProxyTarget,
		boolToInt(site.SSLEnabled), boolToInt(site.ForceHTTPS), boolToInt(site.HSTS),
		site.Status, site.CreatedAt, site.UpdatedAt)
	if err != nil {
		if isUnique(err) {
			return fmt.Errorf("%w: domain %s", ErrConflict, site.Domain)
		}
		return err
	}
	site.ID, err = res.LastInsertId()
	return err
}

func (s *Store) GetSite(ctx context.Context, sc Scope, id int64) (*Site, error) {
	where, args, err := sc.where("sites", "id = ?")
	if err != nil {
		return nil, err
	}
	return scanSite(s.db.QueryRowContext(ctx, `SELECT `+siteCols+` FROM sites`+where, append(args, id)...))
}

func (s *Store) SiteByDomain(ctx context.Context, sc Scope, domain string) (*Site, error) {
	where, args, err := sc.where("sites", "domain = ?")
	if err != nil {
		return nil, err
	}
	return scanSite(s.db.QueryRowContext(ctx, `SELECT `+siteCols+` FROM sites`+where,
		append(args, strings.ToLower(domain))...))
}

func (s *Store) ListSites(ctx context.Context, sc Scope) ([]*Site, error) {
	where, args, err := sc.where("sites")
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+siteCols+` FROM sites`+where+` ORDER BY domain`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*Site{}
	for rows.Next() {
		site, err := scanSite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, site)
	}
	return out, rows.Err()
}

func (s *Store) UpdateSite(ctx context.Context, sc Scope, site *Site) error {
	if err := sc.owns(site.TenantID); err != nil {
		return err
	}
	if err := validateSite(site); err != nil {
		return err
	}
	where, args, err := sc.where("sites", "id = ?")
	if err != nil {
		return err
	}
	site.UpdatedAt = now()

	set := []any{encodeList(site.Aliases), string(site.Type), site.DocumentRoot,
		site.PHPVersion, site.ProxyTarget, boolToInt(site.SSLEnabled),
		boolToInt(site.ForceHTTPS), boolToInt(site.HSTS), site.Status, site.UpdatedAt}
	res, err := s.db.ExecContext(ctx, `
		UPDATE sites SET aliases = ?, type = ?, document_root = ?, php_version = ?,
			proxy_target = ?, ssl_enabled = ?, force_https = ?, hsts = ?,
			status = ?, updated_at = ?`+where,
		append(set, append(args, site.ID)...)...)
	return affected(res, err)
}

func (s *Store) DeleteSite(ctx context.Context, sc Scope, id int64) error {
	where, args, err := sc.where("sites", "id = ?")
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM sites`+where, append(args, id)...)
	return affected(res, err)
}

func (s *Store) CountSites(ctx context.Context, sc Scope) (int, error) {
	where, args, err := sc.where("sites")
	if err != nil {
		return 0, err
	}
	var n int
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sites`+where, args...).Scan(&n)
	return n, err
}

func validateSite(site *Site) error {
	site.Domain = strings.ToLower(strings.TrimSpace(site.Domain))
	if !ValidDomain(site.Domain) {
		return fmt.Errorf("%q ist kein gültiger domainname", site.Domain)
	}
	for _, a := range site.Aliases {
		if !ValidDomain(a) {
			return fmt.Errorf("alias %q ist kein gültiger domainname", a)
		}
	}
	if !site.Type.Valid() {
		return fmt.Errorf("unbekannter site-typ %q", site.Type)
	}
	if site.Type == SitePHP && site.PHPVersion == "" {
		return errors.New("php-site ohne php-version")
	}
	if site.Type == SiteProxy && site.ProxyTarget == "" {
		return errors.New("proxy-site ohne ziel")
	}
	// Der DocumentRoot wird an root_path gehängt; ".." würde aus der Site ausbrechen.
	if strings.Contains(site.DocumentRoot, "..") || strings.HasPrefix(site.DocumentRoot, "/") {
		return fmt.Errorf("document_root %q muss ein relativer pfad ohne .. sein", site.DocumentRoot)
	}
	return nil
}

func scanSite(sc scanner) (*Site, error) {
	var site Site
	var aliases, typ string
	var ssl, force, hsts int
	err := sc.Scan(&site.ID, &site.TenantID, &site.Domain, &aliases, &typ, &site.SystemUser,
		&site.RootPath, &site.DocumentRoot, &site.PHPVersion, &site.ProxyTarget,
		&ssl, &force, &hsts, &site.Status, &site.CreatedAt, &site.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	site.Aliases, site.Type = decodeList(aliases), SiteType(typ)
	site.SSLEnabled, site.ForceHTTPS, site.HSTS = ssl == 1, force == 1, hsts == 1
	return &site, nil
}
