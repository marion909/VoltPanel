package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const backupTargetCols = `id, tenant_id, name, kind, endpoint, region, bucket, path_style,
	host, port, use_tls, skip_verify, username, secret_enc, base_path, enabled,
	last_error, last_used_at, last_ok_at, created_at, updated_at`

// reTargetName: der Name steht in Auswahllisten und im Audit-Log. Er darf
// aussagekräftig sein, aber nichts enthalten, was eine Zeile umbricht.
var reTargetName = regexp.MustCompile(`^[\p{L}\p{N} ._-]{2,48}$`)

// validKind sind die Arten, für die es einen Transport gibt. Eine vierte
// einzutragen, ohne sie zu bauen, wäre genau der Fehler, den dieses Schema
// schon dreimal gemacht hat.
var validKind = map[string]bool{"s3": true, "b2": true, "ftp": true}

func validateTarget(t *BackupTarget) error {
	t.Name = strings.TrimSpace(t.Name)
	if !reTargetName.MatchString(t.Name) {
		return fmt.Errorf("der name %q passt nicht — erlaubt sind 2 bis 48 zeichen "+
			"aus buchstaben, ziffern, leerzeichen, punkt, strich und unterstrich", t.Name)
	}
	if !validKind[t.Kind] {
		return fmt.Errorf("die art %q ist unbekannt (s3, b2, ftp)", t.Kind)
	}

	// Die eigentliche Prüfung der Felder steht im transfer-Paket, wo sie
	// gebraucht wird. Hier nur das, was ohne den Transport gilt: der
	// Zeilenumbruch, der in jedem der drei Protokolle etwas anderes bedeutet.
	for name, v := range map[string]string{
		"endpunkt": t.Endpoint, "region": t.Region, "bucket": t.Bucket,
		"host": t.Host, "benutzername": t.Username, "pfad": t.BasePath,
	} {
		if strings.ContainsAny(v, "\r\n\x00") {
			return fmt.Errorf("%s darf keinen zeilenumbruch enthalten", name)
		}
	}

	switch t.Kind {
	case "s3", "b2":
		if t.Endpoint == "" || t.Bucket == "" || t.Region == "" {
			return errors.New("für einen s3-speicher braucht es endpunkt, region und bucket")
		}
	case "ftp":
		if t.Host == "" {
			return errors.New("für einen ftp-server braucht es den host")
		}
		if t.Port == 0 {
			t.Port = 21
		}
		if t.Port < 1 || t.Port > 65535 {
			return fmt.Errorf("der port %d liegt ausserhalb des gültigen bereichs", t.Port)
		}
	}
	return nil
}

func (s *Store) CreateBackupTarget(ctx context.Context, sc Scope, t *BackupTarget) error {
	if err := sc.owns(t.TenantID); err != nil {
		return err
	}
	if err := validateTarget(t); err != nil {
		return err
	}
	t.CreatedAt, t.UpdatedAt = now(), now()

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO backup_targets (tenant_id, name, kind, endpoint, region, bucket,
			path_style, host, port, use_tls, skip_verify, username, secret_enc,
			base_path, enabled, last_error, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', ?, ?)`,
		t.TenantID, t.Name, t.Kind, t.Endpoint, t.Region, t.Bucket,
		boolToInt(t.PathStyle), t.Host, t.Port, boolToInt(t.UseTLS), boolToInt(t.SkipVerify),
		t.Username, t.SecretEnc, t.BasePath, boolToInt(t.Enabled), t.CreatedAt, t.UpdatedAt)
	if err != nil {
		if isUnique(err) {
			return fmt.Errorf("%w: ein ziel namens %q gibt es schon", ErrConflict, t.Name)
		}
		return err
	}
	t.ID, err = res.LastInsertId()
	return err
}

func (s *Store) UpdateBackupTarget(ctx context.Context, sc Scope, t *BackupTarget) error {
	if err := validateTarget(t); err != nil {
		return err
	}
	where, args, err := sc.where("backup_targets", "id = ?")
	if err != nil {
		return err
	}
	t.UpdatedAt = now()

	set := []any{t.Name, t.Kind, t.Endpoint, t.Region, t.Bucket, boolToInt(t.PathStyle),
		t.Host, t.Port, boolToInt(t.UseTLS), boolToInt(t.SkipVerify), t.Username,
		t.SecretEnc, t.BasePath, boolToInt(t.Enabled), t.UpdatedAt}

	res, err := s.db.ExecContext(ctx, `
		UPDATE backup_targets SET name = ?, kind = ?, endpoint = ?, region = ?, bucket = ?,
			path_style = ?, host = ?, port = ?, use_tls = ?, skip_verify = ?, username = ?,
			secret_enc = ?, base_path = ?, enabled = ?, updated_at = ?`+where,
		append(append(set, args...), t.ID)...)
	if err != nil {
		if isUnique(err) {
			return fmt.Errorf("%w: ein ziel namens %q gibt es schon", ErrConflict, t.Name)
		}
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkBackupTarget hält fest, wie der letzte Versuch ausging.
//
// Getrennt vom übrigen Update, weil es nach jedem Lauf passiert und die
// Angaben des Kunden dabei nicht angefasst werden dürfen — sonst überschriebe
// ein nächtlicher Lauf eine Änderung, die jemand gerade gespeichert hat.
func (s *Store) MarkBackupTarget(ctx context.Context, sc Scope, id int64, failure string) error {
	where, args, err := sc.where("backup_targets", "id = ?")
	if err != nil {
		return err
	}
	stamp := now()
	var okAt any
	if failure == "" {
		okAt = stamp
	}

	_, err = s.db.ExecContext(ctx, `
		UPDATE backup_targets SET last_error = ?, last_used_at = ?,
			last_ok_at = COALESCE(?, last_ok_at)`+where,
		append(append([]any{failure, stamp, okAt}, args...), id)...)
	return err
}

func (s *Store) GetBackupTarget(ctx context.Context, sc Scope, id int64) (*BackupTarget, error) {
	where, args, err := sc.where("backup_targets", "id = ?")
	if err != nil {
		return nil, err
	}
	return scanBackupTarget(s.db.QueryRowContext(ctx,
		`SELECT `+backupTargetCols+` FROM backup_targets`+where, append(args, id)...))
}

func (s *Store) ListBackupTargets(ctx context.Context, sc Scope) ([]*BackupTarget, error) {
	where, args, err := sc.where("backup_targets")
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+backupTargetCols+` FROM backup_targets`+where+` ORDER BY name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*BackupTarget{}
	for rows.Next() {
		t, err := scanBackupTarget(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) DeleteBackupTarget(ctx context.Context, sc Scope, id int64) error {
	where, args, err := sc.where("backup_targets", "id = ?")
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM backup_targets`+where, append(args, id)...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func scanBackupTarget(sc scanner) (*BackupTarget, error) {
	var t BackupTarget
	var pathStyle, useTLS, skipVerify, enabled int
	err := sc.Scan(&t.ID, &t.TenantID, &t.Name, &t.Kind, &t.Endpoint, &t.Region, &t.Bucket,
		&pathStyle, &t.Host, &t.Port, &useTLS, &skipVerify, &t.Username, &t.SecretEnc,
		&t.BasePath, &enabled, &t.LastError, &t.LastUsedAt, &t.LastOKAt,
		&t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	t.PathStyle, t.UseTLS = pathStyle == 1, useTLS == 1
	t.SkipVerify, t.Enabled = skipVerify == 1, enabled == 1
	// Das Geheimnis selbst verlässt den Server nicht; die Oberfläche muss aber
	// wissen, ob eines hinterlegt ist, um "unverändert" von "leer" zu trennen.
	t.HasSecret = t.SecretEnc != ""
	return &t, nil
}
