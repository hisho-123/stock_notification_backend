package usecase

import (
	"context"
	"errors"
	"sort"

	"github.com/hisho-123/stock_notification_backend/internal/domain"
)

type Monitor struct {
	Assets      AssetRepository
	Settings    SettingsRepository
	Sessions    SessionRepository
	Transaction Transaction
	Prices      PriceProvider
	Notifier    Notifier
	Clock       Clock
}
type MonitorResult struct{ Processed, Skipped, Failed, Stale int }

func (r MonitorResult) Success() bool { return r.Failed == 0 && r.Stale == 0 }

type Observation struct {
	AssetID           int64
	Result, ErrorCode string
}

func (s Monitor) Run(ctx context.Context, report func(Observation)) (MonitorResult, error) {
	var result MonitorResult
	if err := s.Sessions.DeleteExpiredSessions(ctx, s.Clock.Now()); err != nil {
		return result, err
	}
	settings, err := s.Settings.GetSettings(ctx)
	if err != nil {
		return result, err
	}
	thresholds := append([]int(nil), settings.Thresholds...)
	targets, err := s.Assets.Targets(ctx)
	if err != nil {
		return result, err
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].ID < targets[j].ID })
	for _, target := range targets {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		q, fetchErr := s.Prices.Fetch(ctx, target.Code)
		checkedAt := s.Clock.Now()
		if fetchErr == nil && (!q.Price.Valid() || q.FetchedAt.IsZero()) {
			fetchErr = ErrPriceProvider
		}
		obs := Observation{AssetID: target.ID, Result: "success"}
		stale := false
		err := s.Transaction.WithinTransaction(ctx, func(repo AssetRepository) error {
			a, err := repo.Get(ctx, target.ID)
			if errors.Is(err, ErrNotFound) {
				obs.Result = "skipped"
				return nil
			}
			if err != nil {
				return err
			}
			if !a.MonitoringEnabled {
				obs.Result = "skipped"
				return nil
			}
			a.UpdatedAt = checkedAt
			if fetchErr != nil {
				a.LastCheckedAt = checkedAt
				e := domain.PriceFetchFailed
				a.LastErrorCode = &e
				obs.Result, obs.ErrorCode = "failed", string(e)
				a.UpdatedAt = s.Clock.Now()
				return repo.SaveObservation(ctx, a)
			}
			a.LastCheckedAt = q.FetchedAt
			if q.Stale() {
				stale = true
				if err := s.Notifier.Send(ctx, domain.StaleMessage(a, q)); err != nil {
					e := domain.NotificationFailed
					a.LastErrorCode = &e
					obs.Result, obs.ErrorCode = "failed", string(e)
				} else {
					a.MonitoringEnabled = false
					e := domain.PriceDataStale
					a.LastErrorCode = &e
					obs.Result, obs.ErrorCode = "stale", string(e)
				}
			} else {
				threshold := a.ApplyFreshPrice(q, thresholds)
				a.LastErrorCode = nil
				if threshold > 0 {
					if err := s.Notifier.Send(ctx, domain.DeclineMessage(a, threshold)); err != nil {
						e := domain.NotificationFailed
						a.LastErrorCode = &e
						obs.Result, obs.ErrorCode = "failed", string(e)
					} else {
						a.LastNotifiedThreshold = &threshold
					}
				}
			}
			a.UpdatedAt = s.Clock.Now()
			return repo.SaveObservation(ctx, a)
		})
		if stale {
			result.Stale++
		}
		if err != nil {
			result.Failed++
			obs.Result, obs.ErrorCode = "failed", "DB_ERROR"
		} else if obs.Result == "skipped" {
			result.Skipped++
		} else {
			result.Processed++
			if obs.Result == "failed" {
				result.Failed++
			}
		}
		if report != nil {
			report(obs)
		}
		if errors.Is(err, ErrConnectionLost) {
			return result, err
		}
		if err := ctx.Err(); err != nil {
			return result, err
		}
	}
	return result, nil
}
