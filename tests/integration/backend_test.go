//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hisho-123/stock_notification_backend/internal/adapter/fmp"
	"github.com/hisho-123/stock_notification_backend/internal/adapter/httpapi"
	"github.com/hisho-123/stock_notification_backend/internal/adapter/line"
	"github.com/hisho-123/stock_notification_backend/internal/adapter/postgres"
	"github.com/hisho-123/stock_notification_backend/internal/adapter/security"
	"github.com/hisho-123/stock_notification_backend/internal/domain"
	"github.com/hisho-123/stock_notification_backend/internal/testfixture"
	"github.com/hisho-123/stock_notification_backend/internal/usecase"
	"github.com/jackc/pgx/v5"
)

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

var sequence atomic.Uint64

func database(t *testing.T) (*postgres.Store, *pgx.Conn, string) {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Fatal("TEST_DATABASE_URL is required; use a dedicated test database")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Fatal("TEST_DATABASE_URL must be a PostgreSQL URI")
	}
	admin, err := pgx.Connect(context.Background(), raw)
	if err != nil {
		t.Fatal("cannot connect to test database")
	}
	schema := fmt.Sprintf("stock_test_%d_%d", time.Now().UnixNano(), sequence.Add(1))
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(context.Background(), "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+quoted+" CASCADE")
		_ = admin.Close(context.Background())
	})
	if _, err := admin.Exec(context.Background(), "SET search_path TO "+quoted); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000001_initial.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(context.Background(), string(data)); err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	store, err := postgres.OpenPool(context.Background(), u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	return store, admin, u.String()
}

func price(t *testing.T, text string) domain.Price {
	t.Helper()
	p, err := domain.ParsePrice(text)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func asset(t *testing.T, code, value string) domain.Asset {
	now := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	p := price(t, value)
	return domain.Asset{Code: code, Name: "Name " + code, HighestValue: p, LatestValue: p, LatestFetchedAt: now, LastCheckedAt: now, MonitoringEnabled: true, CreatedAt: now, UpdatedAt: now}
}

func TestRepositoriesAndDatabaseConstraints(t *testing.T) {
	s, raw, _ := database(t)
	ctx := context.Background()
	first, err := s.Create(ctx, asset(t, "A_%", "9"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Create(ctx, asset(t, "B", "100"))
	if err != nil {
		t.Fatal(err)
	}
	third, err := s.Create(ctx, asset(t, "C", "9"))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.List(ctx, usecase.ListOptions{Sort: "latest_value", Order: "asc"})
	if err != nil || len(rows) != 3 || rows[0].ID != first.ID || rows[1].ID != third.ID || rows[2].Code != "B" {
		t.Fatal(rows, err)
	}
	for _, query := range []string{"%", "_", "a_%"} {
		rows, err := s.List(ctx, usecase.ListOptions{Query: query, Sort: "created_at", Order: "asc"})
		if err != nil || len(rows) != 1 || rows[0].ID != first.ID {
			t.Fatal("wildcard not escaped", query, rows, err)
		}
	}
	threshold := 10
	first.LastNotifiedThreshold = &threshold
	if err := s.SaveObservation(ctx, first); err != nil {
		t.Fatal(err)
	}
	for _, order := range []string{"asc", "desc"} {
		rows, err := s.List(ctx, usecase.ListOptions{Sort: "last_notified_threshold", Order: order})
		if err != nil || rows[0].ID != first.ID || rows[1].LastNotifiedThreshold != nil {
			t.Fatal("null ordering", rows, err)
		}
	}
	for _, expression := range []string{"'NaN'::numeric", "'Infinity'::numeric", "0", "-1"} {
		if _, err := raw.Exec(ctx, "UPDATE assets SET highest_value="+expression+" WHERE id=$1", first.ID); err == nil {
			t.Fatal("invalid numeric accepted", expression)
		}
	}
	for _, expression := range []string{"'{}'::smallint[]", "ARRAY[10,10]::smallint[]", "ARRAY[20,10]::smallint[]", "ARRAY[0]::smallint[]", "ARRAY[100]::smallint[]", "ARRAY[10,NULL]::smallint[]", "ARRAY[[10,20]]::smallint[]", "'[0:1]={10,20}'::smallint[]"} {
		if _, err := raw.Exec(ctx, "UPDATE notification_settings SET thresholds="+expression); err == nil {
			t.Fatal("invalid thresholds accepted", expression)
		}
	}
	settings := usecase.Settings{Repository: s, Clock: fixedClock{time.Now()}}
	if result, err := settings.Update(ctx, []int{30, 10, 20}); err != nil || result.Thresholds[0] != 10 {
		t.Fatal(result, err)
	}
	if got, err := s.Get(ctx, first.ID); err != nil || got.LastNotifiedThreshold == nil || *got.LastNotifiedThreshold != 10 {
		t.Fatal("settings reset notification state", got, err)
	}
	rollbackErr := errors.New("rollback test")
	err = s.WithinTransaction(ctx, func(repo usecase.AssetRepository) error {
		a, err := repo.Get(ctx, first.ID)
		if err != nil {
			return err
		}
		a.LatestValue = price(t, "8")
		if err := repo.SaveObservation(ctx, a); err != nil {
			return err
		}
		return rollbackErr
	})
	a, fetchErr := s.Get(ctx, first.ID)
	if !errors.Is(err, rollbackErr) || fetchErr != nil || a.LatestValue.String() != "9" {
		t.Fatal("rollback failed", a, err, fetchErr)
	}
	var wg sync.WaitGroup
	outcomes := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := s.Create(ctx, asset(t, "DUP", "1")); outcomes <- err }()
	}
	wg.Wait()
	close(outcomes)
	success, conflict := 0, 0
	for err := range outcomes {
		if err == nil {
			success++
		} else if errors.Is(err, usecase.ErrAlreadyExists) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal(success, conflict)
	}
	if _, err := raw.Exec(ctx, `DELETE FROM notification_settings`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSettings(ctx); !errors.Is(err, usecase.ErrUnavailable) {
		t.Fatal("missing settings silently defaulted", err)
	}
}

func TestDeclineSortingWithoutDivisionRounding(t *testing.T) {
	s, _, _ := database(t)
	ctx := context.Background()
	high := "1" + strings.Repeat("0", 40)
	a, err := s.Create(ctx, asset(t, "SLIGHTLY_LESS", high))
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Create(ctx, asset(t, "EXACTLY_TEN", high))
	if err != nil {
		t.Fatal(err)
	}
	a.LatestValue = price(t, "9"+strings.Repeat("0", 38)+"1")
	b.LatestValue = price(t, "9"+strings.Repeat("0", 39))
	for _, value := range []domain.Asset{a, b} {
		if err := s.SaveObservation(ctx, value); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.List(ctx, usecase.ListOptions{Sort: "decline_rate", Order: "desc"})
	if err != nil || len(rows) != 2 || rows[0].ID != b.ID {
		t.Fatal("declines were rounded before sorting", rows, err)
	}
}

type externalServices struct {
	now                   time.Time
	price                 string
	stale                 bool
	fmpStatus, lineStatus int
	fmpCalls, lineCalls   int
	prices                *fmp.Client
	line                  *line.Client
}

func mockServices(t *testing.T) *externalServices {
	t.Helper()
	data, err := os.ReadFile("testdata/quote.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil || len(fixture) != 1 {
		t.Fatal("invalid quote fixture", err)
	}
	e := &externalServices{now: time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC), price: "100", fmpStatus: 200, lineStatus: 200}
	quoteServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.fmpCalls++
		w.WriteHeader(e.fmpStatus)
		if e.fmpStatus != 200 {
			_, _ = w.Write([]byte("secret-provider-response"))
			return
		}
		provided := e.now
		if e.stale {
			provided = provided.Add(-240 * time.Hour)
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{{"symbol": r.URL.Query().Get("symbol"), "name": fixture[0].Name, "price": json.Number(e.price), "timestamp": provided.Unix()}})
	}))
	lineServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { e.lineCalls++; w.WriteHeader(e.lineStatus) }))
	t.Cleanup(quoteServer.Close)
	t.Cleanup(lineServer.Close)
	e.prices = fmp.New("secret-fmp-key", quoteServer.Client())
	e.prices.Endpoint = quoteServer.URL
	e.prices.Now = func() time.Time { return e.now }
	e.line = line.New("secret-line-token", "test-user", lineServer.Client())
	e.line.Endpoint = lineServer.URL
	return e
}

func TestAPIAndMonitoring(t *testing.T) {
	s, raw, _ := database(t)
	external := mockServices(t)
	clock := fixedClock{external.now}
	hash := testfixture.PasswordHash
	api := &httpapi.Server{PublicOrigin: "https://stock.example.com", Clock: clock,
		Assets: usecase.Assets{Repository: s, Prices: external.prices, Clock: clock}, Settings: usecase.Settings{Repository: s, Clock: clock},
		Auth: usecase.Auth{Repository: s, Passwords: security.Passwords{}, Tokens: security.Tokens{}, Clock: clock, AdminID: "owner", PasswordHash: hash},
	}
	handler := api.Handler()
	var cookies []*http.Cookie
	csrf := ""
	call := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "https://stock.example.com"+path, strings.NewReader(body))
		r.Header.Set("Origin", api.PublicOrigin)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-CSRF-Token", csrf)
		for _, cookie := range cookies {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := call("GET", "/api/v1/assets", ""); w.Code != 401 {
		t.Fatal(w.Code, w.Body.String())
	}
	w := call("POST", "/api/v1/auth/login", `{"admin_id":"owner","password":"correct"}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var session struct {
		CSRFToken string `json:"csrf_token"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &session)
	csrf = session.CSRFToken
	cookies = w.Result().Cookies()
	for _, path := range []string{"/api/v1/auth/session", "/api/v1/settings/notifications"} {
		if w := call("GET", path, ""); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w = call("POST", "/api/v1/assets", `{"code":" aapl "}`)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var response struct {
		ID                string
		Code              string
		MonitoringEnabled bool `json:"monitoring_enabled"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &response)
	if response.Code != "AAPL" || !response.MonitoringEnabled || w.Header().Get("Location") != "/api/v1/assets/"+response.ID {
		t.Fatal(w.Body.String())
	}
	if w := call("POST", "/api/v1/assets", `{"code":"aapl"}`); w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, enabled := range []bool{false, true} {
		w = call("PATCH", "/api/v1/assets/"+response.ID, fmt.Sprintf(`{"monitoring_enabled":%t}`, enabled))
		if w.Code != 200 || external.fmpCalls != 1 || external.lineCalls != 0 {
			t.Fatal("toggle fetched or notified", w.Code)
		}
	}
	w = call("PUT", "/api/v1/settings/notifications", `{"thresholds":[30,10,25]}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `[10,25,30]`) {
		t.Fatal(w.Code, w.Body.String())
	}
	external.stale = true
	w = call("POST", "/api/v1/assets", `{"code":"STALE"}`)
	if w.Code != 201 || !strings.Contains(w.Body.String(), `"monitoring_enabled":false`) || !strings.Contains(w.Body.String(), "PRICE_DATA_STALE") || external.lineCalls != 0 {
		t.Fatal(w.Code, w.Body.String())
	}
	external.stale, external.price = false, "73"
	monitor := usecase.Monitor{Assets: s, Settings: s, Sessions: s, Transaction: s, Prices: external.prices, Notifier: external.line, Clock: clock}
	result, err := monitor.Run(context.Background(), nil)
	if err != nil || !result.Success() || result.Processed != 1 || external.lineCalls != 1 {
		t.Fatal(result, err, external.lineCalls)
	}
	w = call("GET", "/api/v1/assets?sort=decline_rate&order=desc", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"last_notified_threshold":25`) || !strings.Contains(w.Body.String(), `"decline_rate":"27.00"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	// Stale notification failure keeps monitoring; next success stops it.
	external.stale, external.lineStatus = true, 500
	result, err = monitor.Run(context.Background(), nil)
	a, fetchErr := s.Get(context.Background(), 1)
	if err != nil || fetchErr != nil || result.Failed != 1 || !a.MonitoringEnabled || a.LatestValue.String() != "73" || *a.LastErrorCode != domain.NotificationFailed {
		t.Fatal(result, a, err, fetchErr)
	}
	external.lineStatus = 200
	result, err = monitor.Run(context.Background(), nil)
	a, fetchErr = s.Get(context.Background(), 1)
	if err != nil || fetchErr != nil || result.Success() || result.Stale != 1 || a.MonitoringEnabled || *a.LastErrorCode != domain.PriceDataStale {
		t.Fatal(result, a, err)
	}
	before := external.fmpCalls
	if result, err = monitor.Run(context.Background(), nil); err != nil || !result.Success() || external.fmpCalls != before {
		t.Fatal("stopped asset fetched", result, err)
	}
	if w = call("DELETE", "/api/v1/assets/"+response.ID, ""); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = call("DELETE", "/api/v1/assets/"+response.ID, ""); w.Code != 404 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = call("POST", "/api/v1/auth/logout", ""); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = call("GET", "/api/v1/assets", ""); w.Code != 401 {
		t.Fatal("logout did not revoke", w.Code)
	}
	var count int
	if err := raw.QueryRow(context.Background(), `SELECT count(*) FROM sessions`).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}

func TestMonitorConnectionLossAndMigrationRollback(t *testing.T) {
	_, raw, dsn := database(t)
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	applicationName := fmt.Sprintf("stock_monitor_test_%d_%d", time.Now().UnixNano(), sequence.Add(1))
	query := u.Query()
	query.Set("application_name", applicationName)
	u.RawQuery = query.Encode()
	dsn = u.String()
	s, err := postgres.OpenConnection(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, err := s.Create(context.Background(), asset(t, "AAPL", "100"))
	if err != nil {
		t.Fatal(err)
	}
	var pid int
	// A transactional repository runs on the same pinned connection.
	if err := s.WithinTransaction(context.Background(), func(repo usecase.AssetRepository) error {
		_, err := repo.Get(context.Background(), a.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := raw.QueryRow(context.Background(), `SELECT pid FROM pg_stat_activity WHERE application_name=$1`, applicationName).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(context.Background(), `SELECT pg_terminate_backend($1)`, pid); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(context.Background(), a.ID); !errors.Is(err, usecase.ErrConnectionLost) {
		t.Fatal("connection loss not reported", err)
	}
	if _, err := s.Get(context.Background(), a.ID); !errors.Is(err, usecase.ErrConnectionLost) {
		t.Fatal("connection silently reopened", err)
	}
	down, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000001_initial.down.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(context.Background(), string(down)); err != nil {
		t.Fatal(err)
	}
	up, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000001_initial.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(context.Background(), string(up)); err != nil {
		t.Fatal(err)
	}
}
