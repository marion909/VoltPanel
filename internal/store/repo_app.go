package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Apps: eine Anwendung ist eine systemd-Unit plus Reverse-Proxy.
//
// Zwei Werte entstehen hier und werden nicht eingegeben — der Name und der
// Port. Beide sind Eigenschaften der Maschine, nicht des Mandanten, und wer sie
// eingeben ließe, müsste "schon vergeben" antworten können. Das wäre in einem
// Panel mit mehreren Mandanten eine Auskunft über einen fremden.

var (
	reAppName = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}[a-z0-9]$`)
	// Dasselbe Muster wie im templates-Paket. Bewusst hier noch einmal: der
	// Store darf nicht davon abhängen, dass jemand später dort etwas lockert.
	reAppArg = regexp.MustCompile(`^[A-Za-z0-9_./:@=,+-]+$`)
)

const appCols = `id, tenant_id, site_id, name, runtime, args, port, env, enabled,
	created_at, updated_at`

// App ist eine Anwendung hinter dem Reverse-Proxy einer Site.
type App struct {
	ID       int64    `json:"id"`
	TenantID int64    `json:"tenant_id"`
	SiteID   int64    `json:"site_id"`
	Name     string   `json:"name"`
	Runtime  string   `json:"runtime"`
	Args     []string `json:"args"`
	Port     int      `json:"port"`

	// EnvEnc liegt verschlüsselt und wird nie serialisiert. Was hinterlegt ist,
	// sagt EnvKeys — die Namen ohne die Werte.
	EnvEnc string `json:"-"`

	Enabled   bool  `json:"enabled"`
	CreatedAt int64 `json:"created_at"`
	UpdatedAt int64 `json:"updated_at"`
}

// appPortFrom und appPortTo sind der Bereich, aus dem Ports vergeben werden.
//
// Weit oben, damit nichts Bekanntes darin liegt, und unterhalb des Bereichs für
// flüchtige Ports (ab 32768 auf Linux) — sonst könnte eine ausgehende
// Verbindung des Servers den Port belegen, bevor die App ihn bekommt.
const (
	appPortFrom = 21000
	appPortTo   = 21999
)

// AppNameForDomain bildet den Unit-Namen aus der Domain.
//
// Nicht eingegeben, sondern abgeleitet: der Name wird ein Unit- und ein
// Dateiname auf der Maschine und muss über alle Mandanten hinweg eindeutig
// sein. Domains sind es schon.
//
// Zu lange Domains werden gekürzt und mit einem Stück Prüfsumme versehen. Ohne
// die fielen "sehr-lange-domain-eins.example.at" und
// "sehr-lange-domain-zwei.example.at" auf denselben Namen — und die zweite Site
// überschriebe die Unit der ersten.
func AppNameForDomain(domain string) string {
	name := strings.ToLower(strings.TrimSpace(domain))
	name = strings.TrimSuffix(name, ".")
	name = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		default:
			return '-'
		}
	}, name)
	name = strings.Trim(name, "-")

	const max = 32
	if len(name) > max {
		sum := sha256.Sum256([]byte(domain))
		name = name[:max-8] + "-" + hex.EncodeToString(sum[:])[:7]
	}
	// Ein Name muss mit einem Buchstaben beginnen — eine Domain, die mit einer
	// Ziffer anfängt, gibt es durchaus.
	if name == "" || name[0] < 'a' || name[0] > 'z' {
		name = "a" + name
		if len(name) > max {
			name = name[:max]
		}
	}
	return strings.Trim(name, "-")
}

// CreateApp legt eine App an und vergibt ihr einen freien Port.
func (s *Store) CreateApp(ctx context.Context, sc Scope, a *App) error {
	if err := sc.owns(a.TenantID); err != nil {
		return err
	}
	if err := validateApp(a); err != nil {
		return err
	}

	// Port und Einfügen in einer Transaktion: zwischen "welcher ist frei" und
	// "nimm den" darf niemand dazwischenkommen. Der eindeutige Index hielte es
	// auch so, aber mit einem Fehler statt mit dem nächsten freien Port.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if a.Port == 0 {
		a.Port, err = freeAppPort(ctx, tx)
		if err != nil {
			return err
		}
	}
	a.CreatedAt, a.UpdatedAt = now(), now()

	args, err := json.Marshal(a.Args)
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `
		INSERT INTO apps (tenant_id, site_id, name, runtime, args, port, env, enabled,
			created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.TenantID, a.SiteID, a.Name, a.Runtime, string(args), a.Port, a.EnvEnc,
		boolToInt(a.Enabled), a.CreatedAt, a.UpdatedAt)
	if err != nil {
		if isUnique(err) {
			return fmt.Errorf("%w: für diese site gibt es schon eine app", ErrConflict)
		}
		return err
	}
	if a.ID, err = res.LastInsertId(); err != nil {
		return err
	}
	return tx.Commit()
}

// freeAppPort sucht den kleinsten freien Port des Bereichs.
//
// Der kleinste, nicht ein zufälliger: so bleiben die Nummern dicht beieinander
// und eine Firewallregel über den Bereich deckt tatsächlich alles ab.
func freeAppPort(ctx context.Context, tx *sql.Tx) (int, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT port FROM apps WHERE port BETWEEN ? AND ? ORDER BY port`, appPortFrom, appPortTo)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	// Die Zeilen kommen sortiert. Solange der nächste belegte Port genau der
	// gesuchte ist, rückt der gesuchte weiter; sobald eine Lücke kommt, ändert
	// sich nichts mehr.
	//
	// Hier stand ein `break` bei der ersten Lücke. Es war überflüssig — ohne
	// es kommt derselbe Port heraus, es werden nur ein paar Zeilen mehr
	// gelesen. Aufgefallen ist das bei der Gegenprobe: der Test blieb grün,
	// als ich es entfernt habe, und das lag nicht am Test.
	next := appPortFrom
	for rows.Next() {
		var p int
		if err := rows.Scan(&p); err != nil {
			return 0, err
		}
		if p == next {
			next++
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if next > appPortTo {
		return 0, fmt.Errorf("kein freier port zwischen %d und %d", appPortFrom, appPortTo)
	}
	return next, nil
}

func (s *Store) GetApp(ctx context.Context, sc Scope, id int64) (*App, error) {
	a, err := scanApp(s.db.QueryRowContext(ctx,
		`SELECT `+appCols+` FROM apps WHERE id = ?`, id))
	if err != nil {
		return nil, err
	}
	// Erst lesen, dann prüfen: der Scope hängt am tenant_id der Zeile, und den
	// kennt man vorher nicht. Zurückgegeben wird nichts, was nicht passt.
	if err := sc.owns(a.TenantID); err != nil {
		return nil, ErrNotFound
	}
	return a, nil
}

// AppForSite liefert die App einer Site, ErrNotFound wenn es keine gibt.
func (s *Store) AppForSite(ctx context.Context, sc Scope, siteID int64) (*App, error) {
	a, err := scanApp(s.db.QueryRowContext(ctx,
		`SELECT `+appCols+` FROM apps WHERE site_id = ?`, siteID))
	if err != nil {
		return nil, err
	}
	if err := sc.owns(a.TenantID); err != nil {
		return nil, ErrNotFound
	}
	return a, nil
}

func (s *Store) ListApps(ctx context.Context, sc Scope) ([]*App, error) {
	if err := sc.valid(); err != nil {
		return nil, err
	}
	q := `SELECT ` + appCols + ` FROM apps`
	var args []any
	if !sc.IsSystem() {
		q += ` WHERE tenant_id = ?`
		args = append(args, sc.TenantID)
	}
	q += ` ORDER BY name`

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*App{}
	for rows.Next() {
		a, err := scanApp(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) UpdateApp(ctx context.Context, sc Scope, a *App) error {
	if err := sc.owns(a.TenantID); err != nil {
		return err
	}
	if err := validateApp(a); err != nil {
		return err
	}
	args, err := json.Marshal(a.Args)
	if err != nil {
		return err
	}
	a.UpdatedAt = now()

	// tenant_id und site_id stehen bewusst nicht im SET: eine App wandert nicht
	// zu einem anderen Mandanten, und der Port bliebe sonst beim alten.
	res, err := s.db.ExecContext(ctx, `
		UPDATE apps SET runtime = ?, args = ?, env = ?, enabled = ?, updated_at = ?
		WHERE id = ? AND tenant_id = ?`,
		a.Runtime, string(args), a.EnvEnc, boolToInt(a.Enabled), a.UpdatedAt, a.ID, a.TenantID)
	return affected(res, err)
}

func (s *Store) DeleteApp(ctx context.Context, sc Scope, id int64) error {
	a, err := s.GetApp(ctx, sc, id)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM apps WHERE id = ? AND tenant_id = ?`, a.ID, a.TenantID)
	return affected(res, err)
}

func scanApp(sc scanner) (*App, error) {
	var a App
	var args string
	var enabled int
	err := sc.Scan(&a.ID, &a.TenantID, &a.SiteID, &a.Name, &a.Runtime, &args,
		&a.Port, &a.EnvEnc, &enabled, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	a.Enabled = enabled != 0
	if err := json.Unmarshal([]byte(args), &a.Args); err != nil {
		// Eine kaputte Zeile darf nicht die ganze Liste unlesbar machen.
		a.Args = nil
	}
	return &a, nil
}

// validateApp prüft, was nachher in eine Unit-Datei wandert.
//
// Der Agent prüft es noch einmal, und das ist kein Versehen: der Store schützt
// die Datenbank vor Unsinn, der Agent den Server vor dem Store. Wer sich auf
// eine der beiden Prüfungen verlässt, hat die andere umsonst.
func validateApp(a *App) error {
	if a.SiteID <= 0 {
		return errors.New("eine app braucht eine site")
	}
	if !reAppName.MatchString(a.Name) {
		return fmt.Errorf("app-name %q ist ungültig", a.Name)
	}
	if !validAppRuntime(a.Runtime) {
		return fmt.Errorf("laufzeitumgebung %q ist unbekannt", a.Runtime)
	}
	if len(a.Args) > 32 {
		return errors.New("höchstens 32 argumente")
	}
	for _, arg := range a.Args {
		if !reAppArg.MatchString(arg) {
			return fmt.Errorf("das argument %q enthält zeichen, die in einer unit-datei "+
				"nicht sicher stehen können", arg)
		}
	}
	if a.Port != 0 && (a.Port < appPortFrom || a.Port > appPortTo) {
		return fmt.Errorf("der port muss zwischen %d und %d liegen", appPortFrom, appPortTo)
	}
	return nil
}

func validAppRuntime(r string) bool {
	switch r {
	case "node", "npm":
		return true
	}
	return false
}
