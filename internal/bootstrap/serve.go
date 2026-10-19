package bootstrap

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/hisho-123/stock_notification_backend/internal/adapter/fmp"
	"github.com/hisho-123/stock_notification_backend/internal/adapter/httpapi"
	"github.com/hisho-123/stock_notification_backend/internal/adapter/line"
	"github.com/hisho-123/stock_notification_backend/internal/adapter/security"
	"github.com/hisho-123/stock_notification_backend/internal/config"
	"github.com/hisho-123/stock_notification_backend/internal/usecase"
)

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now().UTC() }

func Serve(ctx context.Context, c config.Config, logger *slog.Logger) error {
	return serveWith(ctx, c, logger, openPool, newHTTPServer)
}

func serveWith(ctx context.Context, c config.Config, logger *slog.Logger, open openStore, newServer func(*http.Server) httpServer) error {
	startup, cancel := context.WithTimeout(ctx, 15*time.Second)
	store, err := open(startup, c.DatabaseURL)
	if err != nil {
		cancel()
		return err
	}
	defer store.Close()
	if err := store.DeleteExpiredSessions(startup, time.Now()); err != nil {
		cancel()
		return err
	}
	if err := store.DeleteExpiredRegistrationTokens(startup, time.Now()); err != nil {
		cancel()
		return err
	}
	cancel()
	clock := wallClock{}
	api := &httpapi.Server{
		Assets:   usecase.Assets{Repository: store, Prices: fmp.New(c.FMPAPIKey, nil), Clock: clock},
		Settings: usecase.Settings{Repository: store, Clock: clock},
		Auth:     usecase.Auth{Repository: store, Passwords: security.Passwords{}, Tokens: security.Tokens{}, Clock: clock, Users: store},
		Clock:    clock, PublicOrigin: c.PublicOrigin, StaticDir: c.StaticDir, TrustedProxies: c.TrustedProxies,
	}
	api.Registration = usecase.Registration{Repository: store, Passwords: security.Passwords{}, Auth: api.Auth}
	api.LINEChannelSecret = c.LINEChannelSecret
	api.Webhook = usecase.LINEWebhook{Repository: store, Tokens: security.Tokens{}, Clock: clock, Replier: line.New(c.LINEToken, nil), PublicOrigin: c.PublicOrigin, Report: func(id, status string) { logger.Info("line_reply", "webhook_event_id", id, "reply_status", status) }}
	server := newServer(&http.Server{Addr: c.HTTPAddr, Handler: api.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 90 * time.Second, WriteTimeout: 95 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10})
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	logger.Info("web_started", "result", "listening")
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return usecase.ErrUnavailable
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 95*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			_ = server.Close()
			return usecase.ErrUnavailable
		}
		return nil
	}
}
