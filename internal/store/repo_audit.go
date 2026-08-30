package store

import (
	"context"
	"encoding/json"
)

// Log schreibt einen Audit-Eintrag. Bewusst ohne Scope-Prüfung: protokolliert
// wird immer, auch wenn die eigentliche Aktion am Scope gescheitert ist.
func (s *Store) Log(ctx context.Context, e *AuditEntry) error {
	if e.Result == "" {
		e.Result = "ok"
	}
	e.CreatedAt = now()
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO audit_log (tenant_id, user_id, actor, action, target_type,
			target_id, detail, ip, result, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.TenantID, e.UserID, e.Actor, e.Action, e.TargetType, e.TargetID,
		e.Detail, e.IP, e.Result, e.CreatedAt)
	if err != nil {
		return err
	}
	e.ID, err = res.LastInsertId()
	return err
}

// Detail serialisiert Zusatzinfos. Der Aufrufer ist dafür verantwortlich, hier
// keine Passwörter oder Tokens hineinzugeben.
func Detail(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// ListAudit liefert das Log des Scopes, neueste zuerst.
func (s *Store) ListAudit(ctx context.Context, sc Scope, limit int) ([]*AuditEntry, error) {
	if err := sc.valid(); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}

	q := `SELECT id, tenant_id, user_id, actor, action, target_type, target_id,
	             detail, ip, result, created_at FROM audit_log`
	var args []any
	if !sc.IsSystem() {
		// Systemweite Einträge (tenant_id IS NULL) sieht ein Tenant nicht.
		q += ` WHERE tenant_id = ?`
		args = append(args, sc.TenantID)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.TenantID, &e.UserID, &e.Actor, &e.Action,
			&e.TargetType, &e.TargetID, &e.Detail, &e.IP, &e.Result, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}
