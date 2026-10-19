package usecase

import (
	"context"
	"errors"
	"time"

	"github.com/hisho-123/stock_notification_backend/internal/domain"
)

var (
	ErrNotFound                = errors.New("not found")
	ErrAlreadyExists           = errors.New("already exists")
	ErrUnavailable             = errors.New("service unavailable")
	ErrConnectionLost          = errors.New("database connection lost")
	ErrSymbolUnavailable       = errors.New("symbol unavailable")
	ErrPriceProvider           = errors.New("price provider error")
	ErrPriceTimeout            = errors.New("price provider timeout")
	ErrInvalidCredentials      = errors.New("invalid credentials")
	ErrUnauthenticated         = errors.New("unauthenticated")
	ErrCSRF                    = errors.New("csrf failed")
	ErrRegistrationUnavailable = errors.New("registration link unavailable")
	ErrLoginIDExists           = errors.New("login id already exists")
	ErrInvalidLINESignature    = errors.New("invalid LINE signature")
)

type ValidationError struct{ Fields map[string]string }

func (*ValidationError) Error() string { return "validation error" }
func Invalid(field, message string) error {
	return &ValidationError{Fields: map[string]string{field: message}}
}

type ListOptions struct{ Query, Sort, Order string }
type Target struct {
	ID      int64
	Code    string
	LoginID string
}

type AssetRepository interface {
	List(context.Context, ListOptions) ([]domain.Asset, error)
	ExistsCode(context.Context, string) (bool, error)
	Create(context.Context, domain.Asset) (domain.Asset, error)
	Get(context.Context, int64) (domain.Asset, error)
	SetMonitoring(context.Context, int64, bool, time.Time) (domain.Asset, error)
	Delete(context.Context, int64) error
	Targets(context.Context) ([]Target, error)
	SaveObservation(context.Context, domain.Asset) error
}
type SettingsRepository interface {
	GetSettings(context.Context) (domain.NotificationSettings, error)
	SaveSettings(context.Context, domain.NotificationSettings) (domain.NotificationSettings, error)
}
type SessionRepository interface {
	FindSession(context.Context, string) (domain.Session, error)
	ReplaceSession(context.Context, string, domain.Session) error
	DeleteSession(context.Context, string) error
	DeleteExpiredSessions(context.Context, time.Time) error
}
type Transaction interface {
	WithinTransaction(context.Context, func(AssetRepository) error) error
}
type PriceProvider interface {
	Fetch(context.Context, string) (domain.Quote, error)
}
type Notifier interface {
	Send(context.Context, string, string) error
}
type Clock interface{ Now() time.Time }
type PasswordVerifier interface{ Matches(string, string) bool }
type Tokens interface {
	Generate() (string, error)
	Hash(string) string
	EqualHash(string, string) bool
}
