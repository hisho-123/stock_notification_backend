package usecase

import (
	"context"
	"regexp"
	"time"

	"github.com/hisho-123/stock_notification_backend/internal/domain"
)

type UserRepository interface {
	FindUser(context.Context, string) (domain.User, error)
}
type RegistrationRepository interface {
	ValidateRegistration(context.Context, string, time.Time) (time.Time, error)
	Register(context.Context, string, domain.User, domain.Session, string, time.Time) error
	ProcessLINEEvent(context.Context, domain.LINEEvent, domain.RegistrationToken, string, string, time.Time) (domain.LINEReply, error)
	SaveLINEReply(context.Context, string, string) error
	DeleteExpiredRegistrationTokens(context.Context, time.Time) error
}
type PasswordHasher interface{ Hash(string) (string, error) }
type LINEReplier interface {
	Reply(context.Context, string, string) error
}
type Registration struct {
	Repository RegistrationRepository
	Passwords  PasswordHasher
	Auth       Auth
}

var registrationTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

func (s Registration) Validate(ctx context.Context, token string) (time.Time, error) {
	if !registrationTokenPattern.MatchString(token) {
		return time.Time{}, ErrRegistrationUnavailable
	}
	return s.Repository.ValidateRegistration(ctx, s.Auth.Tokens.Hash(token), s.Auth.Clock.Now())
}
func (s Registration) Register(ctx context.Context, token, id, password, oldToken string) (LoginResult, error) {
	if err := validateCredentials(id, password); err != nil {
		return LoginResult{}, err
	}
	if _, err := s.Validate(ctx, token); err != nil {
		return LoginResult{}, err
	}
	hash, err := s.Passwords.Hash(password)
	if err != nil {
		return LoginResult{}, err
	}
	result, err := s.Auth.newSession(id)
	if err != nil {
		return LoginResult{}, err
	}
	oldHash := ""
	if oldToken != "" {
		oldHash = s.Auth.Tokens.Hash(oldToken)
	}
	now := s.Auth.Clock.Now()
	user := domain.User{LoginID: id, PasswordHash: hash, LINEFollowing: true, CreatedAt: now, UpdatedAt: now}
	if err = s.Repository.Register(ctx, s.Auth.Tokens.Hash(token), user, result.Session, oldHash, now); err != nil {
		return LoginResult{}, err
	}
	return result, nil
}

type LINEWebhook struct {
	Repository   RegistrationRepository
	Tokens       Tokens
	Clock        Clock
	Replier      LINEReplier
	PublicOrigin string
	Report       func(string, string)
}

func (s LINEWebhook) Handle(ctx context.Context, events []domain.LINEEvent) error {
	for _, event := range events {
		plain, err := s.Tokens.Generate()
		if err != nil {
			return err
		}
		now := s.Clock.Now()
		token := domain.RegistrationToken{TokenHash: s.Tokens.Hash(plain), LINEUserID: event.LINEUserID, CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour)}
		reply, err := s.Repository.ProcessLINEEvent(ctx, event, token, s.PublicOrigin+"/register#token="+plain, s.PublicOrigin+"/login", now)
		if err != nil {
			return err
		}
		if reply.Text == "" {
			continue
		}
		// 業務処理のコミット後だけ返信する。結果保存に失敗しても同じイベントを再返信しない。
		replyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		status := "sent"
		if s.Replier.Reply(replyCtx, event.ReplyToken, reply.Text) != nil {
			status = "failed"
		}
		cancel()
		if s.Repository.SaveLINEReply(ctx, event.ID, status) != nil {
			status = "pending"
		}
		if s.Report != nil {
			s.Report(event.ID, status)
		}
	}
	return nil
}
