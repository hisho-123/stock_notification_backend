package usecase

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/hisho-123/stock_notification_backend/internal/domain"
)

type Assets struct {
	Repository AssetRepository
	Prices     PriceProvider
	Clock      Clock
}

func (s Assets) Create(ctx context.Context, input string) (domain.Asset, error) {
	code, err := domain.NormalizeCode(input)
	if err != nil {
		return domain.Asset{}, Invalid("code", "銘柄コードを確認してください。")
	}
	exists, err := s.Repository.ExistsCode(ctx, code)
	if err != nil {
		return domain.Asset{}, err
	}
	if exists {
		return domain.Asset{}, ErrAlreadyExists
	}
	q, err := s.Prices.Fetch(ctx, code)
	if err != nil {
		return domain.Asset{}, err
	}
	if !q.Price.Valid() || q.FetchedAt.IsZero() {
		return domain.Asset{}, ErrPriceProvider
	}
	a := domain.Asset{Code: code, Name: domain.NormalizeName(q.Name, code), HighestValue: q.Price, LatestValue: q.Price, LatestFetchedAt: q.FetchedAt, LastCheckedAt: q.FetchedAt, MonitoringEnabled: !q.Stale(), CreatedAt: q.FetchedAt, UpdatedAt: q.FetchedAt}
	if q.Stale() {
		e := domain.PriceDataStale
		a.LastErrorCode = &e
	}
	return s.Repository.Create(ctx, a)
}

func (s Assets) List(ctx context.Context, options ListOptions) ([]domain.Asset, error) {
	options.Query = strings.TrimSpace(options.Query)
	if !utf8.ValidString(options.Query) || utf8.RuneCountInString(options.Query) > 200 {
		return nil, Invalid("q", "検索文字列は200文字以内で指定してください。")
	}
	if options.Sort == "" {
		options.Sort = "created_at"
	}
	if options.Order == "" {
		options.Order = "asc"
	}
	switch options.Sort {
	case "created_at", "code", "name", "highest_value", "latest_value", "decline_rate", "latest_fetched_at", "monitoring_enabled", "last_notified_threshold":
	default:
		return nil, Invalid("sort", "ソート項目を確認してください。")
	}
	if options.Order != "asc" && options.Order != "desc" {
		return nil, Invalid("order", "順序を確認してください。")
	}
	return s.Repository.List(ctx, options)
}
func (s Assets) SetMonitoring(ctx context.Context, id int64, enabled bool) (domain.Asset, error) {
	return s.Repository.SetMonitoring(ctx, id, enabled, s.Clock.Now())
}
func (s Assets) Delete(ctx context.Context, id int64) error { return s.Repository.Delete(ctx, id) }
