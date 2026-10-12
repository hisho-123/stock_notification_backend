package usecase_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hisho-123/stock_notification_backend/internal/domain"
	"github.com/hisho-123/stock_notification_backend/internal/usecase"
)

type sessionRepo struct {
	usecase.SessionRepository
	row                 domain.Session
	err                 error
	old, found, deleted string
	replacements        int
}

func (r *sessionRepo) FindSession(_ context.Context, hash string) (domain.Session, error) {
	r.found = hash
	return r.row, r.err
}
func (r *sessionRepo) ReplaceSession(_ context.Context, old string, row domain.Session) error {
	r.replacements++
	r.old, r.row = old, row
	return r.err
}
func (r *sessionRepo) DeleteSession(_ context.Context, hash string) error {
	r.deleted = hash
	return r.err
}

type tokenSource struct{ calls, failAt int }

func (t *tokenSource) Generate() (string, error) {
	t.calls++
	if t.calls == t.failAt {
		return "", usecase.ErrUnavailable
	}
	if t.calls == 1 {
		return "session-secret", nil
	}
	return "csrf-secret", nil
}
func (*tokenSource) Hash(s string) string            { return "hash:" + s }
func (t *tokenSource) EqualHash(s, hash string) bool { return t.Hash(s) == hash }

type passwordCheck struct {
	matches bool
	calls   int
}

func (p *passwordCheck) Matches(_, _ string) bool { p.calls++; return p.matches }

func authFixture() (usecase.Auth, *sessionRepo, *tokenSource, *passwordCheck) {
	now := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	r, tokens, passwords := &sessionRepo{}, &tokenSource{}, &passwordCheck{matches: true}
	return usecase.Auth{Repository: r, Tokens: tokens, Passwords: passwords, Clock: &clock{now}, AdminID: "owner", PasswordHash: "hash"}, r, tokens, passwords
}

func TestLoginValidationAndFailures(t *testing.T) {
	for _, tc := range []struct{ name, id, password, field string }{
		{"empty ID", "", "correct", "admin_id"}, {"invalid ID UTF8", "\xff", "correct", "admin_id"},
		{"long ID", strings.Repeat("界", 101), "correct", "admin_id"},
		{"empty password", "owner", "", "password"}, {"invalid password UTF8", "owner", "\xff", "password"},
		{"long password", "owner", strings.Repeat("a", 73), "password"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, r, tokens, passwords := authFixture()
			_, err := a.Login(context.Background(), tc.id, tc.password, "")
			var validation *usecase.ValidationError
			if !errors.As(err, &validation) || validation.Fields[tc.field] == "" || passwords.calls != 0 || tokens.calls != 0 || r.replacements != 0 {
				t.Fatalf("err=%v passwords=%d tokens=%d saves=%d", err, passwords.calls, tokens.calls, r.replacements)
			}
		})
	}
	for _, tc := range []struct {
		name                   string
		wrongID, wrongPassword bool
		tokenFailure           int
		saveFailure            bool
	}{
		{name: "wrong ID", wrongID: true}, {name: "wrong password", wrongPassword: true},
		{name: "session token failure", tokenFailure: 1}, {name: "CSRF token failure", tokenFailure: 2}, {name: "save failure", saveFailure: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, r, tokens, passwords := authFixture()
			id := "owner"
			if tc.wrongID {
				id = "other"
			}
			passwords.matches = !tc.wrongPassword
			tokens.failAt = tc.tokenFailure
			want := usecase.ErrUnavailable
			if tc.wrongID || tc.wrongPassword {
				want = usecase.ErrInvalidCredentials
			}
			if tc.saveFailure {
				r.err = want
			}
			_, err := a.Login(context.Background(), id, "correct", "")
			if !errors.Is(err, want) || passwords.calls != 1 {
				t.Fatalf("err=%v password checks=%d", err, passwords.calls)
			}
			if !tc.saveFailure && r.replacements != 0 {
				t.Fatal("failed login persisted a session")
			}
		})
	}
}

func TestLoginPersistsHashesAndReplacesSession(t *testing.T) {
	for _, old := range []string{"", "old-secret"} {
		t.Run("old="+old, func(t *testing.T) {
			a, r, tokens, _ := authFixture()
			got, err := a.Login(context.Background(), "owner", "correct", old)
			wantOld := ""
			if old != "" {
				wantOld = tokens.Hash(old)
			}
			if err != nil || tokens.calls != 2 || r.replacements != 1 || r.old != wantOld || got.Token != "session-secret" || got.CSRFToken != "csrf-secret" || got.AdminID != "owner" || r.row.TokenHash != tokens.Hash(got.Token) || r.row.CSRFTokenHash != tokens.Hash(got.CSRFToken) || !r.row.CreatedAt.Equal(a.Clock.Now()) || !r.row.ExpiresAt.Equal(a.Clock.Now().Add(24*time.Hour)) {
				t.Fatalf("result=%+v row=%+v old=%s err=%v", got, r.row, r.old, err)
			}
		})
	}
}

func TestAuthenticationAndLogout(t *testing.T) {
	for _, tc := range []struct {
		name, token, csrf  string
		repoErr            error
		expired, wrongCSRF bool
	}{
		{"valid", "token", "csrf", nil, false, false}, {"no token", "", "csrf", nil, false, false}, {"no CSRF", "token", "", nil, false, false},
		{"missing session", "token", "csrf", usecase.ErrNotFound, false, false}, {"database failure", "token", "csrf", usecase.ErrUnavailable, false, false},
		{"expired", "token", "csrf", nil, true, false}, {"wrong CSRF", "token", "csrf", nil, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, r, tokens, _ := authFixture()
			r.err = tc.repoErr
			r.row = domain.Session{TokenHash: tokens.Hash(tc.token), CSRFTokenHash: tokens.Hash(tc.csrf), ExpiresAt: a.Clock.Now().Add(time.Hour)}
			if tc.expired {
				r.row.ExpiresAt = a.Clock.Now()
			}
			if tc.wrongCSRF {
				r.row.CSRFTokenHash = "other"
			}
			got, err := a.Authenticate(context.Background(), tc.token, tc.csrf)
			want := usecase.ErrUnauthenticated
			if tc.name == "valid" {
				want = nil
			}
			if tc.repoErr == usecase.ErrUnavailable {
				want = tc.repoErr
			}
			if !errors.Is(err, want) {
				t.Fatalf("got %v want %v", err, want)
			}
			if want == nil {
				if got.AdminID != "owner" || got.CSRFToken != tc.csrf || got.Session != r.row || !a.CheckCSRF(got, tc.csrf) || a.CheckCSRF(got, "") || a.CheckCSRF(got, "other") {
					t.Fatal("invalid authentication", got)
				}
				for _, failure := range []error{nil, usecase.ErrUnavailable} {
					r.err = failure
					if err := a.Logout(context.Background(), got); !errors.Is(err, failure) || r.deleted != r.row.TokenHash {
						t.Fatal("logout", err, r.deleted)
					}
				}
			}
			if tc.token == "" || tc.csrf == "" {
				if r.found != "" {
					t.Fatal("invalid input queried repository")
				}
			} else if r.found != tokens.Hash(tc.token) {
				t.Fatal("plaintext token queried")
			}
		})
	}
}
