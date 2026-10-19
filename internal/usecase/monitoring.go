package usecase

import (
	"context"
	"errors"
	"sort"

	"github.com/hisho-123/stock_notification_backend/internal/domain"
)

type Monitor struct {
	Assets        AssetRepository
	Snapshots     SnapshotRepository
	Users         UserRepository
	Registrations RegistrationRepository
	Settings      SettingsRepository
	Sessions      SessionRepository
	Transaction   Transaction
	Prices        PriceProvider
	Notifier      Notifier
	Clock         Clock
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
	if s.Registrations != nil {
		if err := s.Registrations.DeleteExpiredRegistrationTokens(ctx, s.Clock.Now()); err != nil {
			return result, err
		}
	}
	snapshot, err := s.Snapshots.MonitoringSnapshot(ctx)
	if err != nil {
		return result, err
	}
	result.Failed += len(snapshot.MissingSettings)
	for range snapshot.MissingSettings {
		if report != nil {
			report(Observation{Result: "failed", ErrorCode: "SETTINGS_MISSING"})
		}
	}
	targets := snapshot.Targets
	sort.Slice(targets, func(i, j int) bool { return targets[i].ID < targets[j].ID })
	for _, target := range targets {
		settings, ok := snapshot.Settings[target.LoginID]
		if !ok {
			result.Skipped++
			continue
		}
		thresholds := append([]int(nil), settings.Thresholds...)
		ownerCtx := WithLoginID(ctx, target.LoginID)
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
		err := s.Transaction.WithinTransaction(ownerCtx, func(repo AssetRepository) error {
			a, err := repo.Get(ownerCtx, target.ID)
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
				return repo.SaveObservation(ownerCtx, a)
			}
			a.LastCheckedAt = q.FetchedAt
			if q.Stale() {
				stale = true
				owner, err := s.Users.FindUser(ctx, target.LoginID)
				if err != nil {
					return err
				}
				if !owner.LINEFollowing {
					e := domain.PriceDataStale
					a.LastErrorCode = &e
					obs.Result, obs.ErrorCode = "stale", string(e)
				} else if err := s.Notifier.Send(ctx, owner.LINEUserID, domain.StaleMessage(a, q)); err != nil {
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
					owner, err := s.Users.FindUser(ctx, target.LoginID)
					if err != nil {
						return err
					}
					if !owner.LINEFollowing {
						// 送信抑止を通知成功とせず、通知済み率を維持する。
					} else if err := s.Notifier.Send(ctx, owner.LINEUserID, domain.DeclineMessage(a, threshold)); err != nil {
						e := domain.NotificationFailed
						a.LastErrorCode = &e
						obs.Result, obs.ErrorCode = "failed", string(e)
					} else {
						a.LastNotifiedThreshold = &threshold
					}
				}
			}
			a.UpdatedAt = s.Clock.Now()
			return repo.SaveObservation(ownerCtx, a)
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
