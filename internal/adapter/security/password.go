package security

import (
	"encoding/base64"
	"regexp"

	"golang.org/x/crypto/bcrypt"
)

var (
	bcryptHashPattern = regexp.MustCompile(`^\$2[aby]?\$[0-9]{2}\$[./A-Za-z0-9]{53}$`)
	bcryptBase64      = base64.NewEncoding("./ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789").WithPadding(base64.NoPadding).Strict()
)

type Passwords struct{}

func (Passwords) Matches(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func ValidPasswordHash(hash string) bool {
	if !bcryptHashPattern.MatchString(hash) {
		return false
	}
	cost, err := bcrypt.Cost([]byte(hash))
	if err != nil || cost < 12 {
		return false
	}
	// The body is a 22-character salt and a 31-character checksum. Strict
	// decoding also rejects nonzero unused bits in their final characters.
	body := hash[len(hash)-53:]
	if _, err := bcryptBase64.DecodeString(body[:22]); err != nil {
		return false
	}
	_, err = bcryptBase64.DecodeString(body[22:])
	return err == nil
}
