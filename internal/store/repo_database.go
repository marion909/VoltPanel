package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// MySQL erlaubt in Bezeichnern mehr, als gut für uns wäre. Wir schränken
// bewusst enger ein: Diese Namen landen als Identifier in DDL-Anweisungen, die
// sich nicht parametrisieren lassen — je kleiner die erlaubte Zeichenmenge,
// desto weniger muss man dem Quoting vertrauen.
var (
	reDBName   = regexp.MustCompile(`^[a-z][a-z0-9_]{2,47}$`)
	reDBUser   = regexp.MustCompile(`^[a-z][a-z0-9_]{2,30}$`)
	reHostPat  = regexp.MustCompile(`^(localhost|%|[0-9.%]{1,45}|[a-z0-9.\-%]{1,60})$`)
	reCharset  = regexp.MustCompile(`^[a-z0-9_]{2,32}$`)
	reCollate  = regexp.MustCompile(`^[a-z0-9_]{2,64}$`)
	validGrant = map[string]bool{"ALL": true, "READONLY": true, "READWRITE": true}
)

// ValidDBName sagt, ob ein Datenbankname als SQL-Identifier taugt.
func ValidDBName(s string) bool { return reDBName.MatchString(s) }

// reNameInput ist das, was ein Mensch eintippen darf, bevor der Tenant-Präfix
// davorkommt und Leerzeichen sowie Bindestriche zu Unterstrichen werden.
//
// Bewusst großzügiger als reDBName, aber nicht beliebig: "mein-shop" ist eine
// vernünftige Eingabe, "x; DROP DATABASE mysql" nicht. Beides ließe sich
// gefahrlos normalisieren — aber eine Eingabe stillschweigend umzuschreiben
// verbirgt einen Angriffsversuch, statt ihn sichtbar zu machen.
var reNameInput = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9 _-]{1,45}$`)

// ValidNameInput prüft eine vom Benutzer eingegebene Bezeichnung, bevor sie zu
// einem Datenbank- oder Benutzernamen normalisiert wird.
func ValidNameInput(s string) bool { return reNameInput.MatchString(strings.TrimSpace(s)) }

// ValidDBUser sagt, ob ein Benutzername als SQL-Identifier taugt.
// Die Obergrenze von 31 Zeichen liegt unter dem MySQL-Limit von 32.
func ValidDBUser(s string) bool { return reDBUser.MatchString(s) }

// ValidGrantSet prüft die Berechtigungsstufe.
func ValidGrantSet(s string) bool { return validGrant[strings.ToUpper(s)] }

const dbCols = `id, tenant_id, site_id, name, engine, charset, collation,
	size_bytes, created_at, updated_at`

func (s *Store) CreateDatabase(ctx context.Context, sc Scope, d *Database) error {
	if err := sc.owns(d.TenantID); err != nil {
		return err
	}
	if err := validateDatabase(d); err != nil {
		return err
	}
	d.CreatedAt, d.UpdatedAt = now(), now()

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO databases (tenant_id, site_id, name, engine, charset, collation,
			size_bytes, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.TenantID, nilIfEmpty(d.SiteID), d.Name, d.Engine, d.Charset, d.Collation,
		d.SizeBytes, d.CreatedAt, d.UpdatedAt)
	if err != nil {
		if isUnique(err) {
			return fmt.Errorf("%w: datenbank %s", ErrConflict, d.Name)
		}
		return err
	}
	d.ID, err = res.LastInsertId()
	return err
}

func (s *Store) GetDatabase(ctx context.Context, sc Scope, id int64) (*Database, error) {
	where, args, err := sc.where("databases", "id = ?")
	if err != nil {
		return nil, err
	}
	return scanDatabase(s.db.QueryRowContext(ctx,
		`SELECT `+dbCols+` FROM databases`+where, append(args, id)...))
}

func (s *Store) ListDatabases(ctx context.Context, sc Scope) ([]*Database, error) {
	where, args, err := sc.where("databases")
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+dbCols+` FROM databases`+where+` ORDER BY name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*Database{}
	for rows.Next() {
		d, err := scanDatabase(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) CountDatabases(ctx context.Context, sc Scope) (int, error) {
	where, args, err := sc.where("databases")
	if err != nil {
		return 0, err
	}
	var n int
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM databases`+where, args...).Scan(&n)
	return n, err
}

// UpdateDatabaseSize schreibt die gemessene Größe zurück. Läuft aus einem
// Hintergrund-Job und deshalb ohne Tenant-Scope.
func (s *Store) UpdateDatabaseSize(ctx context.Context, name string, bytes int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE databases SET size_bytes = ?, updated_at = ? WHERE name = ?`, bytes, now(), name)
	return err
}

func (s *Store) DeleteDatabase(ctx context.Context, sc Scope, id int64) error {
	where, args, err := sc.where("databases", "id = ?")
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM databases`+where, append(args, id)...)
	return affected(res, err)
}

func validateDatabase(d *Database) error {
	d.Name = strings.ToLower(strings.TrimSpace(d.Name))
	if !ValidDBName(d.Name) {
		return fmt.Errorf("datenbankname %q: erlaubt sind 3–48 zeichen a-z, 0-9 und unterstrich, beginnend mit einem buchstaben", d.Name)
	}
	if d.Engine == "" {
		d.Engine = "mariadb"
	}
	if d.Charset == "" {
		d.Charset = "utf8mb4"
	}
	if d.Collation == "" {
		d.Collation = "utf8mb4_unicode_ci"
	}
	if !reCharset.MatchString(d.Charset) {
		return fmt.Errorf("zeichensatz %q ist ungültig", d.Charset)
	}
	if !reCollate.MatchString(d.Collation) {
		return fmt.Errorf("sortierung %q ist ungültig", d.Collation)
	}
	return nil
}

func scanDatabase(sc scanner) (*Database, error) {
	var d Database
	err := sc.Scan(&d.ID, &d.TenantID, &d.SiteID, &d.Name, &d.Engine, &d.Charset,
		&d.Collation, &d.SizeBytes, &d.CreatedAt, &d.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// --- Datenbankbenutzer -----------------------------------------------------

const dbUserCols = `id, tenant_id, database_id, username, host_pattern, grants,
	password_enc, created_at, updated_at`

func (s *Store) CreateDBUser(ctx context.Context, sc Scope, u *DBUser) error {
	if err := sc.owns(u.TenantID); err != nil {
		return err
	}
	if err := validateDBUser(u); err != nil {
		return err
	}
	u.CreatedAt, u.UpdatedAt = now(), now()

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO db_users (tenant_id, database_id, username, host_pattern, grants,
			password_enc, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		u.TenantID, u.DatabaseID, u.Username, u.HostPattern, u.Grants,
		u.PasswordEnc, u.CreatedAt, u.UpdatedAt)
	if err != nil {
		if isUnique(err) {
			return fmt.Errorf("%w: benutzer %s@%s", ErrConflict, u.Username, u.HostPattern)
		}
		return err
	}
	u.ID, err = res.LastInsertId()
	return err
}

func (s *Store) GetDBUser(ctx context.Context, sc Scope, id int64) (*DBUser, error) {
	where, args, err := sc.where("db_users", "id = ?")
	if err != nil {
		return nil, err
	}
	return scanDBUser(s.db.QueryRowContext(ctx,
		`SELECT `+dbUserCols+` FROM db_users`+where, append(args, id)...))
}

// ListDBUsers liefert die Benutzer einer Datenbank, oder alle des Scopes,
// wenn databaseID 0 ist.
func (s *Store) ListDBUsers(ctx context.Context, sc Scope, databaseID int64) ([]*DBUser, error) {
	var extra []string
	if databaseID > 0 {
		extra = append(extra, "database_id = ?")
	}
	where, args, err := sc.where("db_users", extra...)
	if err != nil {
		return nil, err
	}
	if databaseID > 0 {
		args = append(args, databaseID)
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT `+dbUserCols+` FROM db_users`+where+` ORDER BY username`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*DBUser{}
	for rows.Next() {
		u, err := scanDBUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) UpdateDBUser(ctx context.Context, sc Scope, u *DBUser) error {
	if err := sc.owns(u.TenantID); err != nil {
		return err
	}
	if err := validateDBUser(u); err != nil {
		return err
	}
	where, args, err := sc.where("db_users", "id = ?")
	if err != nil {
		return err
	}
	u.UpdatedAt = now()

	set := []any{u.Grants, u.HostPattern, u.PasswordEnc, u.UpdatedAt}
	res, err := s.db.ExecContext(ctx, `
		UPDATE db_users SET grants = ?, host_pattern = ?, password_enc = ?, updated_at = ?`+where,
		append(set, append(args, u.ID)...)...)
	return affected(res, err)
}

func (s *Store) DeleteDBUser(ctx context.Context, sc Scope, id int64) error {
	where, args, err := sc.where("db_users", "id = ?")
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM db_users`+where, append(args, id)...)
	return affected(res, err)
}

func validateDBUser(u *DBUser) error {
	u.Username = strings.ToLower(strings.TrimSpace(u.Username))
	if !ValidDBUser(u.Username) {
		return fmt.Errorf("benutzername %q: erlaubt sind 3–31 zeichen a-z, 0-9 und unterstrich, beginnend mit einem buchstaben", u.Username)
	}
	if u.HostPattern == "" {
		u.HostPattern = "localhost"
	}
	if !reHostPat.MatchString(u.HostPattern) {
		return fmt.Errorf("host-muster %q ist ungültig", u.HostPattern)
	}
	if u.Grants == "" {
		u.Grants = "ALL"
	}
	u.Grants = strings.ToUpper(u.Grants)
	if !ValidGrantSet(u.Grants) {
		return fmt.Errorf("berechtigung %q ist unbekannt (ALL, READWRITE, READONLY)", u.Grants)
	}
	if u.DatabaseID <= 0 {
		return errors.New("datenbankbenutzer ohne datenbank")
	}
	return nil
}

func scanDBUser(sc scanner) (*DBUser, error) {
	var u DBUser
	err := sc.Scan(&u.ID, &u.TenantID, &u.DatabaseID, &u.Username, &u.HostPattern,
		&u.Grants, &u.PasswordEnc, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}
