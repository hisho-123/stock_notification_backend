package security

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
)

type Tokens struct{}

func (Tokens) Generate() (string, error) {
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes[:]), nil
}
func (Tokens) Hash(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}
func (t Tokens) EqualHash(token, hash string) bool {
	return subtle.ConstantTimeCompare([]byte(t.Hash(token)), []byte(hash)) == 1
}
