package security

import "golang.org/x/crypto/bcrypt"

type Passwords struct{}

func (Passwords) Matches(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
func ValidPasswordHash(hash string) bool {
	cost, err := bcrypt.Cost([]byte(hash))
	return err == nil && cost >= 12
}
