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

type assetRepo struct {
	usecase.AssetRepository
	exists                        bool
	existsErr, createErr, listErr error
	created                       domain.Asset
	options                       usecase.ListOptions
	listed                        int
	id                            int64
	enabled                       bool
	updated                       time.Time
}

func (r *assetRepo) ExistsCode(context.Context, string) (bool, error) { return r.exists, r.existsErr }
func (r *assetRepo) Create(_ context.Context, a domain.Asset) (domain.Asset, error) {
	r.created = a
	return a, r.createErr
}
func (r *assetRepo) List(_ context.Context, o usecase.ListOptions) ([]domain.Asset, error) {
	r.listed++
	r.options = o
	return []domain.Asset{r.created}, r.listErr
}
func (r *assetRepo) SetMonitoring(_ context.Context, id int64, enabled bool, now time.Time) (domain.Asset, error) {
	r.id, r.enabled, r.updated = id, enabled, now
	return r.created, r.createErr
}
func (r *assetRepo) Delete(_ context.Context, id int64) error { r.id = id; return r.createErr }

func TestCreateValidationAndDependencyFailures(t *testing.T) {
	now := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	for _, name := range []string{"invalid code", "exists failure", "duplicate", "fetch failure", "invalid price", "missing fetch time", "save failure", "fresh"} {
		t.Run(name, func(t *testing.T) {
			r := &assetRepo{}
			q := domain.Quote{Price: p("123.45"), Name: " Name ", ProvidedAt: now, FetchedAt: now}
			fetchErr := error(nil)
			input := " aapl "
			want := error(nil)
			switch name {
			case "invalid code":
				input = " "
				want = usecase.Invalid("code", "")
			case "exists failure":
				r.existsErr = usecase.ErrUnavailable
				want = r.existsErr
			case "duplicate":
				r.exists = true
				want = usecase.ErrAlreadyExists
			case "fetch failure":
				fetchErr = usecase.ErrPriceTimeout
				want = fetchErr
			case "invalid price":
				q.Price = domain.Price{}
				want = usecase.ErrPriceProvider
			case "missing fetch time":
				q.FetchedAt = time.Time{}
				want = usecase.ErrPriceProvider
			case "save failure":
				r.createErr = usecase.ErrUnavailable
				want = r.createErr
			}
			prices := &provider{fetch: func(string) (domain.Quote, error) { return q, fetchErr }}
			s := usecase.Assets{Repository: r, Prices: prices, Clock: &clock{now}}
			got, err := s.Create(context.Background(), input)
			if name == "invalid code" {
				var validation *usecase.ValidationError
				if !errors.As(err, &validation) || validation.Fields["code"] == "" {
					t.Fatal(err)
				}
			} else if !errors.Is(err, want) {
				t.Fatal(err, want)
			}
			if name == "fresh" && (got.Code != "AAPL" || got.Name != "Name" || got.HighestValue != q.Price || got.LatestValue != q.Price || !got.MonitoringEnabled || got.LastErrorCode != nil || !got.CreatedAt.Equal(now) || !got.UpdatedAt.Equal(now)) {
				t.Fatal(got)
			}
			if name == "invalid code" || name == "exists failure" || name == "duplicate" {
				if len(prices.calls) != 0 {
					t.Fatal("unexpected provider call")
				}
			} else if !reflect.DeepEqual(prices.calls, []string{"AAPL"}) {
				t.Fatal(prices.calls)
			}
		})
	}
}

func TestAssetListValidationAndForwarding(t *testing.T) {
	for _, tc := range []struct {
		name        string
		input, want usecase.ListOptions
		field       string
	}{
		{"defaults", usecase.ListOptions{Query: " aapl "}, usecase.ListOptions{Query: "aapl", Sort: "created_at", Order: "asc"}, ""},
		{"bad UTF8", usecase.ListOptions{Query: "\xff"}, usecase.ListOptions{}, "q"},
		{"long query", usecase.ListOptions{Query: strings.Repeat("界", 201)}, usecase.ListOptions{}, "q"},
		{"bad sort", usecase.ListOptions{Sort: "sql"}, usecase.ListOptions{}, "sort"},
		{"bad order", usecase.ListOptions{Order: "ASC"}, usecase.ListOptions{}, "order"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &assetRepo{}
			s := usecase.Assets{Repository: r}
			_, err := s.List(context.Background(), tc.input)
			if tc.field != "" {
				var validation *usecase.ValidationError
				if !errors.As(err, &validation) || validation.Fields[tc.field] == "" || r.listed != 0 {
					t.Fatal(err, r.listed)
				}
			} else if err != nil || r.options != tc.want {
				t.Fatal(err, r.options)
			}
		})
	}
	for _, sort := range []string{"created_at", "code", "name", "highest_value", "latest_value", "decline_rate", "latest_fetched_at", "monitoring_enabled", "last_notified_threshold"} {
		for _, order := range []string{"asc", "desc"} {
			t.Run(sort+"/"+order, func(t *testing.T) {
				r := &assetRepo{listErr: usecase.ErrUnavailable}
				s := usecase.Assets{Repository: r}
				o := usecase.ListOptions{Sort: sort, Order: order}
				if _, err := s.List(context.Background(), o); !errors.Is(err, r.listErr) || r.options != o {
					t.Fatal(err, r.options)
				}
			})
		}
	}
}

func TestAssetMutationForwarding(t *testing.T) {
	now := time.Now().UTC()
	r := &assetRepo{}
	s := usecase.Assets{Repository: r, Clock: &clock{now}}
	for _, failure := range []error{nil, usecase.ErrNotFound} {
		r.createErr = failure
		for _, enabled := range []bool{false, true} {
			if _, err := s.SetMonitoring(context.Background(), 42, enabled); !errors.Is(err, failure) || r.id != 42 || r.enabled != enabled || !r.updated.Equal(now) {
				t.Fatal(err, r)
			}
		}
		if err := s.Delete(context.Background(), 43); !errors.Is(err, failure) || r.id != 43 {
			t.Fatal(err, r.id)
		}
	}
}

func TestSettingsValidationAndPersistence(t *testing.T) {
	now := time.Now().UTC()
	m := &memory{settings: domain.NotificationSettings{Thresholds: []int{10}}, settingsErr: usecase.ErrUnavailable}
	s := usecase.Settings{Repository: m, Clock: &clock{now}}
	if _, err := s.Get(context.Background()); !errors.Is(err, m.settingsErr) {
		t.Fatal(err)
	}
	for _, input := range [][]int{{}, {10, 10}, {0}, {100}} {
		_, err := s.Update(context.Background(), input)
		var validation *usecase.ValidationError
		if !errors.As(err, &validation) || validation.Fields["thresholds"] == "" || !reflect.DeepEqual(m.settings.Thresholds, []int{10}) {
			t.Fatal(err, m.settings)
		}
	}
	got, err := s.Update(context.Background(), []int{30, 10, 20})
	if err != nil || !reflect.DeepEqual(got.Thresholds, []int{10, 20, 30}) || !got.UpdatedAt.Equal(now) {
		t.Fatal(got, err)
	}
	if got := (&usecase.ValidationError{}).Error(); got != "validation error" {
		t.Fatal(got)
	}
}
