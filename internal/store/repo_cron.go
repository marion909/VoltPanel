package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const cronCols = `id, tenant_id, site_id, name, schedule, command, run_as, enabled,
	last_run_at, last_exit_code, last_output, created_at, updated_at`

func (s *Store) CreateCronjob(ctx context.Context, sc Scope, c *Cronjob) error {
	if err := sc.owns(c.TenantID); err != nil {
		return err
	}
	// Die Site muss demselben Mandanten gehören — sonst ließe sich ein
	// Cronjob unter der site_id eines fremden Mandanten anlegen.
	if c.SiteID != nil {
		site, err := s.GetSite(ctx, sc, *c.SiteID)
		if err != nil {
			return err
		}
		if site.TenantID != c.TenantID {
			return ErrNotFound
		}
	}
	if err := validateCronjob(c); err != nil {
		return err
	}
	c.CreatedAt, c.UpdatedAt = now(), now()

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO cronjobs (tenant_id, site_id, name, schedule, command, run_as,
			enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.TenantID, nilIfEmpty(c.SiteID), c.Name, c.Schedule, c.Command, c.RunAs,
		boolToInt(c.Enabled), c.CreatedAt, c.UpdatedAt)
	if err != nil {
		return err
	}
	c.ID, err = res.LastInsertId()
	return err
}

func (s *Store) GetCronjob(ctx context.Context, sc Scope, id int64) (*Cronjob, error) {
	where, args, err := sc.where("cronjobs", "id = ?")
	if err != nil {
		return nil, err
	}
	return scanCronjob(s.db.QueryRowContext(ctx,
		`SELECT `+cronCols+` FROM cronjobs`+where, append(args, id)...))
}

func (s *Store) ListCronjobs(ctx context.Context, sc Scope) ([]*Cronjob, error) {
	where, args, err := sc.where("cronjobs")
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+cronCols+` FROM cronjobs`+where+` ORDER BY name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*Cronjob{}
	for rows.Next() {
		c, err := scanCronjob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) CountCronjobs(ctx context.Context, sc Scope) (int, error) {
	where, args, err := sc.where("cronjobs")
	if err != nil {
		return 0, err
	}
	var n int
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM cronjobs`+where, args...).Scan(&n)
	return n, err
}

func (s *Store) UpdateCronjob(ctx context.Context, sc Scope, c *Cronjob) error {
	if err := sc.owns(c.TenantID); err != nil {
		return err
	}
	if err := validateCronjob(c); err != nil {
		return err
	}
	where, args, err := sc.where("cronjobs", "id = ?")
	if err != nil {
		return err
	}
	c.UpdatedAt = now()

	set := []any{c.Name, c.Schedule, c.Command, boolToInt(c.Enabled), c.UpdatedAt}
	res, err := s.db.ExecContext(ctx, `
		UPDATE cronjobs SET name = ?, schedule = ?, command = ?, enabled = ?, updated_at = ?`+where,
		append(set, append(args, c.ID)...)...)
	return affected(res, err)
}

// NoteCronRun hält das Ergebnis eines Laufs fest. Ohne Scope, weil der Aufruf
// aus dem Job selbst kommt.
func (s *Store) NoteCronRun(ctx context.Context, id int64, exitCode int, output string) error {
	// Die Ausgabe wird gedeckelt: ein Skript, das Megabyte auf stdout schreibt,
	// darf die Panel-Datenbank nicht aufblähen.
	const maxOutput = 16 << 10
	if len(output) > maxOutput {
		output = output[:maxOutput] + "\n… gekürzt"
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE cronjobs SET last_run_at = ?, last_exit_code = ?, last_output = ?, updated_at = ?
		WHERE id = ?`, now(), exitCode, output, now(), id)
	return err
}

func (s *Store) DeleteCronjob(ctx context.Context, sc Scope, id int64) error {
	where, args, err := sc.where("cronjobs", "id = ?")
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM cronjobs`+where, append(args, id)...)
	return affected(res, err)
}

var reCronName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9 _.-]{1,63}$`)

func validateCronjob(c *Cronjob) error {
	c.Name = strings.TrimSpace(c.Name)
	if !reCronName.MatchString(c.Name) {
		return fmt.Errorf("name %q: erlaubt sind 2–64 zeichen aus buchstaben, ziffern, leerzeichen, punkt, unterstrich und bindestrich", c.Name)
	}
	if err := ValidateCronSchedule(c.Schedule); err != nil {
		return err
	}
	if strings.TrimSpace(c.Command) == "" {
		return errors.New("cronjob ohne kommando")
	}
	// Das Kommando landet als Zeile in einer crontab-Datei. Ein Zeilenumbruch
	// darin wäre eine zusätzliche, frei wählbare Zeile — also ein weiterer Job.
	if strings.ContainsAny(c.Command, "\n\r\x00") {
		return errors.New("kommando darf nur eine zeile sein")
	}
	if len(c.Command) > 1000 {
		return errors.New("kommando ist länger als 1000 zeichen")
	}
	return nil
}

// ValidateCronSchedule prüft einen Zeitplan im klassischen 5-Feld-Format.
//
// Die Prüfung ist streng, weil der Wert unverändert in eine crontab-Datei
// geschrieben wird: ein sechstes Feld würde dort als Benutzername gelesen und
// den Job unter einem fremden Konto starten.
func ValidateCronSchedule(schedule string) error {
	schedule = strings.TrimSpace(schedule)
	if schedule == "" {
		return errors.New("zeitplan fehlt")
	}

	// Die @-Kurzformen deckt cron selbst ab.
	switch schedule {
	case "@yearly", "@annually", "@monthly", "@weekly", "@daily", "@midnight", "@hourly":
		return nil
	}
	if strings.HasPrefix(schedule, "@") {
		return fmt.Errorf("kurzform %q ist unbekannt (@daily, @hourly, …)", schedule)
	}

	fields := strings.Fields(schedule)
	if len(fields) != 5 {
		return fmt.Errorf("zeitplan braucht genau 5 felder (minute stunde tag monat wochentag), hat aber %d", len(fields))
	}

	ranges := [5][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 7}}
	names := [5]string{"minute", "stunde", "tag", "monat", "wochentag"}
	for i, field := range fields {
		if err := validateCronField(field, ranges[i][0], ranges[i][1]); err != nil {
			return fmt.Errorf("feld %s: %w", names[i], err)
		}
	}
	return nil
}

// validateCronField versteht *, Zahlen, Listen (1,2), Bereiche (1-5) und
// Schrittweiten (*/5, 1-10/2).
func validateCronField(field string, min, max int) error {
	for _, part := range strings.Split(field, ",") {
		if part == "" {
			return errors.New("leerer listeneintrag")
		}

		value, step, hasStep := strings.Cut(part, "/")
		if hasStep {
			n, err := strconv.Atoi(step)
			if err != nil || n < 1 || n > max {
				return fmt.Errorf("schrittweite %q ist ungültig", step)
			}
		}

		if value == "*" {
			continue
		}
		lo, hi, isRange := strings.Cut(value, "-")
		if err := checkCronNumber(lo, min, max); err != nil {
			return err
		}
		if isRange {
			if err := checkCronNumber(hi, min, max); err != nil {
				return err
			}
			a, _ := strconv.Atoi(lo)
			b, _ := strconv.Atoi(hi)
			if a > b {
				return fmt.Errorf("bereich %q läuft rückwärts", value)
			}
		}
	}
	return nil
}

func checkCronNumber(s string, min, max int) error {
	n, err := strconv.Atoi(s)
	if err != nil {
		return fmt.Errorf("%q ist keine zahl", s)
	}
	if n < min || n > max {
		return fmt.Errorf("%d liegt außerhalb von %d–%d", n, min, max)
	}
	return nil
}

func scanCronjob(sc scanner) (*Cronjob, error) {
	var c Cronjob
	var enabled int
	err := sc.Scan(&c.ID, &c.TenantID, &c.SiteID, &c.Name, &c.Schedule, &c.Command,
		&c.RunAs, &enabled, &c.LastRunAt, &c.LastExitCode, &c.LastOutput,
		&c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	c.Enabled = enabled == 1
	return &c, nil
}
