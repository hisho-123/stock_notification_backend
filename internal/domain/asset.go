package domain

import (
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type AssetError string

const (
	PriceFetchFailed   AssetError = "PRICE_FETCH_FAILED"
	NotificationFailed AssetError = "NOTIFICATION_FAILED"
	PriceDataStale     AssetError = "PRICE_DATA_STALE"
)

type Asset struct {
	ID                        int64
	Code, Name                string
	HighestValue, LatestValue Price
	LatestFetchedAt           time.Time
	MonitoringEnabled         bool
	LastNotifiedThreshold     *int
	LastCheckedAt             time.Time
	LastErrorCode             *AssetError
	CreatedAt, UpdatedAt      time.Time
}

func NormalizeCode(input string) (string, error) {
	code := strings.TrimSpace(input)
	if !utf8.ValidString(code) || utf8.RuneCountInString(code) < 1 || utf8.RuneCountInString(code) > 64 {
		return "", errors.New("invalid code")
	}
	for _, r := range code {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return "", errors.New("invalid code")
		}
	}
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' {
			return r - ('a' - 'A')
		}
		return r
	}, code), nil
}

func NormalizeName(name, code string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return code
	}
	runes := []rune(name)
	if len(runes) > 200 {
		name = string(runes[:200])
	}
	return name
}

type Quote struct {
	Price                 Price
	Name                  string
	ProvidedAt, FetchedAt time.Time
}

func (q Quote) Stale() bool { return !q.ProvidedAt.After(q.FetchedAt.Add(-240 * time.Hour)) }

// ApplyFreshPrice updates the observed high and returns a threshold to notify, or 0.
func (a *Asset) ApplyFreshPrice(q Quote, thresholds []int) int {
	a.LatestValue, a.LatestFetchedAt, a.LastCheckedAt = q.Price, q.FetchedAt, q.FetchedAt
	if q.Price.Compare(a.HighestValue) > 0 {
		a.HighestValue, a.LastNotifiedThreshold = q.Price, nil
		return 0
	}
	reached := ReachedThreshold(a.HighestValue, q.Price, thresholds)
	if a.LastNotifiedThreshold != nil && reached <= *a.LastNotifiedThreshold {
		return 0
	}
	return reached
}
