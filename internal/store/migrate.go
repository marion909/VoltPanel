package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/marion909/voltpanel/internal/version"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

type migration struct {
	version int
	name    string
	sql     string
}

// Migrate bringt die Datenbank auf den Stand dieses Binaries.
//
// Vorwärts-only: Rollback passiert nicht per Down-Migration, sondern indem
// `volt update` das vorher angelegte DB-Backup zurückspielt. Eine halb
// ausgeführte Down-Migration wäre schlimmer als die Kopie.
func (s *Store) Migrate(ctx context.Context) (from, to int, err error) {
	if _, err = s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INTEGER PRIMARY KEY,
			name       TEXT    NOT NULL,
			applied_at INTEGER NOT NULL
		)`); err != nil {
		return 0, 0, fmt.Errorf("migrationstabelle: %w", err)
	}

	current, err := s.SchemaVersion(ctx)
	if err != nil {
		return 0, 0, err
	}
	if current > version.SchemaVersion {
		return current, current, fmt.Errorf(
			"datenbank hat schema v%d, dieses binary kennt nur v%d — bitte volt aktualisieren statt downgraden",
			current, version.SchemaVersion)
	}

	all, err := loadMigrations()
	if err != nil {
		return current, current, err
	}

	applied := current
	for _, m := range all {
		if m.version <= current {
			continue
		}
		// Jede Migration läuft in einer eigenen Transaktion: bricht die dritte
		// ab, bleiben die ersten beiden gültig und der Neustart macht dort weiter.
		if err := s.Tx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, m.sql); err != nil {
				return fmt.Errorf("migration %04d_%s: %w", m.version, m.name, err)
			}
			_, err := tx.ExecContext(ctx,
				`INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
				m.version, m.name, now())
			return err
		}); err != nil {
			return current, applied, err
		}
		applied = m.version
	}
	return current, applied, nil
}

func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var v sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&v)
	if err != nil {
		// Tabelle fehlt noch => frische Datenbank.
		if strings.Contains(err.Error(), "no such table") {
			return 0, nil
		}
		return 0, err
	}
	return int(v.Int64), nil
}

// loadMigrations liest migrations/NNNN_name.sql und sortiert sie numerisch.
func loadMigrations() ([]migration, error) {
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return nil, err
	}

	out := make([]migration, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		numStr, rest, ok := strings.Cut(strings.TrimSuffix(e.Name(), ".sql"), "_")
		if !ok {
			return nil, fmt.Errorf("migration %q: erwartet NNNN_name.sql", e.Name())
		}
		num, err := strconv.Atoi(numStr)
		if err != nil {
			return nil, fmt.Errorf("migration %q: %q ist keine version", e.Name(), numStr)
		}
		body, err := migrationFS.ReadFile(path.Join("migrations", e.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, migration{version: num, name: rest, sql: string(body)})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	for i := 1; i < len(out); i++ {
		if out[i].version == out[i-1].version {
			return nil, fmt.Errorf("migration %04d ist doppelt vergeben", out[i].version)
		}
	}
	return out, nil
}
