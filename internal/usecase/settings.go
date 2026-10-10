package usecase

import (
	"context"

	"github.com/hisho-123/stock_notification_backend/internal/domain"
)

type Settings struct {
	Repository SettingsRepository
	Clock      Clock
}

func (s Settings) Get(ctx context.Context) (domain.NotificationSettings, error) {
	return s.Repository.GetSettings(ctx)
}
func (s Settings) Update(ctx context.Context, input []int) (domain.NotificationSettings, error) {
	values, err := domain.NormalizeThresholds(input)
	if err != nil {
		return domain.NotificationSettings{}, Invalid("thresholds", "1〜99の整数を重複なく1件以上指定してください。")
	}
	return s.Repository.SaveSettings(ctx, domain.NotificationSettings{Thresholds: values, UpdatedAt: s.Clock.Now()})
}
