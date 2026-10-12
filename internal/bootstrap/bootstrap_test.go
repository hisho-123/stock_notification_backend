package bootstrap

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hisho-123/stock_notification_backend/internal/config"
	"github.com/hisho-123/stock_notification_backend/internal/domain"
	"github.com/hisho-123/stock_notification_backend/internal/usecase"
)

type storeStub struct {
	usecase.AssetRepository
	usecase.SessionRepository
	usecase.Transaction
	cleanupErr, settingsErr error
	closed                  int
	asset                   domain.Asset
}

func (s *storeStub) Close()                                                 { s.closed++ }
func (s *storeStub) DeleteExpiredSessions(context.Context, time.Time) error { return s.cleanupErr }
func (s *storeStub) GetSettings(context.Context) (domain.NotificationSettings, error) {
	return domain.NotificationSettings{Thresholds: []int{10, 25, 30}}, s.settingsErr
}
func (s *storeStub) SaveSettings(context.Context, domain.NotificationSettings) (domain.NotificationSettings, error) {
	panic("unexpected settings write")
}
func (s *storeStub) Targets(context.Context) ([]usecase.Target, error) {
	return []usecase.Target{{ID: 1, Code: "AAPL"}}, nil
}
func (s *storeStub) Get(context.Context, int64) (domain.Asset, error)        { return s.asset, nil }
func (s *storeStub) SaveObservation(_ context.Context, a domain.Asset) error { s.asset = a; return nil }
func (s *storeStub) WithinTransaction(ctx context.Context, fn func(usecase.AssetRepository) error) error {
	return fn(s)
}

type serverStub struct {
	started, stop          chan struct{}
	listenErr, shutdownErr error
	shutdowns, closes      int
}

func (s *serverStub) ListenAndServe() error {
	close(s.started)
	if s.listenErr != nil {
		return s.listenErr
	}
	<-s.stop
	return http.ErrServerClosed
}
func (s *serverStub) Shutdown(context.Context) error {
	s.shutdowns++
	if s.shutdownErr == nil {
		close(s.stop)
	}
	return s.shutdownErr
}
func (s *serverStub) Close() error { s.closes++; close(s.stop); return nil }

type pricesStub struct{ q domain.Quote }

func (p pricesStub) Fetch(context.Context, string) (domain.Quote, error) { return p.q, nil }

type notifierStub struct{ messages []string }

func (n *notifierStub) Send(_ context.Context, message string) error {
	n.messages = append(n.messages, message)
	return nil
}

func TestServeStartupFailuresReleaseResources(t *testing.T) {
	for _, stage := range []string{"open", "cleanup", "settings"} {
		t.Run(stage, func(t *testing.T) {
			s := &storeStub{}
			var startup context.Context
			if stage == "cleanup" {
				s.cleanupErr = usecase.ErrUnavailable
			}
			if stage == "settings" {
				s.settingsErr = usecase.ErrUnavailable
			}
			open := func(ctx context.Context, url string) (store, error) {
				startup = ctx
				if url != "secret-db" {
					t.Error(url)
				}
				if stage == "open" {
					return nil, usecase.ErrUnavailable
				}
				return s, nil
			}
			err := serveWith(context.Background(), config.Config{DatabaseURL: "secret-db"}, slog.Default(), open, func(*http.Server) httpServer { t.Fatal("startup failure started HTTP server"); return nil })
			wantClosed := 1
			if stage == "open" {
				wantClosed = 0
			}
			if !errors.Is(err, usecase.ErrUnavailable) || s.closed != wantClosed || !errors.Is(startup.Err(), context.Canceled) {
				t.Fatal(err, s.closed, startup.Err())
			}
		})
	}
}

func TestServeLifecycleAndConfiguration(t *testing.T) {
	for _, stage := range []string{"listen failure", "already closed", "shutdown", "shutdown failure"} {
		t.Run(stage, func(t *testing.T) {
			s := &storeStub{}
			server := &serverStub{started: make(chan struct{}), stop: make(chan struct{})}
			if stage == "listen failure" {
				server.listenErr = errors.New("secret")
			}
			if stage == "already closed" {
				server.listenErr = http.ErrServerClosed
			}
			if stage == "shutdown failure" {
				server.shutdownErr = errors.New("shutdown")
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var startup context.Context
			open := func(c context.Context, _ string) (store, error) { startup = c; return s, nil }
			newServer := func(c *http.Server) httpServer {
				if c.Addr != "127.0.0.1:9000" || c.ReadHeaderTimeout != 10*time.Second || c.ReadTimeout != 90*time.Second || c.WriteTimeout != 95*time.Second || c.IdleTimeout != 60*time.Second || c.MaxHeaderBytes != 32<<10 {
					t.Error("incorrect HTTP server configuration", c)
				}
				w := httptest.NewRecorder()
				c.Handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/settings/notifications", nil))
				if w.Code != 401 {
					t.Error("API handler not installed", w.Code)
				}
				return server
			}
			done := make(chan error, 1)
			go func() {
				done <- serveWith(ctx, config.Config{HTTPAddr: "127.0.0.1:9000"}, slog.Default(), open, newServer)
			}()
			select {
			case <-server.started:
			case <-time.After(5 * time.Second):
				t.Fatal("HTTP server did not start")
			}
			if strings.HasPrefix(stage, "shutdown") {
				cancel()
			}
			var err error
			select {
			case err = <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("HTTP server did not stop")
			}
			want := error(nil)
			if stage == "listen failure" || stage == "shutdown failure" {
				want = usecase.ErrUnavailable
			}
			if !errors.Is(err, want) || s.closed != 1 || startup.Err() != context.Canceled {
				t.Fatal(err, s.closed, startup.Err())
			}
			wantShutdowns, wantCloses := 0, 0
			if strings.HasPrefix(stage, "shutdown") {
				wantShutdowns = 1
			}
			if stage == "shutdown failure" {
				wantCloses = 1
			}
			if server.shutdowns != wantShutdowns || server.closes != wantCloses {
				t.Fatal(server.shutdowns, server.closes)
			}
		})
	}
}

func TestMonitorWiringAndObservationLog(t *testing.T) {
	now := time.Now().UTC()
	high, _ := domain.ParsePrice("100")
	latest, _ := domain.ParsePrice("75")
	s := &storeStub{asset: domain.Asset{ID: 1, Code: "AAPL", Name: "Apple", HighestValue: high, LatestValue: high, MonitoringEnabled: true}}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	n := &notifierStub{}
	var startup context.Context
	got, err := monitorWith(context.Background(), config.Config{DatabaseURL: "secret-db"}, logger, func(ctx context.Context, url string) (store, error) {
		startup = ctx
		if url != "secret-db" {
			t.Error(url)
		}
		return s, nil
	}, pricesStub{domain.Quote{Price: latest, ProvidedAt: now, FetchedAt: now}}, n)
	if err != nil || got.Processed != 1 || !got.Success() || s.closed != 1 || startup.Err() != context.Canceled || len(n.messages) != 1 || s.asset.LastNotifiedThreshold == nil || *s.asset.LastNotifiedThreshold != 25 {
		t.Fatal(got, err, s.asset)
	}
	for _, field := range []string{`"msg":"asset_checked"`, `"asset_id":1`, `"result":"success"`, `"error_code":""`, `"finished_at":`} {
		if !strings.Contains(logs.String(), field) {
			t.Fatal(logs.String())
		}
	}
}

func TestPublicBootstrapConnectionFailures(t *testing.T) {
	c := config.Config{DatabaseURL: "postgres://%zz"}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	if err := Serve(context.Background(), c, logger); !errors.Is(err, usecase.ErrUnavailable) {
		t.Fatal(err)
	}
	if _, err := Monitor(context.Background(), c, logger); !errors.Is(err, usecase.ErrUnavailable) {
		t.Fatal(err)
	}
}
