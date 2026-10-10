package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/hisho-123/stock_notification_backend/internal/adapter/security"
	"github.com/hisho-123/stock_notification_backend/internal/domain"
	"github.com/hisho-123/stock_notification_backend/internal/usecase"
)

type testClock struct{ now time.Time }

func (c *testClock) Now() time.Time { return c.now }

type testPasswords struct{}

func (testPasswords) Matches(_, password string) bool { return password == "correct" }

type testSessions struct {
	usecase.SessionRepository
	rows map[string]domain.Session
}

func (s *testSessions) FindSession(_ context.Context, hash string) (domain.Session, error) {
	row, ok := s.rows[hash]
	if !ok {
		return row, usecase.ErrNotFound
	}
	return row, nil
}
func (s *testSessions) ReplaceSession(_ context.Context, old string, row domain.Session) error {
	delete(s.rows, old)
	s.rows[row.TokenHash] = row
	return nil
}
func (s *testSessions) DeleteSession(_ context.Context, hash string) error {
	delete(s.rows, hash)
	return nil
}

type testSettings struct {
	values  []int
	updates int
}

func (s *testSettings) GetSettings(context.Context) (domain.NotificationSettings, error) {
	return domain.NotificationSettings{Thresholds: s.values}, nil
}
func (s *testSettings) SaveSettings(_ context.Context, settings domain.NotificationSettings) (domain.NotificationSettings, error) {
	s.updates++
	s.values = settings.Thresholds
	return settings, nil
}

type testAssets struct{ usecase.AssetRepository }

func (testAssets) List(context.Context, usecase.ListOptions) ([]domain.Asset, error) {
	return []domain.Asset{}, nil
}

type fixture struct {
	handler  http.Handler
	server   *Server
	sessions *testSessions
	settings *testSettings
	clock    *testClock
}

func newFixture() fixture {
	clock := &testClock{time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)}
	sessions := &testSessions{rows: map[string]domain.Session{}}
	settings := &testSettings{values: []int{10, 20}}
	api := &Server{PublicOrigin: "https://stock.example.com", Clock: clock, Assets: usecase.Assets{Repository: testAssets{}, Clock: clock}, Settings: usecase.Settings{Repository: settings, Clock: clock}, Auth: usecase.Auth{Repository: sessions, Tokens: security.Tokens{}, Passwords: testPasswords{}, Clock: clock, AdminID: "owner"}}
	return fixture{handler: api.Handler(), server: api, sessions: sessions, settings: settings, clock: clock}
}
func (f fixture) request(method, path, body string, cookies []*http.Cookie, csrf string, modify func(*http.Request)) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "https://stock.example.com"+path, strings.NewReader(body))
	r.RemoteAddr = "192.0.2.1:12345"
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://stock.example.com")
	for _, cookie := range cookies {
		r.AddCookie(cookie)
	}
	if csrf != "" {
		r.Header.Set("X-CSRF-Token", csrf)
	}
	if modify != nil {
		modify(r)
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}
func (f fixture) login(t *testing.T) ([]*http.Cookie, string) {
	t.Helper()
	w := f.request("POST", "/api/v1/auth/login", `{"admin_id":"owner","password":"correct"}`, nil, "", nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var response sessionDTO
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return w.Result().Cookies(), response.CSRFToken
}
func TestSessionLifecycleAndCookies(t *testing.T) {
	f := newFixture()
	cookies, csrf := f.login(t)
	if len(cookies) != 2 || len(f.sessions.rows) != 1 {
		t.Fatal("session not issued")
	}
	for _, cookie := range cookies {
		if !cookie.Secure || !cookie.HttpOnly || cookie.Path != "/" || cookie.Domain != "" || cookie.SameSite != http.SameSiteStrictMode || cookie.MaxAge != 86400 {
			t.Fatal(cookie)
		}
		if _, ok := f.sessions.rows[cookie.Value]; ok {
			t.Fatal("plaintext token persisted")
		}
	}
	w := f.request("GET", "/api/v1/auth/session", "", cookies, "", nil)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Body.String(), csrf) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = f.request("POST", "/api/v1/auth/logout", "", cookies, csrf, nil)
	if w.Code != 204 || len(f.sessions.rows) != 0 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, cookie := range w.Result().Cookies() {
		if cookie.MaxAge >= 0 {
			t.Fatal("cookie not deleted")
		}
	}
	if w := f.request("GET", "/api/v1/assets", "", cookies, "", nil); w.Code != 401 {
		t.Fatal("old session valid")
	}
	cookies, _ = f.login(t)
	f.clock.now = f.clock.now.Add(24 * time.Hour)
	if w := f.request("GET", "/api/v1/assets", "", cookies, "", nil); w.Code != 401 {
		t.Fatal("session accepted at expiry boundary")
	}
}
func TestLoginReplacesExistingSession(t *testing.T) {
	f := newFixture()
	old, _ := f.login(t)
	w := f.request("POST", "/api/v1/auth/login", `{"admin_id":"owner","password":"correct"}`, old, "", nil)
	if w.Code != 200 || len(f.sessions.rows) != 1 {
		t.Fatal(w.Code)
	}
	if w := f.request("GET", "/api/v1/auth/session", "", old, "", nil); w.Code != 401 {
		t.Fatal("old token not revoked")
	}
}
func TestCSRFAndStrictInputs(t *testing.T) {
	f := newFixture()
	cookies, csrf := f.login(t)
	for _, tc := range []struct {
		name, method, path, body, token string
		status                          int
		change                          func(*http.Request)
	}{
		{"no Origin", "POST", "/api/v1/auth/login", `{}`, "", 403, func(r *http.Request) { r.Header.Del("Origin") }},
		{"other Origin", "PUT", "/api/v1/settings/notifications", `{"thresholds":[10]}`, csrf, 403, func(r *http.Request) { r.Header.Set("Origin", "https://other.example") }},
		{"missing CSRF", "PUT", "/api/v1/settings/notifications", `{"thresholds":[10]}`, "", 403, nil},
		{"wrong CSRF", "PUT", "/api/v1/settings/notifications", `{"thresholds":[10]}`, "wrong", 403, nil},
		{"unknown property", "PUT", "/api/v1/settings/notifications", `{"thresholds":[10],"x":1}`, csrf, 400, nil},
		{"duplicate property", "PUT", "/api/v1/settings/notifications", `{"thresholds":[10],"thresholds":[20]}`, csrf, 400, nil},
		{"null element", "PUT", "/api/v1/settings/notifications", `{"thresholds":[null]}`, csrf, 400, nil},
		{"decimal element", "PUT", "/api/v1/settings/notifications", `{"thresholds":[1.5]}`, csrf, 400, nil},
		{"string element", "PUT", "/api/v1/settings/notifications", `{"thresholds":["10"]}`, csrf, 400, nil},
		{"null boolean", "PATCH", "/api/v1/assets/1", `{"monitoring_enabled":null}`, csrf, 400, nil},
		{"missing property", "PATCH", "/api/v1/assets/1", `{}`, csrf, 400, nil},
		{"trailing JSON", "PATCH", "/api/v1/assets/1", `{"monitoring_enabled":true}{}`, csrf, 400, nil},
		{"wrong media", "PUT", "/api/v1/settings/notifications", `{"thresholds":[10]}`, csrf, 415, func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }},
		{"leading zero ID", "DELETE", "/api/v1/assets/01", "", csrf, 400, nil},
		{"overflow ID", "DELETE", "/api/v1/assets/9223372036854775808", "", csrf, 400, nil},
		{"unexpected delete body", "DELETE", "/api/v1/assets/1", `{}`, csrf, 400, nil},
		{"unexpected logout body", "POST", "/api/v1/auth/logout", `{}`, csrf, 400, nil},
		{"unknown query", "GET", "/api/v1/assets?x=1", "", "", 400, nil},
		{"duplicate query", "GET", "/api/v1/assets?q=a&q=b", "", "", 400, nil},
		{"empty sort", "GET", "/api/v1/assets?sort=", "", "", 400, nil},
		{"bad enum", "GET", "/api/v1/assets?sort=sql", "", "", 400, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := f.request(tc.method, tc.path, tc.body, cookies, tc.token, tc.change)
			if w.Code != tc.status {
				t.Fatal(w.Code, w.Body.String())
			}
			var response struct{ Error apiError }
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || response.Error.Fields == nil {
				t.Fatal("invalid error schema", w.Body.String())
			}
		})
	}
	if f.settings.updates != 0 {
		t.Fatal("invalid request mutated settings")
	}
	w := f.request("PUT", "/api/v1/settings/notifications", `{"thresholds":[30,10,20]}`, cookies, csrf, nil)
	if w.Code != 200 || f.settings.values[0] != 10 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = f.request("GET", "/api/v1/assets", "", cookies, "", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"items":[]`) {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestLoginFailureWindow(t *testing.T) {
	f := newFixture()
	for i := 0; i < 5; i++ {
		w := f.request("POST", "/api/v1/auth/login", `{"admin_id":"owner","password":"wrong"}`, nil, "", func(r *http.Request) { r.Header.Set("X-Forwarded-For", "203.0.113."+string(rune('1'+i))) })
		if w.Code != 401 {
			t.Fatal(w.Code)
		}
	}
	w := f.request("POST", "/api/v1/auth/login", `{"admin_id":"owner","password":"correct"}`, nil, "", nil)
	if w.Code != 429 || w.Header().Get("Retry-After") != "900" {
		t.Fatal(w.Code, w.Header())
	}
	f.clock.now = f.clock.now.Add(15 * time.Minute)
	_, _ = f.login(t)
}
func TestTrustedProxyChain(t *testing.T) {
	s := &Server{TrustedProxies: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.1:1234"
	r.Header.Set("X-Forwarded-For", "192.0.2.99, 203.0.113.1, 10.1.0.2")
	if got := s.clientIP(r); got != "203.0.113.1" {
		t.Fatal(got)
	}
	r.RemoteAddr = "192.0.2.1:1234"
	if got := s.clientIP(r); got != "192.0.2.1" {
		t.Fatal(got)
	}
}
