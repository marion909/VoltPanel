package store

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
)

// Plugins: der Installationszustand aus dem festen Katalog.
//
// Anders als fast alles sonst in diesem Store hat diese Tabelle keine
// tenant_id — ein Plugin ist eine Eigenschaft des Servers, nicht eines
// Mandanten, genau wie Docker oder die Firewall. Der Zugriff wird deshalb
// nicht über einen Scope geregelt, sondern eine Ebene höher über die Rolle:
// nur ein Administrator darf diese Zeilen überhaupt sehen.

// rePluginID ist derselbe strenge Namensraum wie bei einer Fähigkeit
// (internal/agent/ops_feature.go) — die id landet in Dateinamen und
// Dienstverweisen und kommt aus dem Katalog, nie aus einer Anfrage.
var rePluginID = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}[a-z0-9]$`)

// ValidPluginID prüft eine Katalog-Kennung.
func ValidPluginID(id string) bool { return rePluginID.MatchString(id) }

// Plugin ist der Installationszustand eines einzelnen Katalogeintrags.
type Plugin struct {
	ID          string `json:"id"`
	Enabled     bool   `json:"enabled"`
	Config      string `json:"config"`
	InstalledAt *int64 `json:"installed_at,omitempty"`
	UpdatedAt   int64  `json:"updated_at"`
}

const pluginCols = `id, enabled, config, installed_at, updated_at`

// ListPlugins liefert den Zustand aller je installierten Plugins.
//
// Ein Katalogeintrag ohne Zeile hier gilt als nie installiert — das
// unterscheidet der Aufrufer, indem er die Liste gegen seinen eigenen Katalog
// hält.
func (s *Store) ListPlugins(ctx context.Context) ([]*Plugin, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+pluginCols+` FROM plugins ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*Plugin{}
	for rows.Next() {
		p, err := scanPlugin(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetPlugin liefert eine einzelne Zeile, ErrNotFound wenn sie fehlt.
func (s *Store) GetPlugin(ctx context.Context, id string) (*Plugin, error) {
	return scanPlugin(s.db.QueryRowContext(ctx,
		`SELECT `+pluginCols+` FROM plugins WHERE id = ?`, id))
}

// SetPlugin legt die Zeile an oder ändert sie — je nachdem, ob es sie schon
// gibt.
//
// installed wird nur beim ersten Mal gesetzt: eine erneute Installation
// desselben Plugins soll den ursprünglichen Zeitpunkt nicht verlieren.
func (s *Store) SetPlugin(ctx context.Context, id string, enabled bool, config string) error {
	if !ValidPluginID(id) {
		return errors.New("ungültige plugin-kennung")
	}
	ts := now()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO plugins (id, enabled, config, installed_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			enabled = excluded.enabled,
			config = excluded.config,
			updated_at = excluded.updated_at`,
		id, boolToInt(enabled), config, ts, ts)
	return err
}

// DeletePlugin nimmt die Zeile wieder heraus — nach einer Deinstallation.
func (s *Store) DeletePlugin(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM plugins WHERE id = ?`, id)
	return err
}

func scanPlugin(row scanner) (*Plugin, error) {
	var p Plugin
	var enabled int
	var installed sql.NullInt64
	err := row.Scan(&p.ID, &enabled, &p.Config, &installed, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	p.Enabled = enabled != 0
	if installed.Valid {
		p.InstalledAt = &installed.Int64
	}
	return &p, nil
}
