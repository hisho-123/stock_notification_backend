package bootstrap

import (
	"context"
	"log/slog"
	"time"

	"github.com/hisho-123/stock_notification_backend/internal/adapter/fmp"
	"github.com/hisho-123/stock_notification_backend/internal/adapter/line"
	"github.com/hisho-123/stock_notification_backend/internal/adapter/postgres"
	"github.com/hisho-123/stock_notification_backend/internal/config"
	"github.com/hisho-123/stock_notification_backend/internal/usecase"
)

func Monitor(ctx context.Context, c config.Config, logger *slog.Logger) (usecase.MonitorResult, error) {
	startup, cancel := context.WithTimeout(ctx, 15*time.Second)
	store, err := postgres.OpenConnection(startup, c.DatabaseURL)
	cancel()
	if err != nil {
		return usecase.MonitorResult{}, err
	}
	defer store.Close()
	monitor := usecase.Monitor{Assets: store, Settings: store, Sessions: store, Transaction: store, Prices: fmp.New(c.FMPAPIKey, nil), Notifier: line.New(c.LINEToken, c.LINEUserID, nil), Clock: wallClock{}}
	return monitor.Run(ctx, func(o usecase.Observation) {
		logger.Info("asset_checked", "asset_id", o.AssetID, "result", o.Result, "error_code", o.ErrorCode, "finished_at", time.Now().UTC())
	})
}
