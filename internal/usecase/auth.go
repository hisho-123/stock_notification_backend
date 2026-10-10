package usecase

import (
	"context"
	"errors"
	"time"
	"unicode/utf8"

	"github.com/hisho-123/stock_notification_backend/internal/domain"
)

type Auth struct {
	Repository            SessionRepository
	Passwords             PasswordVerifier
	Tokens                Tokens
	Clock                 Clock
	AdminID, PasswordHash string
}
type Authenticated struct {
	AdminID, CSRFToken string
	Session            domain.Session
}
type LoginResult struct {
	Authenticated
	Token string
}

func (s Auth) Login(ctx context.Context, id, password, oldToken string) (LoginResult, error) {
	if !utf8.ValidString(id) || utf8.RuneCountInString(id) < 1 || utf8.RuneCountInString(id) > 100 {
		return LoginResult{}, Invalid("admin_id", "IDを確認してください。")
	}
	if !utf8.ValidString(password) || len(password) < 1 || len(password) > 72 {
		return LoginResult{}, Invalid("password", "パスワードは1〜72バイトで指定してください。")
	}
	matched := s.Passwords.Matches(s.PasswordHash, password)
	if id != s.AdminID || !matched {
		return LoginResult{}, ErrInvalidCredentials
	}
	token, err := s.Tokens.Generate()
	if err != nil {
		return LoginResult{}, err
	}
	csrf, err := s.Tokens.Generate()
	if err != nil {
		return LoginResult{}, err
	}
	now := s.Clock.Now()
	session := domain.Session{TokenHash: s.Tokens.Hash(token), CSRFTokenHash: s.Tokens.Hash(csrf), CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour)}
	oldHash := ""
	if oldToken != "" {
		oldHash = s.Tokens.Hash(oldToken)
	}
	if err := s.Repository.ReplaceSession(ctx, oldHash, session); err != nil {
		return LoginResult{}, err
	}
	return LoginResult{Authenticated: Authenticated{AdminID: s.AdminID, CSRFToken: csrf, Session: session}, Token: token}, nil
}
func (s Auth) Authenticate(ctx context.Context, token, csrf string) (Authenticated, error) {
	if token == "" || csrf == "" {
		return Authenticated{}, ErrUnauthenticated
	}
	session, err := s.Repository.FindSession(ctx, s.Tokens.Hash(token))
	if errors.Is(err, ErrNotFound) {
		return Authenticated{}, ErrUnauthenticated
	}
	if err != nil {
		return Authenticated{}, err
	}
	if !session.ValidAt(s.Clock.Now()) || !s.Tokens.EqualHash(csrf, session.CSRFTokenHash) {
		return Authenticated{}, ErrUnauthenticated
	}
	return Authenticated{AdminID: s.AdminID, CSRFToken: csrf, Session: session}, nil
}
func (s Auth) CheckCSRF(auth Authenticated, token string) bool {
	return token != "" && s.Tokens.EqualHash(token, auth.Session.CSRFTokenHash)
}
func (s Auth) Logout(ctx context.Context, auth Authenticated) error {
	return s.Repository.DeleteSession(ctx, auth.Session.TokenHash)
}
