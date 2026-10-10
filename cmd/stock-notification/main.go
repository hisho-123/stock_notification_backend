package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hisho-123/stock_notification_backend/internal/adapter/security"
	"github.com/hisho-123/stock_notification_backend/internal/config"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:])
	stop()
	os.Exit(code)
}
func run(ctx context.Context, args []string) int {
	token, err := (security.Tokens{}).Generate()
	if err != nil {
		return 1
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("run_id", token)
	if len(args) != 1 || (args[0] != "serve" && args[0] != "monitor") {
		logger.Error("usage: stock-notification serve|monitor")
		return 1
	}
	c, err := config.Load(args[0])
	if err != nil {
		logger.Error("configuration_invalid", "error_code", "CONFIG_INVALID", "detail", err.Error())
		return 1
	}
	code := 0
	if args[0] == "serve" {
		code = serve(ctx, c, logger)
	} else {
		code = monitor(ctx, c, logger)
	}
	logger.Info("command_finished", "command", args[0], "exit_code", code, "finished_at", time.Now().UTC())
	return code
}
