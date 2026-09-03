package store

import (
	"context"
	"database/sql"
	"errors"
)

// Webmail: die eine, server-weite Roundcube-Installation.
//
// Wie bei den Plugins keine tenant_id und kein Scope — Webmail gehört dem
// Server, nicht einem Mandanten. Anders als dort gibt es aber nie mehr als
// eine Zeile: id ist immer 1, es gibt nie eine zweite Installation.

// webmailID ist die einzige jemals gültige Zeilen-ID.
const webmailID = 1

// Webmail ist der Installationszustand.
type Webmail struct {
	Hostname      string `json:"hostname"`
	PHPVersion    string `json:"php_version"`
	DBName        string `json:"db_name"`
	DBUser        string `json:"db_user"`
	DBPasswordEnc string `json:"-"`
	InstalledAt   int64  `json:"installed_at"`
	UpdatedAt     int64  `json:"updated_at"`
}

const webmailCols = `hostname, php_version, db_name, db_user, db_password_enc, installed_at, updated_at`

// GetWebmail liefert die Zeile, ErrNotFound wenn Webmail nie installiert wurde.
func (s *Store) GetWebmail(ctx context.Context) (*Webmail, error) {
	return scanWebmail(s.db.QueryRowContext(ctx,
		`SELECT `+webmailCols+` FROM webmail WHERE id = ?`, webmailID))
}

// SetWebmail legt die Zeile an oder ändert sie — je nachdem, ob es sie schon
// gibt. installed_at wird nur beim ersten Mal gesetzt, aus demselben Grund
// wie bei SetPlugin.
func (s *Store) SetWebmail(ctx context.Context, hostname, phpVersion, dbName, dbUser, dbPasswordEnc string) error {
	ts := now()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO webmail (id, hostname, php_version, db_name, db_user, db_password_enc, installed_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			hostname = excluded.hostname,
			php_version = excluded.php_version,
			db_name = excluded.db_name,
			db_user = excluded.db_user,
			db_password_enc = excluded.db_password_enc,
			updated_at = excluded.updated_at`,
		webmailID, hostname, phpVersion, dbName, dbUser, dbPasswordEnc, ts, ts)
	return err
}

// DeleteWebmail nimmt die Zeile wieder heraus.
func (s *Store) DeleteWebmail(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM webmail WHERE id = ?`, webmailID)
	return err
}

func scanWebmail(row scanner) (*Webmail, error) {
	var w Webmail
	err := row.Scan(&w.Hostname, &w.PHPVersion, &w.DBName, &w.DBUser, &w.DBPasswordEnc,
		&w.InstalledAt, &w.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &w, nil
}
