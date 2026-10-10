package main

import (
	"context"
	"errors"
	"log/slog"

	"github.com/hisho-123/stock_notification_backend/internal/bootstrap"
	"github.com/hisho-123/stock_notification_backend/internal/config"
	"github.com/hisho-123/stock_notification_backend/internal/usecase"
)

func monitor(ctx context.Context, c config.Config, logger *slog.Logger) int {
	result, err := bootstrap.Monitor(ctx, c, logger)
	logger.Info("monitor_finished", "processed", result.Processed, "skipped", result.Skipped, "failed", result.Failed, "stale", result.Stale)
	if err != nil {
		code := "MONITOR_FAILED"
		if errors.Is(err, usecase.ErrConnectionLost) {
			code = "DB_CONNECTION_LOST"
		}
		if errors.Is(err, usecase.ErrUnavailable) {
			code = "SERVICE_UNAVAILABLE"
		}
		logger.Error("monitor_failed", "error_code", code)
	}
	if err != nil || !result.Success() {
		return 1
	}
	return 0
}
