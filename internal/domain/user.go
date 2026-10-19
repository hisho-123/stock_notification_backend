package domain

import "time"

type User struct {
	LoginID, PasswordHash, LINEUserID string
	LINEFollowing                     bool
	CreatedAt, UpdatedAt              time.Time
}

type RegistrationToken struct {
	TokenHash, LINEUserID string
	CreatedAt, ExpiresAt  time.Time
	ConsumedAt, RevokedAt *time.Time
}

type LINEEvent struct {
	ID, LINEUserID, Type, ReplyToken string
	Timestamp                        int64
}

type LINEReply struct{ Text string }
