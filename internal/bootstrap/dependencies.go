package bootstrap

import (
	"context"
	"net/http"

	"github.com/hisho-123/stock_notification_backend/internal/adapter/postgres"
	"github.com/hisho-123/stock_notification_backend/internal/usecase"
)

type store interface {
	usecase.AssetRepository
	usecase.SettingsRepository
	usecase.SessionRepository
	usecase.Transaction
	usecase.UserRepository
	usecase.RegistrationRepository
	usecase.SnapshotRepository
	Close()
}

type openStore func(context.Context, string) (store, error)

func openPool(ctx context.Context, url string) (store, error) {
	return postgres.OpenPool(ctx, url)
}

func openConnection(ctx context.Context, url string) (store, error) {
	return postgres.OpenConnection(ctx, url)
}

type httpServer interface {
	ListenAndServe() error
	Shutdown(context.Context) error
	Close() error
}

func newHTTPServer(server *http.Server) httpServer { return server }
