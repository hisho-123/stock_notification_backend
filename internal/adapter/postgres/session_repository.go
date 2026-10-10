package postgres

import (
	"context"
	"time"

	"github.com/hisho-123/stock_notification_backend/internal/domain"
)

func (s *Store) FindSession(ctx context.Context, hash string) (domain.Session, error) {
	var session domain.Session
	err := s.q.QueryRow(ctx, `SELECT token_hash,csrf_token_hash,created_at,expires_at FROM sessions WHERE token_hash=$1`, hash).Scan(&session.TokenHash, &session.CSRFTokenHash, &session.CreatedAt, &session.ExpiresAt)
	return session, s.mapError(err)
}
func (s *Store) ReplaceSession(ctx context.Context, oldHash string, session domain.Session) error {
	tx, err := s.b.Begin(ctx)
	if err != nil {
		return s.mapError(err)
	}
	defer rollback(tx)
	if oldHash != "" {
		if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE token_hash=$1`, oldHash); err != nil {
			return s.mapError(err)
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO sessions (token_hash,csrf_token_hash,created_at,expires_at) VALUES ($1,$2,$3,$4)`, session.TokenHash, session.CSRFTokenHash, session.CreatedAt, session.ExpiresAt)
	if err != nil {
		return s.mapError(err)
	}
	return s.mapError(tx.Commit(ctx))
}
func (s *Store) DeleteSession(ctx context.Context, hash string) error {
	_, err := s.q.Exec(ctx, `DELETE FROM sessions WHERE token_hash=$1`, hash)
	return s.mapError(err)
}
func (s *Store) DeleteExpiredSessions(ctx context.Context, now time.Time) error {
	_, err := s.q.Exec(ctx, `DELETE FROM sessions WHERE expires_at <= $1`, now)
	return s.mapError(err)
}
