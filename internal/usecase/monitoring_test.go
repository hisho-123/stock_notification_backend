package usecase_test

import (
	"context"
	"errors"
	"reflect"
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
	assets      map[int64]domain.Asset
	targets     []usecase.Target
	settings    domain.NotificationSettings
	saveErrors  map[int64]error
	settingsErr error
	cleaned     bool
}

func (m *memory) Get(_ context.Context, id int64) (domain.Asset, error) {
	a, ok := m.assets[id]
	if !ok {
		return a, usecase.ErrNotFound
	}
	return a, nil
}
func (m *memory) SaveObservation(_ context.Context, a domain.Asset) error {
	if err := m.saveErrors[a.ID]; err != nil {
		return err
	}
	m.assets[a.ID] = a
	return nil
}
func (m *memory) Targets(context.Context) ([]usecase.Target, error) {
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
	return nil
}
func (m *memory) WithinTransaction(ctx context.Context, fn func(usecase.AssetRepository) error) error {
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
		})
	}
}
func TestTargetRecheckAndThresholdSnapshot(t *testing.T) {
	now := time.Now().UTC()
	m, prices, n, monitor := setup(now)
	a := baseAsset(now)
	a.ID, a.Code = 2, "SECOND"
	m.assets[2] = a
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
	m, prices, n, _ := setup(now)
	prices.fetch = func(string) (domain.Quote, error) {
		return domain.Quote{Price: p("123.4500"), Name: " ", ProvidedAt: now.Add(-240 * time.Hour), FetchedAt: now}, nil
	}
	s := usecase.Assets{Repository: m, Prices: prices, Clock: &clock{now}}
	a, err := s.Create(context.Background(), " xyz ")
	if err != nil || a.Code != "XYZ" || a.Name != "XYZ" || a.MonitoringEnabled || *a.LastErrorCode != domain.PriceDataStale || a.LatestValue.String() != "123.45" || len(n.messages) != 0 {
		t.Fatal(a, err)
	}
	if _, err := s.Create(context.Background(), "xyz"); !errors.Is(err, usecase.ErrAlreadyExists) || len(prices.calls) != 1 {
		t.Fatal("duplicate fetched", err)
	}
}
