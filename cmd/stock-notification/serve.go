package main

import (
	"context"
	"log/slog"

	"github.com/hisho-123/stock_notification_backend/internal/bootstrap"
	"github.com/hisho-123/stock_notification_backend/internal/config"
)

func serve(ctx context.Context, c config.Config, logger *slog.Logger) int {
	if err := bootstrap.Serve(ctx, c, logger); err != nil {
		logger.Error("web_failed", "error_code", "WEB_FAILED")
		return 1
	}
	return 0
}
