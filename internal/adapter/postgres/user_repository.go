package postgres

import (
	"context"

	"github.com/hisho-123/stock_notification_backend/internal/domain"
)

func (s *Store) FindUser(ctx context.Context, id string) (domain.User, error) {
	var u domain.User
	err := s.q.QueryRow(ctx, `SELECT login_id,password_hash,line_user_id,line_following,created_at,updated_at FROM users WHERE login_id=$1`, id).Scan(&u.LoginID, &u.PasswordHash, &u.LINEUserID, &u.LINEFollowing, &u.CreatedAt, &u.UpdatedAt)
	return u, s.mapError(err)
}
