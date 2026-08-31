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
	status, disk_bytes, disk_files, disk_measured_at, traffic_bytes, traffic_period,
	created_at, updated_at`

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

// RecordSiteUsage schreibt den gemessenen Verbrauch zurück.
//
// Läuft aus dem Quota-Job und deshalb ohne Tenant-Scope — gemessen wird immer
// über alle Sites, und die Zuordnung steht in der Zeile selbst.
func (s *Store) RecordSiteUsage(ctx context.Context, siteID, bytes, files int64) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE sites SET disk_bytes = ?, disk_files = ?, disk_measured_at = ?
		WHERE id = ?`, bytes, files, now(), siteID)
	return err
}

// AddSiteTraffic zählt Traffic auf. period ist der Abrechnungszeitraum als
// "2026-08"; wechselt er, beginnt der Zähler wieder bei null.
func (s *Store) AddSiteTraffic(ctx context.Context, siteID int64, bytes int64, period string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE sites
		SET traffic_bytes  = CASE WHEN traffic_period = ? THEN traffic_bytes + ? ELSE ? END,
		    traffic_period = ?
		WHERE id = ?`, period, bytes, bytes, period, siteID)
	return err
}

// TenantUsage summiert den Verbrauch aller Sites eines Tenants.
type TenantUsage struct {
	TenantID     int64 `json:"tenant_id"`
	DiskBytes    int64 `json:"disk_bytes"`
	DiskFiles    int64 `json:"disk_files"`
	TrafficBytes int64 `json:"traffic_bytes"`
	Sites        int   `json:"sites"`
	Databases    int   `json:"databases"`
	Cronjobs     int   `json:"cronjobs"`
	FTPAccounts  int   `json:"ftp_accounts"`
}

// UsageForTenant sammelt alle Zählstände, gegen die Quotas geprüft werden.
func (s *Store) UsageForTenant(ctx context.Context, sc Scope, tenantID int64) (*TenantUsage, error) {
	if err := sc.owns(tenantID); err != nil {
		return nil, err
	}

	usage := &TenantUsage{TenantID: tenantID}
	err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(disk_bytes), 0), COALESCE(SUM(disk_files), 0),
		       COALESCE(SUM(traffic_bytes), 0), COUNT(*)
		FROM sites WHERE tenant_id = ?`, tenantID).
		Scan(&usage.DiskBytes, &usage.DiskFiles, &usage.TrafficBytes, &usage.Sites)
	if err != nil {
		return nil, err
	}

	// Die übrigen Zähler einzeln — ein Join über vier Tabellen mit COUNT
	// liefert Kreuzprodukte statt Zählständen.
	for table, target := range map[string]*int{
		"databases":    &usage.Databases,
		"cronjobs":     &usage.Cronjobs,
		"ftp_accounts": &usage.FTPAccounts,
	} {
		if err := s.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM `+table+` WHERE tenant_id = ?`, tenantID).Scan(target); err != nil {
			return nil, err
		}
	}
	return usage, nil
}

// CountFTPAccounts zählt die FTP-Zugänge eines Scopes. Die Verwaltung selbst
// kommt erst mit Pure-FTPd; die Quota-Prüfung braucht den Zähler schon jetzt.
func (s *Store) CountFTPAccounts(ctx context.Context, sc Scope) (int, error) {
	where, args, err := sc.where("ftp_accounts")
	if err != nil {
		return 0, err
	}
	var n int
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM ftp_accounts`+where, args...).Scan(&n)
	return n, err
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
		&ssl, &force, &hsts, &site.Status,
		&site.DiskBytes, &site.DiskFiles, &site.DiskMeasuredAt,
		&site.TrafficBytes, &site.TrafficPeriod,
		&site.CreatedAt, &site.UpdatedAt)
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
