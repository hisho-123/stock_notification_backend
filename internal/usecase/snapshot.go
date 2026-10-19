package usecase

import (
	"context"

	"github.com/hisho-123/stock_notification_backend/internal/domain"
)

type MonitoringSnapshot struct {
	Targets         []Target
	Settings        map[string]domain.NotificationSettings
	MissingSettings []string
}
type SnapshotRepository interface {
	MonitoringSnapshot(context.Context) (MonitoringSnapshot, error)
}
