package domain

import "time"

type Session struct {
	LoginID, TokenHash, CSRFTokenHash string
	CreatedAt, ExpiresAt              time.Time
}

func (s Session) ValidAt(now time.Time) bool { return s.ExpiresAt.After(now) }
