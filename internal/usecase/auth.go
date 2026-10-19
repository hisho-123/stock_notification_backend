package usecase

import (
	"context"
	"errors"
	"time"
	"unicode/utf8"

	"github.com/hisho-123/stock_notification_backend/internal/domain"
)

type Auth struct {
	Repository SessionRepository
	Users      UserRepository
	Passwords  PasswordVerifier
	Tokens     Tokens
	Clock      Clock
}
type Authenticated struct {
	LoginID, CSRFToken string
	Session            domain.Session
}
type LoginResult struct {
	Authenticated
	Token string
}

// 未知のIDにもcost 12の照合を行い、存在有無による処理差を抑える。
const dummyPasswordHash = "$2a$12$Y05J1ICMqsQPaCvKOv37iuDcdysE7wpit/M.LKHWlXGlATfZSdNyS"

func validateCredentials(id, password string) error {
	if !utf8.ValidString(id) || utf8.RuneCountInString(id) < 1 || utf8.RuneCountInString(id) > 100 {
		return Invalid("login_id", "IDを確認してください。")
	}
	if !utf8.ValidString(password) || len(password) < 8 || len(password) > 72 {
		return Invalid("password", "パスワードは8〜72バイトで指定してください。")
	}
	return nil
}
func (s Auth) newSession(id string) (LoginResult, error) {
	token, err := s.Tokens.Generate()
	if err != nil {
		return LoginResult{}, err
	}
	csrf, err := s.Tokens.Generate()
	if err != nil {
		return LoginResult{}, err
	}
	now := s.Clock.Now()
	session := domain.Session{LoginID: id, TokenHash: s.Tokens.Hash(token), CSRFTokenHash: s.Tokens.Hash(csrf), CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour)}
	return LoginResult{Authenticated: Authenticated{LoginID: id, CSRFToken: csrf, Session: session}, Token: token}, nil
}
func (s Auth) Login(ctx context.Context, id, password, oldToken string) (LoginResult, error) {
	if err := validateCredentials(id, password); err != nil {
		return LoginResult{}, err
	}
	user, err := s.Users.FindUser(ctx, id)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return LoginResult{}, err
	}
	hash := user.PasswordHash
	if errors.Is(err, ErrNotFound) {
		hash = dummyPasswordHash
	}
	matched := s.Passwords.Matches(hash, password)
	if err != nil || !matched {
		return LoginResult{}, ErrInvalidCredentials
	}
	result, err := s.newSession(id)
	if err != nil {
		return LoginResult{}, err
	}
	oldHash := ""
	if oldToken != "" {
		oldHash = s.Tokens.Hash(oldToken)
	}
	if err := s.Repository.ReplaceSession(ctx, oldHash, result.Session); err != nil {
		return LoginResult{}, err
	}
	return result, nil
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
	if _, err = s.Users.FindUser(ctx, session.LoginID); errors.Is(err, ErrNotFound) {
		return Authenticated{}, ErrUnauthenticated
	} else if err != nil {
		return Authenticated{}, err
	}
	return Authenticated{LoginID: session.LoginID, CSRFToken: csrf, Session: session}, nil
}
func (s Auth) CheckCSRF(auth Authenticated, token string) bool {
	return token != "" && s.Tokens.EqualHash(token, auth.Session.CSRFTokenHash)
}
func (s Auth) Logout(ctx context.Context, auth Authenticated) error {
	return s.Repository.DeleteSession(ctx, auth.Session.TokenHash)
}
