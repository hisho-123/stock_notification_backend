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
	// Since Go 1.24, rand.Read fills the buffer or terminates the process.
	_, _ = rand.Read(bytes[:])
	return base64.RawURLEncoding.EncodeToString(bytes[:]), nil
}
func (Tokens) Hash(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}
func (t Tokens) EqualHash(token, hash string) bool {
	return subtle.ConstantTimeCompare([]byte(t.Hash(token)), []byte(hash)) == 1
}
