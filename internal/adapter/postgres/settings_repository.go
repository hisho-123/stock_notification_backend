package postgres

import (
	"context"
	"errors"

	"github.com/hisho-123/stock_notification_backend/internal/domain"
	"github.com/hisho-123/stock_notification_backend/internal/usecase"
)

func (s *Store) GetSettings(ctx context.Context) (domain.NotificationSettings, error) {
	var result domain.NotificationSettings
	err := s.q.QueryRow(ctx, `SELECT thresholds,updated_at FROM notification_settings WHERE id=1`).Scan(&result.Thresholds, &result.UpdatedAt)
	if errors.Is(s.mapError(err), usecase.ErrNotFound) {
		return result, usecase.ErrUnavailable
	}
	return result, s.mapError(err)
}
func (s *Store) SaveSettings(ctx context.Context, input domain.NotificationSettings) (domain.NotificationSettings, error) {
	var result domain.NotificationSettings
	err := s.q.QueryRow(ctx, `UPDATE notification_settings SET thresholds=$1,updated_at=$2 WHERE id=1 RETURNING thresholds,updated_at`, input.Thresholds, input.UpdatedAt).Scan(&result.Thresholds, &result.UpdatedAt)
	if errors.Is(s.mapError(err), usecase.ErrNotFound) {
		return result, usecase.ErrUnavailable
	}
	return result, s.mapError(err)
}
