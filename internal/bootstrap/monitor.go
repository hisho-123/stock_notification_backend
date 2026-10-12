package bootstrap

import (
	"context"
	"log/slog"
	"time"

	"github.com/hisho-123/stock_notification_backend/internal/adapter/fmp"
	"github.com/hisho-123/stock_notification_backend/internal/adapter/line"
	"github.com/hisho-123/stock_notification_backend/internal/config"
	"github.com/hisho-123/stock_notification_backend/internal/usecase"
)

func Monitor(ctx context.Context, c config.Config, logger *slog.Logger) (usecase.MonitorResult, error) {
	return monitorWith(ctx, c, logger, openConnection, fmp.New(c.FMPAPIKey, nil), line.New(c.LINEToken, c.LINEUserID, nil))
}

func monitorWith(ctx context.Context, c config.Config, logger *slog.Logger, open openStore, prices usecase.PriceProvider, notifier usecase.Notifier) (usecase.MonitorResult, error) {
	startup, cancel := context.WithTimeout(ctx, 15*time.Second)
	store, err := open(startup, c.DatabaseURL)
	cancel()
	if err != nil {
		return usecase.MonitorResult{}, err
	}
	defer store.Close()
	monitor := usecase.Monitor{Assets: store, Settings: store, Sessions: store, Transaction: store, Prices: prices, Notifier: notifier, Clock: wallClock{}}
	return monitor.Run(ctx, func(o usecase.Observation) {
		logger.Info("asset_checked", "asset_id", o.AssetID, "result", o.Result, "error_code", o.ErrorCode, "finished_at", time.Now().UTC())
	})
}
