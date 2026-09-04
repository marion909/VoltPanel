package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/mail"
	"strings"
)

const userCols = `id, tenant_id, email, display_name, password_hash, role,
	totp_secret, totp_enabled, totp_last_step, must_change_pw, locale, last_login_at,
	failed_logins, locked_until, status, created_at, updated_at`

func (s *Store) CreateUser(ctx context.Context, sc Scope, u *User) error {
	if err := sc.owns(u.TenantID); err != nil {
		return err
	}
	if err := validateUser(u); err != nil {
		return err
	}
	// Ein Kunde darf niemanden über sich selbst hinaus anlegen.
	if !sc.IsSystem() && !sc.Role.CanCrossTenant() && u.Role.CanCrossTenant() {
		return fmt.Errorf("%w: rolle %s darf keinen %s anlegen", ErrForbidden, sc.Role, u.Role)
	}

	u.CreatedAt, u.UpdatedAt = now(), now()
	if u.Locale == "" {
		u.Locale = "de"
	}
	if u.Status == "" {
		u.Status = "active"
	}

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO users (tenant_id, email, display_name, password_hash, role,
			totp_secret, totp_enabled, must_change_pw, locale, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		u.TenantID, u.Email, u.DisplayName, u.PasswordHash, string(u.Role),
		u.TOTPSecret, boolToInt(u.TOTPEnabled), boolToInt(u.MustChangePW),
		u.Locale, u.Status, u.CreatedAt, u.UpdatedAt)
	if err != nil {
		if isUnique(err) {
			return fmt.Errorf("%w: e-mail %s", ErrConflict, u.Email)
		}
		return err
	}
	u.ID, err = res.LastInsertId()
	return err
}

func (s *Store) GetUser(ctx context.Context, sc Scope, id int64) (*User, error) {
	where, args, err := sc.where("users", "id = ?")
	if err != nil {
		return nil, err
	}
	args = append(args, id)
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users`+where, args...))
}

// UserByEmail sucht ohne Tenant-Filter — der Login weiß den Tenant ja noch nicht.
// Bewusst nur für die Authentifizierung; danach gilt der Scope des Users.
func (s *Store) UserByEmail(ctx context.Context, email string) (*User, error) {
	email = normalizeEmail(email)
	return scanUser(s.db.QueryRowContext(ctx,
		`SELECT `+userCols+` FROM users WHERE email = ?`, email))
}

func (s *Store) ListUsers(ctx context.Context, sc Scope) ([]*User, error) {
	where, args, err := sc.where("users")
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+userCols+` FROM users`+where+` ORDER BY email`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) UpdateUser(ctx context.Context, sc Scope, u *User) error {
	if err := sc.owns(u.TenantID); err != nil {
		return err
	}
	if err := validateUser(u); err != nil {
		return err
	}
	where, args, err := sc.where("users", "id = ?")
	if err != nil {
		return err
	}
	u.UpdatedAt = now()

	set := []any{u.Email, u.DisplayName, u.PasswordHash, string(u.Role), u.TOTPSecret,
		boolToInt(u.TOTPEnabled), boolToInt(u.MustChangePW), u.Locale, u.Status, u.UpdatedAt}
	res, err := s.db.ExecContext(ctx, `
		UPDATE users SET email = ?, display_name = ?, password_hash = ?, role = ?,
			totp_secret = ?, totp_enabled = ?, must_change_pw = ?, locale = ?,
			status = ?, updated_at = ?`+where,
		append(set, append(args, u.ID)...)...)
	return affected(res, err)
}

func (s *Store) DeleteUser(ctx context.Context, sc Scope, id int64) error {
	where, args, err := sc.where("users", "id = ?")
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM users`+where, append(args, id)...)
	return affected(res, err)
}

// NoteLoginSuccess setzt den Fehlversuchszähler zurück.
func (s *Store) NoteLoginSuccess(ctx context.Context, userID int64) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE users SET failed_logins = 0, locked_until = NULL, last_login_at = ?, updated_at = ?
		WHERE id = ?`, now(), now(), userID)
	return err
}

// NoteLoginFailure zählt hoch und sperrt ab maxAttempts für lockSeconds.
// Läuft ohne Scope, weil beim Login noch keine Identität feststeht.
func (s *Store) NoteLoginFailure(ctx context.Context, userID int64, maxAttempts, lockSeconds int) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE users
		SET failed_logins = failed_logins + 1,
		    locked_until  = CASE WHEN failed_logins + 1 >= ? THEN ? ELSE locked_until END,
		    updated_at    = ?
		WHERE id = ?`,
		maxAttempts, now()+int64(lockSeconds), now(), userID)
	return err
}

// SetTOTPLastStep merkt sich den zuletzt akzeptierten TOTP-Zeitschritt.
// Läuft wie NoteLoginFailure ohne Scope: der Login kennt beim TOTP-Schritt
// zwar schon den Benutzer, aber der 2FA-Ein-/Ausschalten-Pfad ist bereits
// authentifiziert und braucht keinen zusätzlichen Tenant-Filter.
func (s *Store) SetTOTPLastStep(ctx context.Context, userID, step int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET totp_last_step = ?, updated_at = ? WHERE id = ?`, step, now(), userID)
	return err
}

// CountUsers wird beim Erststart gebraucht, um zu erkennen, ob überhaupt schon
// jemand eingerichtet ist.
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func validateUser(u *User) error {
	u.Email = normalizeEmail(u.Email)
	if _, err := mail.ParseAddress(u.Email); err != nil {
		return fmt.Errorf("%q ist keine gültige e-mail-adresse", u.Email)
	}
	if !u.Role.Valid() {
		return fmt.Errorf("unbekannte rolle %q", u.Role)
	}
	if u.PasswordHash == "" {
		return errors.New("user ohne passwort-hash")
	}
	return nil
}

func normalizeEmail(e string) string { return strings.ToLower(strings.TrimSpace(e)) }

func scanUser(sc scanner) (*User, error) {
	var u User
	var role string
	var totp, mustChange int
	err := sc.Scan(&u.ID, &u.TenantID, &u.Email, &u.DisplayName, &u.PasswordHash, &role,
		&u.TOTPSecret, &totp, &u.TOTPLastStep, &mustChange, &u.Locale, &u.LastLoginAt,
		&u.FailedLogins, &u.LockedUntil, &u.Status, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u.Role, u.TOTPEnabled, u.MustChangePW = Role(role), totp == 1, mustChange == 1
	return &u, nil
}
