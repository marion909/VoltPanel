package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/marion909/voltpanel/internal/gitspec"
)

// Git-Deploy je Site.
//
// Was hier steht, geht am Ende in Argumente von git und in eine Liste von
// Buildschritten. Geprüft wird deshalb schon beim Speichern — mit denselben
// Funktionen, die der Agent später noch einmal anwendet. Zwei Prüfungen mit
// einer Implementierung: der Store schützt die Datenbank vor Unsinn, der Agent
// den Server vor dem Store, und beide sind sich einig darüber, was gilt.

const deployCols = `id, tenant_id, site_id, repo_url, ref, steps, hook_id,
	hook_secret_enc, auto_deploy, last_release, last_commit, last_status,
	last_log, last_run_at, created_at, updated_at`

type Deploy struct {
	ID       int64    `json:"id"`
	TenantID int64    `json:"tenant_id"`
	SiteID   int64    `json:"site_id"`
	RepoURL  string   `json:"repo_url"`
	Ref      string   `json:"ref"`
	Steps    []string `json:"steps"`

	// HookID ist der öffentliche Teil der Webhook-Adresse.
	HookID string `json:"hook_id"`
	// HookSecretEnc liegt verschlüsselt und wird nie serialisiert. Der Klartext
	// wird beim Einrichten einmal gezeigt und danach nie wieder — wie ein
	// Passwort.
	HookSecretEnc string `json:"-"`
	AutoDeploy    bool   `json:"auto_deploy"`

	LastRelease string `json:"last_release"`
	LastCommit  string `json:"last_commit"`
	LastStatus  string `json:"last_status"`
	LastLog     string `json:"last_log"`
	LastRunAt   int64  `json:"last_run_at"`

	CreatedAt int64 `json:"created_at"`
	UpdatedAt int64 `json:"updated_at"`
}

// NewHookID erzeugt den öffentlichen Teil der Webhook-Adresse.
//
// 16 Byte aus crypto/rand, hexadezimal. Er ist die einzige Angabe, mit der ein
// Aufrufer die richtige Site trifft — ratbar wäre er eine Liste aller Sites des
// Servers, und jeder Treffer ein Deploy, den niemand ausgelöst hat.
func NewHookID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("hook-id erzeugen: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// NewHookSecret erzeugt das Geheimnis für die Signatur.
func NewHookSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("hook-geheimnis erzeugen: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

func (s *Store) CreateDeploy(ctx context.Context, sc Scope, d *Deploy) error {
	if err := sc.owns(d.TenantID); err != nil {
		return err
	}
	if err := validateDeploy(d); err != nil {
		return err
	}
	steps, err := json.Marshal(d.Steps)
	if err != nil {
		return err
	}
	d.CreatedAt, d.UpdatedAt = now(), now()

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO deploys (tenant_id, site_id, repo_url, ref, steps, hook_id,
			hook_secret_enc, auto_deploy, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.TenantID, d.SiteID, d.RepoURL, d.Ref, string(steps), d.HookID,
		d.HookSecretEnc, boolToInt(d.AutoDeploy), d.CreatedAt, d.UpdatedAt)
	if err != nil {
		if isUnique(err) {
			return fmt.Errorf("%w: für diese site gibt es schon einen deploy", ErrConflict)
		}
		return err
	}
	d.ID, err = res.LastInsertId()
	return err
}

func (s *Store) UpdateDeploy(ctx context.Context, sc Scope, d *Deploy) error {
	if err := sc.owns(d.TenantID); err != nil {
		return err
	}
	if err := validateDeploy(d); err != nil {
		return err
	}
	steps, err := json.Marshal(d.Steps)
	if err != nil {
		return err
	}
	d.UpdatedAt = now()

	// hook_id und hook_secret_enc bleiben: sie zu ändern hieße, jede
	// hinterlegte Webhook-Adresse stillschweigend unbrauchbar zu machen.
	res, err := s.db.ExecContext(ctx, `
		UPDATE deploys SET repo_url = ?, ref = ?, steps = ?, auto_deploy = ?, updated_at = ?
		WHERE id = ? AND tenant_id = ?`,
		d.RepoURL, d.Ref, string(steps), boolToInt(d.AutoDeploy), d.UpdatedAt,
		d.ID, d.TenantID)
	return affected(res, err)
}

// RecordDeployRun schreibt das Ergebnis eines Laufs fort.
//
// Ohne Scope: gerufen wird das auch aus dem Webhook, und dort gibt es keinen
// angemeldeten Benutzer. Die Zeile steht schon fest — sie wurde über die
// hook_id gefunden, und mehr als ihr eigenes Ergebnis wird nicht geschrieben.
func (s *Store) RecordDeployRun(ctx context.Context, id int64, release, commit,
	status, log string) error {

	res, err := s.db.ExecContext(ctx, `
		UPDATE deploys SET last_release = ?, last_commit = ?, last_status = ?,
			last_log = ?, last_run_at = ?, updated_at = ?
		WHERE id = ?`,
		release, commit, status, truncateLog(log), now(), now(), id)
	return affected(res, err)
}

// truncateLog begrenzt, was von einem Lauf in der Datenbank landet.
//
// Ein npm-Build schreibt Zehntausende Zeilen. Sie alle zu behalten hieße, die
// Datenbank des Panels mit Buildausgaben zu füllen — und interessant ist das
// Ende, dort steht der Fehler.
func truncateLog(s string) string {
	const max = 64 << 10
	if len(s) <= max {
		return s
	}
	return "… (gekürzt)\n" + s[len(s)-max:]
}

func (s *Store) GetDeploy(ctx context.Context, sc Scope, id int64) (*Deploy, error) {
	d, err := scanDeploy(s.db.QueryRowContext(ctx,
		`SELECT `+deployCols+` FROM deploys WHERE id = ?`, id))
	if err != nil {
		return nil, err
	}
	if err := sc.owns(d.TenantID); err != nil {
		return nil, ErrNotFound
	}
	return d, nil
}

func (s *Store) DeployForSite(ctx context.Context, sc Scope, siteID int64) (*Deploy, error) {
	d, err := scanDeploy(s.db.QueryRowContext(ctx,
		`SELECT `+deployCols+` FROM deploys WHERE site_id = ?`, siteID))
	if err != nil {
		return nil, err
	}
	if err := sc.owns(d.TenantID); err != nil {
		return nil, ErrNotFound
	}
	return d, nil
}

// DeployByHookID sucht den Deploy zu einer Webhook-Adresse.
//
// Ohne Scope, und das ist Absicht: gefragt wird aus dem Webhook heraus, und
// dort gibt es keinen angemeldeten Benutzer. Die hook_id ist der Ausweis —
// deshalb muss sie zufällig sein, und deshalb prüft der Aufrufer danach noch
// die Signatur.
func (s *Store) DeployByHookID(ctx context.Context, hookID string) (*Deploy, error) {
	if len(hookID) != 32 {
		return nil, ErrNotFound
	}
	return scanDeploy(s.db.QueryRowContext(ctx,
		`SELECT `+deployCols+` FROM deploys WHERE hook_id = ?`, hookID))
}

func (s *Store) ListDeploys(ctx context.Context, sc Scope) ([]*Deploy, error) {
	if err := sc.valid(); err != nil {
		return nil, err
	}
	q := `SELECT ` + deployCols + ` FROM deploys`
	var args []any
	if !sc.IsSystem() {
		q += ` WHERE tenant_id = ?`
		args = append(args, sc.TenantID)
	}
	q += ` ORDER BY id`

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*Deploy{}
	for rows.Next() {
		d, err := scanDeploy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) DeleteDeploy(ctx context.Context, sc Scope, id int64) error {
	d, err := s.GetDeploy(ctx, sc, id)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM deploys WHERE id = ? AND tenant_id = ?`, d.ID, d.TenantID)
	return affected(res, err)
}

func scanDeploy(sc scanner) (*Deploy, error) {
	var d Deploy
	var steps string
	var auto int
	err := sc.Scan(&d.ID, &d.TenantID, &d.SiteID, &d.RepoURL, &d.Ref, &steps,
		&d.HookID, &d.HookSecretEnc, &auto, &d.LastRelease, &d.LastCommit,
		&d.LastStatus, &d.LastLog, &d.LastRunAt, &d.CreatedAt, &d.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	d.AutoDeploy = auto != 0
	if err := json.Unmarshal([]byte(steps), &d.Steps); err != nil {
		d.Steps = nil
	}
	return &d, nil
}

// validateDeploy prüft mit denselben Funktionen, die der Agent später anwendet.
//
// Dieselben, nicht ähnliche: eine zweite, nachgebaute Prüfung wäre die Stelle,
// an der die beiden auseinanderlaufen — und dann ließe die eine durch, was die
// andere verbietet, ohne dass jemand es merkt.
func validateDeploy(d *Deploy) error {
	if d.SiteID <= 0 {
		return errors.New("ein deploy braucht eine site")
	}
	clean, err := gitspec.NormalizeURL(d.RepoURL)
	if err != nil {
		return err
	}
	d.RepoURL = clean

	if d.Ref == "" {
		d.Ref = "main"
	}
	if !gitspec.ValidRef(d.Ref) {
		return fmt.Errorf("%q ist kein gültiger branch- oder tagname", d.Ref)
	}
	if len(d.Steps) > 10 {
		return errors.New("höchstens 10 buildschritte")
	}
	for _, step := range d.Steps {
		if !gitspec.ValidStep(step) {
			return fmt.Errorf("%q ist kein bekannter buildschritt", step)
		}
	}
	if len(d.HookID) != 32 {
		return errors.New("die hook-id fehlt oder ist zu kurz")
	}
	if d.HookSecretEnc == "" {
		return errors.New("das hook-geheimnis fehlt")
	}
	return nil
}
