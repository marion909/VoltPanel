package store

import (
	"context"
	"database/sql"
	"errors"
)

// CreateSession legt eine Session an. id ist bereits der Hash des Tokens —
// der Klartext-Token verlässt den Prozess nur Richtung Cookie und wird nie
// gespeichert, damit ein DB-Leak keine Sitzungsübernahme erlaubt.
func (s *Store) CreateSession(ctx context.Context, sess *Session) error {
	sess.CreatedAt = now()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (id, user_id, tenant_id, user_agent, ip, expires_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		sess.ID, sess.UserID, sess.TenantID, sess.UserAgent, sess.IP, sess.ExpiresAt, sess.CreatedAt)
	return err
}

// GetSession liefert nur nicht abgelaufene Sessions.
func (s *Store) GetSession(ctx context.Context, id string) (*Session, error) {
	var sess Session
	err := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, tenant_id, user_agent, ip, expires_at, created_at
		FROM sessions WHERE id = ? AND expires_at > ?`, id, now()).
		Scan(&sess.ID, &sess.UserID, &sess.TenantID, &sess.UserAgent, &sess.IP,
			&sess.ExpiresAt, &sess.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &sess, nil
}

func (s *Store) TouchSession(ctx context.Context, id string, expiresAt int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET expires_at = ? WHERE id = ?`, expiresAt, id)
	return err
}

func (s *Store) DeleteSession(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id)
	return err
}

// DeleteUserSessions wirft alle Sessions eines Users weg — nach Passwortwechsel
// oder 2FA-Reset.
func (s *Store) DeleteUserSessions(ctx context.Context, userID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID)
	return err
}

// PurgeExpiredSessions räumt abgelaufene Sessions weg; läuft periodisch.
func (s *Store) PurgeExpiredSessions(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, now())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
