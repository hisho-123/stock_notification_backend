package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hisho-123/stock_notification_backend/internal/adapter/security"
	"github.com/hisho-123/stock_notification_backend/internal/domain"
	"github.com/hisho-123/stock_notification_backend/internal/usecase"
)

type registrationStub struct {
	usecase.RegistrationRepository
	err, saveErr error
	expires      time.Time
	events       []domain.LINEEvent
	statuses     []string
	user         domain.User
	session      domain.Session
	oldHash      string
}

func (r *registrationStub) ValidateRegistration(context.Context, string, time.Time) (time.Time, error) {
	return r.expires, r.err
}
func (r *registrationStub) Register(_ context.Context, _ string, u domain.User, s domain.Session, old string, _ time.Time) error {
	r.user, r.session, r.oldHash = u, s, old
	return r.err
}
func (r *registrationStub) ProcessLINEEvent(_ context.Context, e domain.LINEEvent, _ domain.RegistrationToken, _ string, _ string, _ time.Time) (domain.LINEReply, error) {
	r.events = append(r.events, e)
	return domain.LINEReply{Text: "登録URL"}, r.err
}
func (r *registrationStub) SaveLINEReply(_ context.Context, _ string, status string) error {
	r.statuses = append(r.statuses, status)
	return r.saveErr
}

type hashStub struct{}

func (hashStub) Hash(string) (string, error) { return "password-hash", nil }

type replyStub struct {
	calls int
	err   error
}

func (r *replyStub) Reply(context.Context, string, string) error { r.calls++; return r.err }
func TestRegistrationEndpointsAndInput(t *testing.T) {
	f := newFixture()
	repo := &registrationStub{expires: f.clock.now.Add(24 * time.Hour)}
	f.server.Registration = usecase.Registration{Repository: repo, Passwords: hashStub{}, Auth: f.server.Auth}
	token := strings.Repeat("a", 43)
	body := `{"registration_token":"` + token + `"}`
	for _, tc := range []struct {
		name, body string
		code       int
	}{
		{"valid", body, 200}, {"unknown field", `{"registration_token":"` + token + `","line_user_id":"secret"}`, 400},
		{"wrong type", `{"registration_token":123}`, 400}, {"null", `{"registration_token":null}`, 400}, {"unavailable", `{"registration_token":"bad"}`, 410},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := f.request("POST", "/api/v1/auth/registration/validate", tc.body, nil, "", nil)
			if w.Code != tc.code {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
	for _, tc := range []struct {
		err  error
		code int
	}{{usecase.ErrLoginIDExists, 409}, {usecase.ErrRegistrationUnavailable, 410}, {usecase.ErrUnavailable, 503}, {nil, 201}} {
		repo.err = tc.err
		w := f.request("POST", "/api/v1/auth/register", `{"registration_token":"`+token+`","login_id":" New ","password":"password"}`, []*http.Cookie{{Name: sessionCookie, Value: "old"}}, "", nil)
		if w.Code != tc.code {
			t.Fatal(w.Code, w.Body.String())
		}
		if tc.code == 201 && (repo.user.LoginID != " New " || repo.user.PasswordHash != "password-hash" || repo.session.LoginID != " New " || repo.oldHash != security.Tokens{}.Hash("old") || len(w.Result().Cookies()) != 2 || !strings.Contains(w.Body.String(), `"login_id":" New "`)) {
			t.Fatal(repo, w.Body.String())
		}
	}
	w := f.request("POST", "/api/v1/auth/register", `{"registration_token":"`+token+`","login_id":"owner","password":"1234567"}`, nil, "", nil)
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
}
func TestConcurrentRegistrationIPLimit(t *testing.T) {
	f := newFixture()
	var wg sync.WaitGroup
	codes := make(chan int, 64)
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- f.request("POST", "/api/v1/auth/registration/validate", `{"registration_token":"bad"}`, nil, "", nil).Code
		}()
	}
	wg.Wait()
	close(codes)
	accepted, limited := 0, 0
	for code := range codes {
		if code == 410 {
			accepted++
		} else if code == 429 {
			limited++
		} else {
			t.Fatal(code)
		}
	}
	if accepted != 30 || limited != 34 {
		t.Fatal(accepted, limited)
	}
	// ログインとは別カウンタであり、登録成功の有無で確認用上限をリセットしない。
	if w := f.request("POST", "/api/v1/auth/login", `{"login_id":"owner","password":"correct-password"}`, nil, "", nil); w.Code != 200 {
		t.Fatal(w.Code)
	}
	f.clock.now = f.clock.now.Add(15 * time.Minute)
	if w := f.request("POST", "/api/v1/auth/registration/validate", `{"registration_token":"bad"}`, nil, "", nil); w.Code != 410 {
		t.Fatal(w.Code)
	}
}
func signedWebhook(f fixture, body string, signature bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "https://stock.example.com/api/v1/webhooks/line", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if signature {
		mac := hmac.New(sha256.New, []byte(f.server.LINEChannelSecret))
		mac.Write([]byte(body))
		req.Header.Set("X-Line-Signature", base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, req)
	return w
}
func TestLINEWebhookSignatureStructureAndReplies(t *testing.T) {
	f := newFixture()
	repo := &registrationStub{}
	replier := &replyStub{}
	f.server.LINEChannelSecret = "channel-secret"
	f.server.Webhook = usecase.LINEWebhook{Repository: repo, Tokens: security.Tokens{}, Clock: f.clock, Replier: replier, PublicOrigin: f.server.PublicOrigin}
	target := `{"type":"follow","webhookEventId":"e1","timestamp":1,"source":{"type":"user","userId":"U00000000000000000000000000000002"},"replyToken":"reply"}`
	for _, tc := range []struct {
		name, body   string
		signature    bool
		code, events int
	}{
		{"missing signature", `{"destination":"bot","events":[` + target + `]}`, false, 403, 0},
		{"empty", `{"destination":"bot","events":[],"unknown":true}`, true, 200, 0},
		{"missing destination", `{"events":[]}`, true, 400, 0},
		{"unknown and group", `{"destination":"bot","events":[{"type":"future"},{"type":"follow","source":{"type":"group"}},{"type":"follow","source":{"type":"user"}}]}`, true, 200, 0},
		{"invalid batch", `{"destination":"bot","events":[` + target + `,{"type":"follow","source":{"type":"user","userId":"U00000000000000000000000000000002"}}]}`, true, 400, 0},
		{"valid", `{"destination":"bot","events":[` + target + `]}`, true, 200, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := len(repo.events)
			w := signedWebhook(f, tc.body, tc.signature)
			if w.Code != tc.code || len(repo.events)-before != tc.events {
				t.Fatal(w.Code, w.Body.String(), len(repo.events)-before)
			}
		})
	}
	if replier.calls != 1 || len(repo.statuses) != 1 || repo.statuses[0] != "sent" {
		t.Fatal(replier, repo.statuses)
	}
	replier.err = errors.New("LINE failed")
	if w := signedWebhook(f, `{"destination":"bot","events":[`+target+`]}`, true); w.Code != 200 || repo.statuses[1] != "failed" {
		t.Fatal(w.Code, repo.statuses)
	}
	repo.err = usecase.ErrUnavailable
	if w := signedWebhook(f, `{"destination":"bot","events":[`+target+`]}`, true); w.Code != 503 || replier.calls != 2 {
		t.Fatal(w.Code, replier.calls)
	}
	repo.err = nil
	repo.saveErr = usecase.ErrUnavailable
	if w := signedWebhook(f, `{"destination":"bot","events":[`+target+`]}`, true); w.Code != 200 {
		t.Fatal(w.Code)
	}
	for i, body := range []string{`null`, `{`, fmt.Sprintf(`{"destination":"bot","events":[%s]}`, strings.Replace(target, `"timestamp":1`, `"timestamp":1.5`, 1)), `{"destination":"bot","events":null}`} {
		if w := signedWebhook(f, body, true); w.Code != 400 {
			t.Fatal(i, w.Code)
		}
	}
}

func TestWebhookBoundaryInputsAndRegistrationGuards(t *testing.T) {
	f := newFixture()
	repo := &registrationStub{}
	f.server.LINEChannelSecret = "secret"
	f.server.Webhook = usecase.LINEWebhook{Repository: repo, Tokens: security.Tokens{}, Clock: f.clock, Replier: &replyStub{}}
	user := `"source":{"type":"user","userId":"U00000000000000000000000000000002"}`
	for _, tc := range []struct {
		event string
		code  int
	}{
		{`{}`, 400}, {`{"type":"follow","source":{"type":"user","userId":"invalid"}}`, 400},
		{`{"type":"message",` + user + `}`, 400},
		{`{"type":"message",` + user + `,"message":{"type":"text","text":"other"}}`, 200},
		{`{"type":"message",` + user + `,"message":{"type":"image"}}`, 200},
		{`{"type":"message",` + user + `,"message":{"type":"text","text":" 登録 "},"webhookEventId":"text","timestamp":1,"replyToken":"reply"}`, 200},
		{`{"type":"future",` + user + `}`, 200},
	} {
		w := signedWebhook(f, `{"destination":"bot","events":[`+tc.event+`]}`, true)
		if w.Code != tc.code {
			t.Fatal(tc.event, w.Code, w.Body.String())
		}
	}
	for _, tc := range []struct {
		body   string
		code   int
		change func(*http.Request)
	}{
		{"", 415, func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }},
		{"", 400, func(r *http.Request) { r.Body = brokenBody{} }},
	} {
		r := httptest.NewRequest("POST", "https://stock.example.com/api/v1/webhooks/line", strings.NewReader(tc.body))
		r.Header.Set("Content-Type", "application/json")
		tc.change(r)
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, r)
		if w.Code != tc.code {
			t.Fatal(w.Code)
		}
	}
	for _, body := range []string{`{}`, `{"registration_token":1,"login_id":"owner","password":"password"}`} {
		w := f.request("POST", "/api/v1/auth/register", body, nil, "", nil)
		if w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
	for i := 0; i < 30; i++ {
		f.request("POST", "/api/v1/auth/register", `{}`, nil, "", nil)
	}
	if w := f.request("POST", "/api/v1/auth/register", `{}`, nil, "", nil); w.Code != 429 {
		t.Fatal(w.Code)
	}
	w := f.request("GET", "/register", "", nil, "", nil)
	if w.Header().Get("Referrer-Policy") != "no-referrer" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Header())
	}
	now := time.Now()
	limiter := &attemptLimiter{failures: map[string][]time.Time{}, limit: 1}
	if limiter.retryAfter("ip", now) != 0 {
		t.Fatal("new limiter rejected")
	}
	limiter.limit = 0
	if limiter.consume("ip", now) != 0 {
		t.Fatal("default limiter rejected")
	}
}
