package postgres

import (
	"context"
	"time"

	"github.com/hisho-123/stock_notification_backend/internal/domain"
	"github.com/hisho-123/stock_notification_backend/internal/usecase"
)

func (s *Store) MonitoringSnapshot(ctx context.Context) (usecase.MonitoringSnapshot, error) {
	result := usecase.MonitoringSnapshot{Settings: map[string]domain.NotificationSettings{}}
	tx, err := s.b.Begin(ctx)
	if err != nil {
		return result, s.mapError(err)
	}
	defer rollback(tx)
	// 設定変更と対象追加が混ざらないよう、開始時の短い読み取りだけを同一スナップショットにする。
	if _, err = tx.Exec(ctx, `SET TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY`); err != nil {
		return result, s.mapError(err)
	}
	rows, err := tx.Query(ctx, `SELECT u.login_id,n.thresholds,n.updated_at FROM users u LEFT JOIN notification_settings n USING(login_id) ORDER BY u.login_id`)
	if err != nil {
		return result, s.mapError(err)
	}
	for rows.Next() {
		var id string
		var thresholds []int
		var settings domain.NotificationSettings
		var updated *time.Time
		if err = rows.Scan(&id, &thresholds, &updated); err != nil {
			rows.Close()
			return result, s.mapError(err)
		}
		if thresholds == nil {
			result.MissingSettings = append(result.MissingSettings, id)
		} else {
			settings.Thresholds = thresholds
			settings.UpdatedAt = *updated
			result.Settings[id] = settings
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, s.mapError(err)
	}
	result.Targets, err = (&Store{q: tx, isClosed: s.isClosed}).Targets(ctx)
	if err != nil {
		return result, err
	}
	return result, s.mapError(tx.Commit(ctx))
}
