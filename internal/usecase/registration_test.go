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

type registrationRepository struct {
	usecase.RegistrationRepository
	validateErr, registerErr, processErr, saveErr error
	reply                                         string
	registered, processed, saved                  int
}

func (r *registrationRepository) ValidateRegistration(context.Context, string, time.Time) (time.Time, error) {
	return time.Now().Add(time.Hour), r.validateErr
}
func (r *registrationRepository) Register(context.Context, string, domain.User, domain.Session, string, time.Time) error {
	r.registered++
	return r.registerErr
}
func (r *registrationRepository) ProcessLINEEvent(context.Context, domain.LINEEvent, domain.RegistrationToken, string, string, time.Time) (domain.LINEReply, error) {
	r.processed++
	return domain.LINEReply{Text: r.reply}, r.processErr
}
func (r *registrationRepository) SaveLINEReply(context.Context, string, string) error {
	r.saved++
	return r.saveErr
}

type hasher struct{ err error }

func (h hasher) Hash(string) (string, error) { return "password-hash", h.err }
func TestRegistrationDependencyFailures(t *testing.T) {
	for _, stage := range []string{"validation", "password hash", "session token", "registration write", "success"} {
		t.Run(stage, func(t *testing.T) {
			auth, _, tokens, _ := authFixture()
			repo := &registrationRepository{}
			h := hasher{}
			want := usecase.ErrUnavailable
			switch stage {
			case "validation":
				repo.validateErr = want
			case "password hash":
				h.err = want
			case "session token":
				tokens.failAt = 1
			case "registration write":
				repo.registerErr = want
			case "success":
				want = nil
			}
			result, err := (usecase.Registration{Repository: repo, Passwords: h, Auth: auth}).Register(context.Background(), strings.Repeat("a", 43), "owner", "password", "")
			if !errors.Is(err, want) {
				t.Fatal(err)
			}
			if want != nil && stage != "registration write" && repo.registered != 0 {
				t.Fatal("failed input created a user")
			}
			if want == nil && (result.LoginID != "owner" || repo.registered != 1) {
				t.Fatal(result, repo)
			}
		})
	}
}

type replier struct {
	calls int
	err   error
}

func (r *replier) Reply(context.Context, string, string) error { r.calls++; return r.err }
func TestWebhookRepliesAfterCommitAndReportsPending(t *testing.T) {
	for _, stage := range []string{"token generation", "event persistence", "no reply", "sent", "failed", "pending"} {
		t.Run(stage, func(t *testing.T) {
			auth, _, tokens, _ := authFixture()
			repo := &registrationRepository{reply: "URL"}
			reply := &replier{}
			reported := ""
			want := error(nil)
			switch stage {
			case "token generation":
				tokens.failAt = 1
				want = usecase.ErrUnavailable
			case "event persistence":
				repo.processErr = usecase.ErrUnavailable
				want = repo.processErr
			case "no reply":
				repo.reply = ""
			case "failed":
				reply.err = usecase.ErrUnavailable
			case "pending":
				repo.saveErr = usecase.ErrUnavailable
			}
			hook := usecase.LINEWebhook{Repository: repo, Tokens: tokens, Clock: auth.Clock, Replier: reply, PublicOrigin: "https://stock.example.com", Report: func(id, status string) {
				if id != "event" {
					t.Error(id)
				}
				reported = status
			}}
			err := hook.Handle(context.Background(), []domain.LINEEvent{{ID: "event", Type: "follow"}})
			if !errors.Is(err, want) {
				t.Fatal(err)
			}
			if stage == "token generation" && repo.processed != 0 || want != nil && reply.calls != 0 || stage == "no reply" && reply.calls != 0 {
				t.Fatal(repo, reply)
			}
			if stage == "sent" || stage == "failed" || stage == "pending" {
				if reported != stage || reply.calls != 1 || repo.saved != 1 {
					t.Fatal(reported, reply, repo)
				}
			}
		})
	}
}

type failingUsers struct{ err error }

func (u failingUsers) FindUser(context.Context, string) (domain.User, error) {
	return domain.User{}, u.err
}
func TestAuthenticationRechecksExistingUserAndDatabase(t *testing.T) {
	auth, repo, tokens, passwords := authFixture()
	auth.Users = failingUsers{usecase.ErrUnavailable}
	if _, err := auth.Login(context.Background(), "owner", "password", ""); !errors.Is(err, usecase.ErrUnavailable) || passwords.calls != 0 {
		t.Fatal(err, passwords.calls)
	}
	repo.row = domain.Session{LoginID: "owner", TokenHash: tokens.Hash("token"), CSRFTokenHash: tokens.Hash("csrf"), ExpiresAt: auth.Clock.Now().Add(time.Hour)}
	for _, err := range []error{usecase.ErrNotFound, usecase.ErrUnavailable} {
		auth.Users = failingUsers{err}
		want := err
		if err == usecase.ErrNotFound {
			want = usecase.ErrUnauthenticated
		}
		if _, got := auth.Authenticate(context.Background(), "token", "csrf"); !errors.Is(got, want) {
			t.Fatal(got, want)
		}
	}
}

type cleanupRegistration struct {
	usecase.RegistrationRepository
	err error
}

func (r cleanupRegistration) DeleteExpiredRegistrationTokens(context.Context, time.Time) error {
	return r.err
}
func TestMonitorRegistrationCleanupAndOwnerReadFailures(t *testing.T) {
	now := time.Now()
	_, prices, _, m := setup(now)
	m.Registrations = cleanupRegistration{err: usecase.ErrUnavailable}
	if _, err := m.Run(context.Background(), nil); !errors.Is(err, usecase.ErrUnavailable) || len(prices.calls) != 0 {
		t.Fatal(err, prices.calls)
	}
	for _, stale := range []bool{false, true} {
		t.Run(map[bool]string{false: "decline owner lookup", true: "stale owner lookup"}[stale], func(t *testing.T) {
			memory, prices, n, m := setup(now)
			m.Users = failingUsers{usecase.ErrUnavailable}
			if stale {
				prices.fetch = func(string) (domain.Quote, error) {
					return domain.Quote{Price: p("75"), ProvidedAt: now.Add(-240 * time.Hour), FetchedAt: now}, nil
				}
			}
			result, err := m.Run(context.Background(), nil)
			if err != nil || result.Failed != 1 || len(n.messages) != 0 || len(memory.saves) != 0 {
				t.Fatal(result, err, n.messages, memory.saves)
			}
		})
	}
}
