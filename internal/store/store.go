// Package store kapselt die SQLite-Panel-Datenbank samt Migrationen.
//
// Zwei Regeln gelten hier ausnahmslos:
//   - Jede Tabelle mit tenant_id wird nur über einen Scope gelesen (siehe scope.go).
//   - Kein SQL wird aus User-Input zusammengesetzt; Werte gehen immer als Parameter rein.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // reiner Go-Treiber: kein cgo, damit das Binary statisch bleibt
)

var (
	ErrNotFound  = errors.New("nicht gefunden")
	ErrConflict  = errors.New("existiert bereits")
	ErrNoTenant  = errors.New("kein tenant-scope gesetzt")
	ErrForbidden = errors.New("gehört einem anderen tenant")
)

type Store struct {
	db   *sql.DB
	path string
}

// Open öffnet die Datenbank im WAL-Mode und legt sie bei Bedarf an.
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("datenverzeichnis %s: %w", dir, err)
		}
	}

	// busy_timeout verhindert "database is locked", wenn Web und CLI gleichzeitig
	// schreiben; foreign_keys muss SQLite pro Verbindung explizit gesagt werden.
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)" +
		"&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite öffnen: %w", err)
	}

	// SQLite verträgt genau einen Schreiber. Ein einzelner Pool-Slot ist
	// ehrlicher als Lock-Fehler zur Laufzeit.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlite erreichen: %w", err)
	}

	return &Store{db: db, path: path}, nil
}

func (s *Store) DB() *sql.DB  { return s.db }
func (s *Store) Path() string { return s.path }
func (s *Store) Close() error { return s.db.Close() }

// Tx führt fn in einer Transaktion aus und rollt bei jedem Fehler zurück.
func (s *Store) Tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() // no-op nach erfolgreichem Commit

	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// Backup schreibt eine konsistente Kopie der DB — wird vor jedem Update
// aufgerufen, damit ein fehlgeschlagenes `volt update` zurückrollen kann.
func (s *Store) Backup(ctx context.Context, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}
	// VACUUM INTO erzeugt eine defragmentierte, in sich konsistente Kopie,
	// ohne dass wir den WAL-Zustand von Hand zusammensuchen müssen.
	if _, err := s.db.ExecContext(ctx, "VACUUM INTO ?", dst); err != nil {
		return fmt.Errorf("db-backup nach %s: %w", dst, err)
	}
	return os.Chmod(dst, 0o600)
}

// Setting liest einen Wert aus der settings-Tabelle; fehlt er, kommt def zurück.
func (s *Store) Setting(ctx context.Context, key, def string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return def, nil
	}
	if err != nil {
		return "", err
	}
	return v, nil
}

func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, time.Now().Unix())
	return err
}

func now() int64 { return time.Now().Unix() }
