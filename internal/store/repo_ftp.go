package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const ftpCols = `id, tenant_id, site_id, username, password_enc, home_dir,
	uid, gid, quota_mb, status, last_error, created_at, updated_at`

// reFTPUser ist eng gefasst, weil der Name als Argument an pure-pw geht und
// dort eine Zeile in der PureDB wird. Ein Doppelpunkt oder ein Zeilenumbruch
// wäre in dieser Datei ein Feldtrenner.
var reFTPUser = regexp.MustCompile(`^[a-z][a-z0-9_]{2,31}$`)

func (s *Store) CreateFTPAccount(ctx context.Context, sc Scope, a *FTPAccount) error {
	if err := sc.owns(a.TenantID); err != nil {
		return err
	}
	// Die Site muss demselben Mandanten gehören — sonst ließe sich ein
	// FTP-Zugang (mit UID/GID/HomeDir) unter der site_id eines fremden
	// Mandanten anlegen.
	if a.SiteID != nil {
		site, err := s.GetSite(ctx, sc, *a.SiteID)
		if err != nil {
			return err
		}
		if site.TenantID != a.TenantID {
			return ErrNotFound
		}
	}
	if err := validateFTPAccount(a); err != nil {
		return err
	}
	a.CreatedAt, a.UpdatedAt = now(), now()

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO ftp_accounts (tenant_id, site_id, username, password_enc, home_dir,
			uid, gid, quota_mb, status, last_error, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.TenantID, nilIfEmpty(a.SiteID), a.Username, a.PasswordEnc, a.HomeDir,
		a.UID, a.GID, a.QuotaMB, a.Status, a.LastError, a.CreatedAt, a.UpdatedAt)
	if err != nil {
		if isUnique(err) {
			return fmt.Errorf("%w: den ftp-zugang %s gibt es schon", ErrConflict, a.Username)
		}
		return err
	}
	a.ID, err = res.LastInsertId()
	return err
}

func (s *Store) GetFTPAccount(ctx context.Context, sc Scope, id int64) (*FTPAccount, error) {
	where, args, err := sc.where("ftp_accounts", "id = ?")
	if err != nil {
		return nil, err
	}
	return scanFTPAccount(s.db.QueryRowContext(ctx,
		`SELECT `+ftpCols+` FROM ftp_accounts`+where, append(args, id)...))
}

// ListFTPAccounts liefert die Zugänge einer Site, oder alle des Scopes, wenn
// siteID 0 ist.
func (s *Store) ListFTPAccounts(ctx context.Context, sc Scope, siteID int64) ([]*FTPAccount, error) {
	var extra []string
	if siteID > 0 {
		extra = append(extra, "site_id = ?")
	}
	where, args, err := sc.where("ftp_accounts", extra...)
	if err != nil {
		return nil, err
	}
	if siteID > 0 {
		args = append(args, siteID)
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT `+ftpCols+` FROM ftp_accounts`+where+` ORDER BY username`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*FTPAccount{}
	for rows.Next() {
		a, err := scanFTPAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) UpdateFTPAccount(ctx context.Context, sc Scope, a *FTPAccount) error {
	if err := sc.owns(a.TenantID); err != nil {
		return err
	}
	if err := validateFTPAccount(a); err != nil {
		return err
	}
	where, args, err := sc.where("ftp_accounts", "id = ?")
	if err != nil {
		return err
	}
	a.UpdatedAt = now()

	set := []any{a.PasswordEnc, a.HomeDir, a.QuotaMB, a.Status, a.LastError, a.UpdatedAt}
	res, err := s.db.ExecContext(ctx, `
		UPDATE ftp_accounts SET password_enc = ?, home_dir = ?, quota_mb = ?,
			status = ?, last_error = ?, updated_at = ?`+where,
		append(set, append(args, a.ID)...)...)
	return affected(res, err)
}

func (s *Store) DeleteFTPAccount(ctx context.Context, sc Scope, id int64) error {
	where, args, err := sc.where("ftp_accounts", "id = ?")
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM ftp_accounts`+where, append(args, id)...)
	return affected(res, err)
}

func validateFTPAccount(a *FTPAccount) error {
	a.Username = strings.ToLower(strings.TrimSpace(a.Username))
	if !reFTPUser.MatchString(a.Username) {
		return fmt.Errorf("%q ist kein gültiger ftp-benutzername — 3 bis 32 zeichen, "+
			"kleinbuchstaben, ziffern und unterstrich, beginnend mit einem buchstaben",
			a.Username)
	}
	if a.HomeDir == "" || !strings.HasPrefix(a.HomeDir, "/") {
		return errors.New("das heimatverzeichnis muss ein absoluter pfad sein")
	}
	if strings.Contains(a.HomeDir, "..") {
		return errors.New("das heimatverzeichnis darf kein .. enthalten")
	}
	if a.UID <= 0 || a.GID <= 0 {
		return errors.New("ein ftp-zugang braucht die uid und gid seiner site")
	}
	if a.QuotaMB < 0 {
		return errors.New("die quota kann nicht negativ sein")
	}
	switch a.Status {
	case "":
		a.Status = "active"
	case "active", "disabled":
	default:
		return fmt.Errorf("unbekannter status %q", a.Status)
	}
	return nil
}

func scanFTPAccount(sc scanner) (*FTPAccount, error) {
	var a FTPAccount
	err := sc.Scan(&a.ID, &a.TenantID, &a.SiteID, &a.Username, &a.PasswordEnc,
		&a.HomeDir, &a.UID, &a.GID, &a.QuotaMB, &a.Status, &a.LastError,
		&a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}
