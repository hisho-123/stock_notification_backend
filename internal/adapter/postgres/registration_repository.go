package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/hisho-123/stock_notification_backend/internal/domain"
	"github.com/hisho-123/stock_notification_backend/internal/usecase"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// 未登録のLINEにも使えるトランザクション単位のロックで、登録・再発行・ブロックを直列化する。
func lockLINE(ctx context.Context, tx pgx.Tx, line string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, line)
	return err
}
func validRegistration(ctx context.Context, q queryer, hash string, now time.Time) (domain.RegistrationToken, error) {
	var token domain.RegistrationToken
	err := q.QueryRow(ctx, `SELECT token_hash,line_user_id,created_at,expires_at FROM registration_tokens t
 WHERE token_hash=$1 AND expires_at>$2 AND consumed_at IS NULL AND revoked_at IS NULL
 AND NOT EXISTS (SELECT 1 FROM users u WHERE u.line_user_id=t.line_user_id)
 AND COALESCE((SELECT event_type FROM line_webhook_events e WHERE e.line_user_id=t.line_user_id AND event_type IN ('follow','unfollow') ORDER BY event_timestamp DESC,(event_type='unfollow') DESC,processed_at ASC LIMIT 1),'follow') <> 'unfollow'`, hash, now).Scan(&token.TokenHash, &token.LINEUserID, &token.CreatedAt, &token.ExpiresAt)
	return token, err
}
func (s *Store) ValidateRegistration(ctx context.Context, hash string, now time.Time) (time.Time, error) {
	token, err := validRegistration(ctx, s.q, hash, now)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, usecase.ErrRegistrationUnavailable
	}
	return token.ExpiresAt, s.mapError(err)
}
func (s *Store) Register(ctx context.Context, hash string, user domain.User, session domain.Session, oldHash string, now time.Time) error {
	tx, err := s.b.Begin(ctx)
	if err != nil {
		return s.mapError(err)
	}
	defer rollback(tx)
	var line string
	if err = tx.QueryRow(ctx, `SELECT line_user_id FROM registration_tokens WHERE token_hash=$1`, hash).Scan(&line); errors.Is(err, pgx.ErrNoRows) {
		return usecase.ErrRegistrationUnavailable
	} else if err != nil {
		return s.mapError(err)
	}
	if err = lockLINE(ctx, tx, line); err != nil {
		return s.mapError(err)
	}
	token, err := validRegistration(ctx, tx, hash, now)
	if errors.Is(err, pgx.ErrNoRows) {
		return usecase.ErrRegistrationUnavailable
	}
	if err != nil {
		return s.mapError(err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO users(login_id,password_hash,line_user_id,line_following,created_at,updated_at) VALUES($1,$2,$3,true,$4,$4)`, user.LoginID, user.PasswordHash, token.LINEUserID, now)
	if err != nil {
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23505" {
			if pg.ConstraintName == "users_pkey" {
				return usecase.ErrLoginIDExists
			}
			return usecase.ErrRegistrationUnavailable
		}
		return s.mapError(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO notification_settings(login_id,thresholds,updated_at) VALUES($1,ARRAY[10,15,20,25,30]::smallint[],$2)`, user.LoginID, now); err != nil {
		return s.mapError(err)
	}
	if _, err = tx.Exec(ctx, `DELETE FROM sessions WHERE token_hash=$1`, oldHash); err != nil {
		return s.mapError(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO sessions(login_id,token_hash,csrf_token_hash,created_at,expires_at) VALUES($1,$2,$3,$4,$5)`, user.LoginID, session.TokenHash, session.CSRFTokenHash, session.CreatedAt, session.ExpiresAt); err != nil {
		return s.mapError(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE registration_tokens SET consumed_at=$2 WHERE token_hash=$1`, hash, now); err != nil {
		return s.mapError(err)
	}
	return s.mapError(tx.Commit(ctx))
}
func (s *Store) ProcessLINEEvent(ctx context.Context, event domain.LINEEvent, token domain.RegistrationToken, registerURL, loginURL string, now time.Time) (domain.LINEReply, error) {
	var reply domain.LINEReply
	tx, err := s.b.Begin(ctx)
	if err != nil {
		return reply, s.mapError(err)
	}
	defer rollback(tx)
	if err = lockLINE(ctx, tx, event.LINEUserID); err != nil {
		return reply, s.mapError(err)
	}
	// 友だち状態とURL要求は独立した時刻順で比較する。新しいメッセージ後の遅着followも状態だけ反映できる。
	var friendType string
	var friendTime int64
	friendErr := tx.QueryRow(ctx, `SELECT event_type,event_timestamp FROM line_webhook_events WHERE line_user_id=$1 AND event_type IN ('follow','unfollow') ORDER BY event_timestamp DESC,(event_type='unfollow') DESC,processed_at ASC LIMIT 1`, event.LINEUserID).Scan(&friendType, &friendTime)
	if friendErr != nil && !errors.Is(friendErr, pgx.ErrNoRows) {
		return reply, s.mapError(friendErr)
	}
	var latestTime int64
	latestErr := tx.QueryRow(ctx, `SELECT max(event_timestamp) FROM line_webhook_events WHERE line_user_id=$1 HAVING count(*)>0`, event.LINEUserID).Scan(&latestTime)
	if latestErr != nil && !errors.Is(latestErr, pgx.ErrNoRows) {
		return reply, s.mapError(latestErr)
	}
	tag, err := tx.Exec(ctx, `INSERT INTO line_webhook_events(webhook_event_id,line_user_id,event_type,event_timestamp,reply_status,processed_at) VALUES($1,$2,$3,$4,'not_required',$5) ON CONFLICT(webhook_event_id) DO NOTHING`, event.ID, event.LINEUserID, event.Type, event.Timestamp, now)
	if err != nil {
		return reply, s.mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return reply, nil
	}
	var registered bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE line_user_id=$1)`, event.LINEUserID).Scan(&registered); err != nil {
		return reply, s.mapError(err)
	}
	changesFriend := event.Type == "follow" || event.Type == "unfollow"
	newerFriend := errors.Is(friendErr, pgx.ErrNoRows) || event.Timestamp > friendTime || event.Timestamp == friendTime && event.Type == "unfollow" && friendType != "unfollow"
	if changesFriend && newerFriend {
		friendType = event.Type
		if registered {
			_, err = tx.Exec(ctx, `UPDATE users SET line_following=$2,updated_at=$3 WHERE line_user_id=$1`, event.LINEUserID, event.Type == "follow", now)
		} else if event.Type == "unfollow" {
			_, err = tx.Exec(ctx, `UPDATE registration_tokens SET revoked_at=$2 WHERE line_user_id=$1 AND consumed_at IS NULL AND revoked_at IS NULL`, event.LINEUserID, now)
		}
		if err != nil {
			return reply, s.mapError(err)
		}
	}
	newerRequest := errors.Is(latestErr, pgx.ErrNoRows) || event.Timestamp > latestTime
	if newerRequest && event.Type != "unfollow" {
		if registered {
			reply.Text = "ログインはこちら: " + loginURL
		} else if friendType != "unfollow" {
			limited := false
			if event.Type == "registration" {
				var count int
				if err = tx.QueryRow(ctx, `SELECT count(*) FROM line_webhook_events WHERE line_user_id=$1 AND event_type='registration' AND processed_at>$2 AND webhook_event_id<>$3`, event.LINEUserID, now.Add(-15*time.Minute), event.ID).Scan(&count); err != nil {
					return reply, s.mapError(err)
				}
				limited = count >= 5
			}
			if limited {
				reply.Text = "登録URLの再発行回数を超えました。15分ほど待ってから『登録』と送信してください。"
			} else {
				if _, err = tx.Exec(ctx, `UPDATE registration_tokens SET revoked_at=$2 WHERE line_user_id=$1 AND consumed_at IS NULL AND revoked_at IS NULL`, event.LINEUserID, now); err != nil {
					return reply, s.mapError(err)
				}
				if _, err = tx.Exec(ctx, `INSERT INTO registration_tokens(token_hash,line_user_id,created_at,expires_at) VALUES($1,$2,$3,$4)`, token.TokenHash, event.LINEUserID, token.CreatedAt, token.ExpiresAt); err != nil {
					return reply, s.mapError(err)
				}
				reply.Text = "24時間有効・1回限りの登録URLです。共有しないでください。\n" + registerURL + "\nURLが届かない／期限切れの場合は『登録』と送信してください。"
			}
		}
	}
	if reply.Text != "" {
		if _, err = tx.Exec(ctx, `UPDATE line_webhook_events SET reply_status='pending' WHERE webhook_event_id=$1`, event.ID); err != nil {
			return domain.LINEReply{}, s.mapError(err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.LINEReply{}, s.mapError(err)
	}
	return reply, nil
}
func (s *Store) SaveLINEReply(ctx context.Context, id, status string) error {
	_, err := s.q.Exec(ctx, `UPDATE line_webhook_events SET reply_status=$2 WHERE webhook_event_id=$1 AND reply_status='pending'`, id, status)
	return s.mapError(err)
}
func (s *Store) DeleteExpiredRegistrationTokens(ctx context.Context, now time.Time) error {
	_, err := s.q.Exec(ctx, `DELETE FROM registration_tokens WHERE expires_at<=$1 OR consumed_at IS NOT NULL OR revoked_at IS NOT NULL`, now)
	return s.mapError(err)
}
