package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hisho-123/stock_notification_backend/internal/usecase"
)

type brokenBody struct{}

func (brokenBody) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (brokenBody) Close() error             { return nil }

func TestErrorResponseContract(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{usecase.Invalid("code", "invalid"), 400, "VALIDATION_ERROR"}, {&usecase.ValidationError{}, 400, "VALIDATION_ERROR"},
		{errMediaType, 415, "UNSUPPORTED_MEDIA_TYPE"}, {usecase.ErrInvalidCredentials, 401, "INVALID_CREDENTIALS"},
		{usecase.ErrUnauthenticated, 401, "UNAUTHENTICATED"}, {usecase.ErrCSRF, 403, "CSRF_FAILED"},
		{usecase.ErrNotFound, 404, "ASSET_NOT_FOUND"}, {usecase.ErrAlreadyExists, 409, "ASSET_ALREADY_EXISTS"},
		{usecase.ErrSymbolUnavailable, 422, "SYMBOL_UNAVAILABLE"}, {usecase.ErrPriceProvider, 502, "PRICE_PROVIDER_ERROR"},
		{usecase.ErrPriceTimeout, 504, "PRICE_PROVIDER_TIMEOUT"}, {usecase.ErrUnavailable, 503, "SERVICE_UNAVAILABLE"},
		{usecase.ErrConnectionLost, 503, "SERVICE_UNAVAILABLE"}, {errors.New("secret SQL provider response"), 500, "INTERNAL_ERROR"},
	} {
		t.Run(tc.code+"/"+tc.err.Error(), func(t *testing.T) {
			w := httptest.NewRecorder()
			writeError(w, tc.err)
			var got struct{ Error apiError }
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != tc.status || got.Error.Code != tc.code || got.Error.Message == "" || got.Error.Fields == nil || strings.Contains(w.Body.String(), "secret") {
				t.Fatal(w.Code, w.Body.String(), err)
			}
			if v, ok := tc.err.(*usecase.ValidationError); ok && v.Fields != nil && !reflect.DeepEqual(got.Error.Fields, v.Fields) {
				t.Fatal(got.Error.Fields)
			}
		})
	}
}

func TestInvalidObjectsAndHandlerInputs(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, body string
		change                   func(*http.Request)
	}{
		{"empty object body", "POST", "/api/v1/assets", "", nil}, {"array body", "POST", "/api/v1/assets", "[]", nil},
		{"nonstring code", "POST", "/api/v1/assets", `{"code":42}`, nil}, {"invalid code", "POST", "/api/v1/assets", `{"code":" "}`, nil},
		{"null code", "POST", "/api/v1/assets", `{"code":null}`, nil}, {"broken key", "POST", "/api/v1/assets", `{"code":"A",,}`, nil},
		{"missing close", "POST", "/api/v1/assets", `{"code":"A"`, nil}, {"broken value", "POST", "/api/v1/assets", `{"code":`, nil},
		{"wrong close", "POST", "/api/v1/assets", `{"code":"A"]`, nil},
		{"invalid UTF8", "POST", "/api/v1/assets", "{\"code\":\"\xff\"}", nil},
		{"large body", "POST", "/api/v1/assets", strings.Repeat(" ", 64<<10+1), nil},
		{"body read error", "POST", "/api/v1/assets", "", func(r *http.Request) { r.Body = brokenBody{} }},
		{"bad patch ID", "PATCH", "/api/v1/assets/a", `{"monitoring_enabled":true}`, nil},
		{"bad boolean", "PATCH", "/api/v1/assets/1", `{"monitoring_enabled":"true"}`, nil},
		{"bad login ID type", "POST", "/api/v1/auth/login", `{"login_id":42,"password":"correct-password"}`, nil},
		{"bad password type", "POST", "/api/v1/auth/login", `{"login_id":"owner","password":42}`, nil},
		{"bad login body", "POST", "/api/v1/auth/login", `{}`, nil},
		{"not array", "PUT", "/api/v1/settings/notifications", `{"thresholds":10}`, nil},
		{"empty thresholds", "PUT", "/api/v1/settings/notifications", `{"thresholds":[]}`, nil},
		{"invalid query escape", "GET", "/api/v1/assets?q=%zz", "", nil},
		{"invalid ID character", "DELETE", "/api/v1/assets/1x", "", nil},
		{"delete read failure", "DELETE", "/api/v1/assets/1", "", func(r *http.Request) { r.Body = brokenBody{} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture()
			cookies, csrf := f.login(t)
			w := f.request(tc.method, tc.path, tc.body, cookies, csrf, tc.change)
			if w.Code != 400 || f.settings.updates != 0 || len(f.assets.calls) != 0 {
				t.Fatal(w.Code, w.Body.String(), f.assets.calls)
			}
		})
	}
	r := httptest.NewRequest("DELETE", "/", nil)
	if _, err := parseID(r); err == nil {
		t.Fatal("empty ID accepted")
	}
}

func TestHTTPDependencyErrorsAndAssetResponses(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, body string
		err                      error
		status                   int
	}{
		{"create provider", "POST", "/api/v1/assets", `{"code":"AAPL"}`, usecase.ErrPriceProvider, 502},
		{"create timeout", "POST", "/api/v1/assets", `{"code":"AAPL"}`, usecase.ErrPriceTimeout, 504},
		{"create symbol", "POST", "/api/v1/assets", `{"code":"AAPL"}`, usecase.ErrSymbolUnavailable, 422},
		{"update missing", "PATCH", "/api/v1/assets/1", `{"monitoring_enabled":true}`, usecase.ErrNotFound, 404},
		{"settings read", "GET", "/api/v1/settings/notifications", "", usecase.ErrUnavailable, 503},
		{"settings save", "PUT", "/api/v1/settings/notifications", `{"thresholds":[10]}`, usecase.ErrUnavailable, 503},
		{"logout failure", "POST", "/api/v1/auth/logout", "", usecase.ErrUnavailable, 503},
		{"authenticate failure", "GET", "/api/v1/assets", "", usecase.ErrUnavailable, 503},
		{"login save failure", "POST", "/api/v1/auth/login", `{"login_id":"owner","password":"correct-password"}`, usecase.ErrUnavailable, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture()
			cookies, csrf := f.login(t)
			switch {
			case strings.HasPrefix(tc.name, "create"):
				f.prices.err = tc.err
			case tc.name == "update missing":
				f.assets.err = tc.err
			case strings.HasPrefix(tc.name, "settings"):
				f.settings.err = tc.err
			case tc.name == "logout failure":
				// Authentication succeeds; the repository fails only when deleting.
				f.server.Auth.Repository = deleteFailSessions{f.sessions}
			default:
				f.sessions.err = tc.err
			}
			w := f.request(tc.method, tc.path, tc.body, cookies, csrf, nil)
			if w.Code != tc.status {
				t.Fatal(w.Code, w.Body.String())
			}
			if strings.Contains(tc.name, "login") || tc.name == "logout failure" {
				if len(w.Result().Cookies()) != 0 {
					t.Fatal("failure changed cookies")
				}
			}
		})
	}
	f := newFixture()
	cookies, csrf := f.login(t)
	w := f.request("POST", "/api/v1/assets", `{"code":" aapl "}`, cookies, csrf, nil)
	var got assetDTO
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != 201 || got.ID != "1" || got.Code != "AAPL" || got.HighestValue != "100" || got.LatestValue != "100" || got.DeclineRate != "0.00" || got.LatestFetchedAt != "2026-10-10T00:00:00Z" || !got.MonitoringEnabled || w.Header().Get("Location") != "/api/v1/assets/1" {
		t.Fatal(w.Code, w.Body.String(), err)
	}
	w = f.request("GET", "/api/v1/assets", "", cookies, "", nil)
	var list struct {
		Items []assetDTO
		Total int
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || list.Total != 1 || len(list.Items) != 1 || list.Items[0] != got {
		t.Fatal(w.Body.String(), err)
	}
	w = f.request("PATCH", "/api/v1/assets/1", `{"monitoring_enabled":false}`, cookies, csrf, nil)
	if w.Code != 200 || f.assets.row.MonitoringEnabled {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := f.request("DELETE", "/api/v1/assets/1", "", cookies, csrf, nil); w.Code != 204 {
		t.Fatal(w.Code)
	}
}

type deleteFailSessions struct{ *testSessions }

func (deleteFailSessions) DeleteSession(context.Context, string) error { return usecase.ErrUnavailable }

func TestGuardRecoveryAndResponseHeaders(t *testing.T) {
	for _, change := range []func(*http.Request){
		func(r *http.Request) { r.Header.Add("Origin", "https://stock.example.com") },
		func(r *http.Request) { r.Header.Add("X-CSRF-Token", "other") },
	} {
		f := newFixture()
		cookies, csrf := f.login(t)
		w := f.request("PUT", "/api/v1/settings/notifications", `{"thresholds":[10]}`, cookies, csrf, change)
		if w.Code != 403 || f.settings.updates != 0 {
			t.Fatal(w.Code)
		}
	}
	f := newFixture()
	cookies, csrf := f.login(t)
	if w := f.request("DELETE", "/api/v1/assets/1", "x", cookies, csrf, func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }); w.Code != 415 {
		t.Fatal(w.Code)
	}
	if w := f.request("GET", "/api/v1/unknown", "", cookies, "", nil); w.Code != 404 {
		t.Fatal(w.Code)
	}
	if w := f.request("GET", "/missing.js", "", nil, "", nil); w.Code != 404 || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal(w.Code, w.Header())
	}
	w := httptest.NewRecorder()
	f.server.recover(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("secret panic") })).ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 500 || strings.Contains(w.Body.String(), "secret") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	responseWriter{w}.WriteHeader(503)
	if w.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatal(w.Header())
	}
}

func TestProxyFallbacksAndPartialFailureWindow(t *testing.T) {
	s := &Server{TrustedProxies: []netip.Prefix{netip.MustParsePrefix("192.168.0.0/16"), netip.MustParsePrefix("10.0.0.0/8")}}
	for _, tc := range []struct{ remote, forwarded, want string }{
		{"192.0.2.1", "", "192.0.2.1"}, {"invalid", "", "invalid"}, {"[::ffff:192.0.2.1]:123", "", "192.0.2.1"},
		{"10.0.0.1:123", "bad", "10.0.0.1"}, {"10.0.0.1:123", "10.1.0.2,10.2.0.3", "10.1.0.2"},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = tc.remote
		r.Header.Set("X-Forwarded-For", tc.forwarded)
		if got := s.clientIP(r); got != tc.want {
			t.Fatal(got, tc)
		}
	}
	now := time.Now()
	l := &attemptLimiter{failures: map[string][]time.Time{}}
	l.failed("ip", now)
	for i := 0; i < 4; i++ {
		l.failed("ip", now.Add(time.Minute))
	}
	if got := l.retryAfter("ip", now.Add(15*time.Minute)); got != 0 || len(l.failures["ip"]) != 4 {
		t.Fatal(got, l.failures)
	}
	l.failed("ip", now.Add(15*time.Minute))
	if got := l.retryAfter("ip", now.Add(15*time.Minute)); got != 60 {
		t.Fatal(got)
	}
	f := newFixture()
	for i := 0; i < 4; i++ {
		f.request("POST", "/api/v1/auth/login", `{"login_id":"owner","password":"wrong-password"}`, nil, "", nil)
	}
	f.login(t)
	if len(f.server.limiter.failures) != 0 {
		t.Fatal("successful login did not clear failure window")
	}
}
