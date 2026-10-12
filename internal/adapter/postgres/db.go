package postgres

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/hisho-123/stock_notification_backend/internal/usecase"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type queryer interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}
type beginner interface {
	Begin(context.Context) (pgx.Tx, error)
}

type Store struct {
	q        queryer
	b        beginner
	isClosed func() bool
	close    func()
}

func OpenPool(ctx context.Context, url string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, usecase.ErrUnavailable
	}
	cfg.ConnConfig.RuntimeParams["timezone"] = "UTC"
	cfg.ConnConfig.ConnectTimeout = 10 * time.Second
	cfg.MaxConns = 4
	// NewWithConfig only rejects MaxConns < 1; it is fixed at four above.
	// Connectivity is checked separately by Ping.
	pool, _ := pgxpool.NewWithConfig(ctx, cfg)
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, usecase.ErrUnavailable
	}
	return &Store{q: pool, b: pool, close: pool.Close}, nil
}

// OpenConnection pins the monitor to one connection, preventing silent reconnects.
func OpenConnection(ctx context.Context, url string) (*Store, error) {
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		return nil, usecase.ErrUnavailable
	}
	cfg.RuntimeParams["timezone"] = "UTC"
	cfg.ConnectTimeout = 10 * time.Second
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, usecase.ErrConnectionLost
	}
	return &Store{q: conn, b: conn, isClosed: conn.IsClosed, close: func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = conn.Close(closeCtx)
	}}, nil
}
func (s *Store) Close() {
	if s.close != nil {
		s.close()
	}
}

func (s *Store) mapError(err error) error {
	if err == nil {
		return nil
	}
	if s.isClosed != nil && s.isClosed() {
		return usecase.ErrConnectionLost
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return usecase.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		if pgErr.Code == "23505" {
			return usecase.ErrAlreadyExists
		}
		if pgErr.Code == "57P01" || pgErr.Code == "57P02" || pgErr.Code == "57P03" || len(pgErr.Code) >= 2 && pgErr.Code[:2] == "08" {
			return usecase.ErrConnectionLost
		}
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return usecase.ErrConnectionLost
	}
	return usecase.ErrUnavailable
}

var _ usecase.AssetRepository = (*Store)(nil)
var _ usecase.SettingsRepository = (*Store)(nil)
var _ usecase.SessionRepository = (*Store)(nil)
var _ usecase.Transaction = (*Store)(nil)
