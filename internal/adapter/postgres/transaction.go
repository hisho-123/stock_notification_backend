package postgres

import (
	"context"
	"time"

	"github.com/hisho-123/stock_notification_backend/internal/usecase"
	"github.com/jackc/pgx/v5"
)

func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}
func (s *Store) WithinTransaction(ctx context.Context, fn func(usecase.AssetRepository) error) error {
	tx, err := s.b.Begin(ctx)
	if err != nil {
		return s.mapError(err)
	}
	defer rollback(tx)
	if err := fn(&Store{q: tx, isClosed: s.isClosed}); err != nil {
		return err
	}
	return s.mapError(tx.Commit(ctx))
}
