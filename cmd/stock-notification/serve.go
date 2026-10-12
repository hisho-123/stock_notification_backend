package main

import (
	"context"
	"log/slog"

	"github.com/hisho-123/stock_notification_backend/internal/bootstrap"
	"github.com/hisho-123/stock_notification_backend/internal/config"
)

func serve(ctx context.Context, c config.Config, logger *slog.Logger) int {
	return serveWith(ctx, c, logger, bootstrap.Serve)
}

func serveWith(ctx context.Context, c config.Config, logger *slog.Logger, start func(context.Context, config.Config, *slog.Logger) error) int {
	if err := start(ctx, c, logger); err != nil {
		logger.Error("web_failed", "error_code", "WEB_FAILED")
		return 1
	}
	return 0
}
