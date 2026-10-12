package usecase_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hisho-123/stock_notification_backend/internal/domain"
	"github.com/hisho-123/stock_notification_backend/internal/usecase"
)

type clock struct{ now time.Time }

func (c *clock) Now() time.Time { return c.now }

type provider struct {
	fetch func(string) (domain.Quote, error)
	calls []string
}

func (p *provider) Fetch(_ context.Context, code string) (domain.Quote, error) {
	p.calls = append(p.calls, code)
	return p.fetch(code)
}

type notifier struct {
	messages []string
	err      error
}

func (n *notifier) Send(_ context.Context, message string) error {
	n.messages = append(n.messages, message)
	return n.err
}

type memory struct {
	usecase.AssetRepository
	usecase.SessionRepository
	assets                                 map[int64]domain.Asset
	targets                                []usecase.Target
	settings                               domain.NotificationSettings
	saveErrors                             map[int64]error
	settingsErr                            error
	cleaned                                bool
	cleanupErr, targetsErr, transactionErr error
	getErrors                              map[int64]error
	saves                                  []int64
}

func (m *memory) Get(_ context.Context, id int64) (domain.Asset, error) {
	if err := m.getErrors[id]; err != nil {
		return domain.Asset{}, err
	}
	a, ok := m.assets[id]
	if !ok {
		return a, usecase.ErrNotFound
	}
	return a, nil
}
func (m *memory) SaveObservation(_ context.Context, a domain.Asset) error {
	m.saves = append(m.saves, a.ID)
	if err := m.saveErrors[a.ID]; err != nil {
		return err
	}
	m.assets[a.ID] = a
	return nil
}
func (m *memory) Targets(context.Context) ([]usecase.Target, error) {
	if m.targetsErr != nil {
		return nil, m.targetsErr
	}
	if m.targets != nil {
		return m.targets, nil
	}
	var result []usecase.Target
	for _, a := range m.assets {
		if a.MonitoringEnabled {
			result = append(result, usecase.Target{ID: a.ID, Code: a.Code})
		}
	}
	return result, nil
}
func (m *memory) GetSettings(context.Context) (domain.NotificationSettings, error) {
	return m.settings, m.settingsErr
}
func (m *memory) SaveSettings(_ context.Context, s domain.NotificationSettings) (domain.NotificationSettings, error) {
	m.settings = s
	return s, nil
}
func (m *memory) DeleteExpiredSessions(context.Context, time.Time) error {
	m.cleaned = true
	return m.cleanupErr
}
func (m *memory) WithinTransaction(ctx context.Context, fn func(usecase.AssetRepository) error) error {
	if m.transactionErr != nil {
		return m.transactionErr
	}
	return fn(m)
}
func (m *memory) ExistsCode(_ context.Context, code string) (bool, error) {
	for _, a := range m.assets {
		if a.Code == code {
			return true, nil
		}
	}
	return false, nil
}
func (m *memory) Create(_ context.Context, a domain.Asset) (domain.Asset, error) {
	a.ID = int64(len(m.assets) + 1)
	m.assets[a.ID] = a
	return a, nil
}
func p(s string) domain.Price {
	v, err := domain.ParsePrice(s)
	if err != nil {
		panic(err)
	}
	return v
}
func baseAsset(now time.Time) domain.Asset {
	return domain.Asset{ID: 1, Code: "AAPL", Name: "Apple", HighestValue: p("100"), LatestValue: p("100"), LatestFetchedAt: now.Add(-24 * time.Hour), LastCheckedAt: now.Add(-24 * time.Hour), MonitoringEnabled: true}
}
func setup(now time.Time) (*memory, *provider, *notifier, usecase.Monitor) {
	m := &memory{assets: map[int64]domain.Asset{1: baseAsset(now)}, settings: domain.NotificationSettings{Thresholds: []int{10, 15, 20, 25, 30}}, saveErrors: map[int64]error{}}
	prices := &provider{fetch: func(string) (domain.Quote, error) {
		return domain.Quote{Price: p("75"), ProvidedAt: now, FetchedAt: now}, nil
	}}
	n := &notifier{}
	return m, prices, n, usecase.Monitor{Assets: m, Settings: m, Sessions: m, Transaction: m, Prices: prices, Notifier: n, Clock: &clock{now}}
}
func TestMonitorStateTransitions(t *testing.T) {
	now := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, price                  string
		stale, notifyFail, fetchFail bool
		wantError                    domain.AssetError
		wantEnabled                  bool
		wantLast                     int
		wantCalls                    int
	}{
		{"decline", "73", false, false, false, "", true, 25, 1},
		{"just below", "90.0001", false, false, false, "", true, 0, 0},
		{"line failure", "75", false, true, false, domain.NotificationFailed, true, 0, 1},
		{"fetch failure", "75", false, false, true, domain.PriceFetchFailed, true, 0, 0},
		{"stale success", "75", true, false, false, domain.PriceDataStale, false, 0, 1},
		{"stale failure", "75", true, true, false, domain.NotificationFailed, true, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, prices, n, monitor := setup(now)
			before := m.assets[1]
			prices.fetch = func(string) (domain.Quote, error) {
				q := domain.Quote{Price: p(tc.price), ProvidedAt: now, FetchedAt: now}
				if tc.stale {
					q.ProvidedAt = now.Add(-240 * time.Hour)
				}
				if tc.fetchFail {
					return domain.Quote{}, usecase.ErrPriceProvider
				}
				return q, nil
			}
			if tc.notifyFail {
				n.err = errors.New("failure")
			}
			r, err := monitor.Run(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			a := m.assets[1]
			if a.MonitoringEnabled != tc.wantEnabled || len(n.messages) != tc.wantCalls {
				t.Fatalf("enabled=%v messages=%v", a.MonitoringEnabled, n.messages)
			}
			if tc.wantCalls > 0 {
				want := "株・指数の下落通知\n銘柄: Apple (AAPL)\n下落率: 25.00%\n到達閾値: 25%\n監視開始後の最高値: 100\n最終取得値: 75\n取得日時: 2026-10-10 09:00 JST"
				if tc.price == "73" {
					want = strings.ReplaceAll(strings.ReplaceAll(want, "25.00%", "27.00%"), "最終取得値: 75", "最終取得値: 73")
				}
				if tc.stale {
					want = "価格データの停滞通知\n銘柄: Apple (AAPL)\n価格データが10日以上前です。\nFMPデータ日時: 2026-09-30 09:00 JST\n検知日時: 2026-10-10 09:00 JST\n監視を自動停止します。データ提供状況を確認し、必要に応じて手動で再開してください。"
				}
				if n.messages[0] != want {
					t.Fatalf("wrong notification:\n%s", n.messages[0])
				}
			}
			if tc.wantError == "" && a.LastErrorCode != nil || tc.wantError != "" && (a.LastErrorCode == nil || *a.LastErrorCode != tc.wantError) {
				t.Fatalf("error: %v", a.LastErrorCode)
			}
			if tc.wantLast == 0 && a.LastNotifiedThreshold != nil || tc.wantLast != 0 && (a.LastNotifiedThreshold == nil || *a.LastNotifiedThreshold != tc.wantLast) {
				t.Fatal("wrong notified threshold")
			}
			if (tc.stale || tc.fetchFail) && (a.LatestValue != before.LatestValue || a.HighestValue != before.HighestValue || !a.LatestFetchedAt.Equal(before.LatestFetchedAt)) {
				t.Fatal("old price was replaced")
			}
			if !a.LastCheckedAt.Equal(now) {
				t.Fatal("check time not advanced")
			}
			if r.Success() == (tc.stale || tc.fetchFail || tc.notifyFail) {
				t.Fatalf("wrong result %+v", r)
			}
		})
	}
}
func TestFailedNotificationReevaluatedNextDay(t *testing.T) {
	for _, latest := range []string{"95", "75"} {
		t.Run(latest, func(t *testing.T) {
			now := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
			m, prices, n, monitor := setup(now)
			n.err = errors.New("line failure")
			_, _ = monitor.Run(context.Background(), nil)
			if m.assets[1].LatestValue.String() != "75" || len(n.messages) != 1 {
				t.Fatal("failed notification lost observation or retried")
			}
			n.err = nil
			monitor.Clock.(*clock).now = now.Add(24 * time.Hour)
			prices.fetch = func(string) (domain.Quote, error) {
				return domain.Quote{Price: p(latest), ProvidedAt: now.Add(24 * time.Hour), FetchedAt: now.Add(24 * time.Hour)}, nil
			}
			r, err := monitor.Run(context.Background(), nil)
			if err != nil || !r.Success() || m.assets[1].LastErrorCode != nil {
				t.Fatal(r, err)
			}
			want := 1
			if latest == "75" {
				want = 2
			}
			if len(n.messages) != want {
				t.Fatal("notification was carried forward or omitted")
			}
			a := m.assets[1]
			if !a.UpdatedAt.Equal(now.Add(24*time.Hour)) || latest == "95" && a.LastNotifiedThreshold != nil || latest == "75" && (a.LastNotifiedThreshold == nil || *a.LastNotifiedThreshold != 25) {
				t.Fatal("wrong next-day state", a)
			}
		})
	}
}
func TestTargetRecheckAndThresholdSnapshot(t *testing.T) {
	now := time.Now().UTC()
	m, prices, n, monitor := setup(now)
	a := baseAsset(now)
	a.ID, a.Code = 2, "SECOND"
	m.assets[2] = a
	m.targets = []usecase.Target{{ID: 2, Code: "SECOND"}, {ID: 1, Code: "AAPL"}}
	prices.fetch = func(code string) (domain.Quote, error) {
		m.settings.Thresholds[0] = 5
		if code == "AAPL" {
			a := m.assets[1]
			a.MonitoringEnabled = false
			m.assets[1] = a
		}
		return domain.Quote{Price: p("93"), ProvidedAt: now, FetchedAt: now}, nil
	}
	r, err := monitor.Run(context.Background(), nil)
	if err != nil || r.Skipped != 1 || len(n.messages) != 0 || !reflect.DeepEqual(prices.calls, []string{"AAPL", "SECOND"}) {
		t.Fatal(r, err, prices.calls, n.messages)
	}
}
func TestDBFailureContinuation(t *testing.T) {
	for _, lost := range []bool{false, true} {
		m, prices, _, monitor := setup(time.Now().UTC())
		a := m.assets[1]
		a.ID, a.Code = 2, "SECOND"
		m.assets[2] = a
		m.saveErrors[1] = usecase.ErrUnavailable
		if lost {
			m.saveErrors[1] = usecase.ErrConnectionLost
		}
		r, err := monitor.Run(context.Background(), nil)
		if r.Failed != 1 || errors.Is(err, usecase.ErrConnectionLost) != lost {
			t.Fatal(r, err)
		}
		want := 2
		if lost {
			want = 1
		}
		if len(prices.calls) != want {
			t.Fatal("wrong continuation", prices.calls)
		}
	}
}
func TestCreateStaleAndDuplicate(t *testing.T) {
	now := time.Now().UTC()
	m, prices, _, _ := setup(now)
	prices.fetch = func(string) (domain.Quote, error) {
		return domain.Quote{Price: p("123.4500"), Name: " ", ProvidedAt: now.Add(-240 * time.Hour), FetchedAt: now}, nil
	}
	s := usecase.Assets{Repository: m, Prices: prices, Clock: &clock{now}}
	a, err := s.Create(context.Background(), " xyz ")
	if err != nil || a.Code != "XYZ" || a.Name != "XYZ" || a.MonitoringEnabled || a.LastErrorCode == nil || *a.LastErrorCode != domain.PriceDataStale || a.LatestValue.String() != "123.45" {
		t.Fatal(a, err)
	}
	if _, err := s.Create(context.Background(), "xyz"); !errors.Is(err, usecase.ErrAlreadyExists) || len(prices.calls) != 1 {
		t.Fatal("duplicate fetched", err)
	}
}

func TestMonitorStartupFailures(t *testing.T) {
	for _, stage := range []string{"cleanup", "settings", "targets"} {
		t.Run(stage, func(t *testing.T) {
			m, prices, n, monitor := setup(time.Now().UTC())
			switch stage {
			case "cleanup":
				m.cleanupErr = usecase.ErrUnavailable
			case "settings":
				m.settingsErr = usecase.ErrUnavailable
			case "targets":
				m.targetsErr = usecase.ErrUnavailable
			}
			got, err := monitor.Run(context.Background(), func(usecase.Observation) { t.Error("startup failure reported an asset") })
			if !errors.Is(err, usecase.ErrUnavailable) || got != (usecase.MonitorResult{}) || len(prices.calls) != 0 || len(n.messages) != 0 || !m.cleaned {
				t.Fatal(got, err, prices.calls, n.messages)
			}
		})
	}
}

func TestMonitorRecheckFailuresAndInvalidQuotes(t *testing.T) {
	for _, name := range []string{"deleted", "get failure", "transaction failure", "invalid price", "missing fetch time"} {
		t.Run(name, func(t *testing.T) {
			now := time.Now().UTC()
			m, prices, n, monitor := setup(now)
			before := m.assets[1]
			q := domain.Quote{Price: p("75"), ProvidedAt: now, FetchedAt: now}
			switch name {
			case "deleted":
				m.targets = []usecase.Target{{ID: 1, Code: "AAPL"}}
				delete(m.assets, 1)
			case "get failure":
				m.getErrors = map[int64]error{1: usecase.ErrUnavailable}
			case "transaction failure":
				m.transactionErr = usecase.ErrUnavailable
			case "invalid price":
				q.Price = domain.Price{}
			case "missing fetch time":
				q.FetchedAt = time.Time{}
			}
			prices.fetch = func(string) (domain.Quote, error) { return q, nil }
			var reports []usecase.Observation
			got, err := monitor.Run(context.Background(), func(o usecase.Observation) { reports = append(reports, o) })
			want := usecase.Observation{AssetID: 1, Result: "failed", ErrorCode: "DB_ERROR"}
			if name == "deleted" {
				want.Result, want.ErrorCode = "skipped", ""
				if got.Skipped != 1 || got.Failed != 0 {
					t.Fatal(got)
				}
			} else if name == "invalid price" || name == "missing fetch time" {
				want.ErrorCode = string(domain.PriceFetchFailed)
				a := m.assets[1]
				if a.LatestValue != before.LatestValue || a.HighestValue != before.HighestValue || !a.LatestFetchedAt.Equal(before.LatestFetchedAt) || a.LastErrorCode == nil || *a.LastErrorCode != domain.PriceFetchFailed || got.Processed != 1 {
					t.Fatal(a, got)
				}
			}
			if err != nil || len(n.messages) != 0 || !reflect.DeepEqual(reports, []usecase.Observation{want}) {
				t.Fatal(got, err, n.messages, reports)
			}
			if name == "deleted" || name == "get failure" || name == "transaction failure" {
				if len(m.saves) != 0 {
					t.Fatal("recheck failure saved an observation")
				}
			}
		})
	}
}

func TestMonitorCancellationAndEmptyTargets(t *testing.T) {
	for _, stage := range []string{"before target", "after target", "empty"} {
		t.Run(stage, func(t *testing.T) {
			m, prices, _, monitor := setup(time.Now().UTC())
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if stage == "empty" {
				m.assets = map[int64]domain.Asset{}
			}
			if stage == "before target" {
				cancel()
			}
			var reports []usecase.Observation
			got, err := monitor.Run(ctx, func(o usecase.Observation) { reports = append(reports, o); cancel() })
			if stage == "empty" {
				if err != nil || !got.Success() || len(prices.calls) != 0 {
					t.Fatal(got, err)
				}
				return
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			want := 0
			if stage == "after target" {
				want = 1
			}
			if len(prices.calls) != want || len(reports) != want || got.Processed != want {
				t.Fatal(got, prices.calls, reports)
			}
		})
	}
}

func TestMonitorPreservesNotificationStateOnFailure(t *testing.T) {
	for _, stale := range []bool{false, true} {
		t.Run(map[bool]string{false: "fetch", true: "stale"}[stale], func(t *testing.T) {
			now := time.Now().UTC()
			m, prices, n, monitor := setup(now)
			last := 20
			a := m.assets[1]
			a.LastNotifiedThreshold = &last
			m.assets[1] = a
			prices.fetch = func(string) (domain.Quote, error) {
				if !stale {
					return domain.Quote{}, usecase.ErrPriceProvider
				}
				return domain.Quote{Price: p("50"), ProvidedAt: now.Add(-240 * time.Hour), FetchedAt: now}, nil
			}
			n.err = errors.New("failed notification")
			_, err := monitor.Run(context.Background(), nil)
			got := m.assets[1]
			if err != nil || got.LastNotifiedThreshold == nil || *got.LastNotifiedThreshold != 20 || got.LatestValue != a.LatestValue || got.HighestValue != a.HighestValue || !got.LatestFetchedAt.Equal(a.LatestFetchedAt) {
				t.Fatal(got, err)
			}
		})
	}
}

func TestMonitorDeterministicTargetOrder(t *testing.T) {
	now := time.Now().UTC()
	m, prices, n, monitor := setup(now)
	for _, id := range []int64{2, 3} {
		a := baseAsset(now)
		a.ID = id
		a.Code = map[int64]string{2: "SECOND", 3: "THIRD"}[id]
		m.assets[id] = a
	}
	m.targets = []usecase.Target{{ID: 3, Code: "THIRD"}, {ID: 1, Code: "AAPL"}, {ID: 2, Code: "SECOND"}}
	prices.fetch = func(string) (domain.Quote, error) {
		return domain.Quote{Price: p("100"), ProvidedAt: now, FetchedAt: now}, nil
	}
	var ids []int64
	got, err := monitor.Run(context.Background(), func(o usecase.Observation) {
		ids = append(ids, o.AssetID)
		if o.Result != "success" || o.ErrorCode != "" {
			t.Error(o)
		}
	})
	if err != nil || got.Processed != 3 || !reflect.DeepEqual(ids, []int64{1, 2, 3}) || !reflect.DeepEqual(prices.calls, []string{"AAPL", "SECOND", "THIRD"}) || len(n.messages) != 0 {
		t.Fatal(got, err, ids, prices.calls)
	}
}
