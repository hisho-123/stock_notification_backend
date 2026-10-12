package security

import (
	"strings"
	"testing"

	"github.com/hisho-123/stock_notification_backend/internal/testfixture"
)

func TestValidPasswordHash(t *testing.T) {
	hash := testfixture.PasswordHash
	valid := hash
	tests := []struct {
		name string
		hash string
		want bool
	}{
		{name: "2a", hash: valid, want: true},
		{name: "2b", hash: strings.Replace(valid, "$2a$", "$2b$", 1), want: true},
		{name: "2y", hash: strings.Replace(valid, "$2a$", "$2y$", 1), want: true},
		{name: "legacy 2", hash: strings.Replace(valid, "$2a$", "$2$", 1), want: true},
		{name: "maximum cost", hash: strings.Replace(valid, "$12$", "$31$", 1), want: true},
		{name: "empty"},
		{name: "truncated", hash: valid[:58]},
		{name: "missing last checksum byte", hash: valid[:59]},
		{name: "extra checksum byte", hash: valid + "A"},
		{name: "leading whitespace", hash: " " + valid},
		{name: "trailing whitespace", hash: valid + " "},
		{name: "old major version", hash: strings.Replace(valid, "$2a$", "$1a$", 1)},
		{name: "new major version", hash: strings.Replace(valid, "$2a$", "$3a$", 1)},
		{name: "unsupported minor version", hash: strings.Replace(valid, "$2a$", "$2x$", 1)},
		{name: "unknown minor version", hash: strings.Replace(valid, "$2a$", "$2z$", 1)},
		{name: "invalid prefix", hash: "!" + valid[1:]},
		{name: "invalid version separator", hash: valid[:3] + "!" + valid[4:]},
		{name: "invalid cost separator", hash: valid[:6] + "!" + valid[7:]},
		{name: "nonnumeric cost", hash: strings.Replace(valid, "$12$", "$1x$", 1)},
		{name: "cost below minimum", hash: strings.Replace(valid, "$12$", "$11$", 1)},
		{name: "cost above maximum", hash: strings.Replace(valid, "$12$", "$32$", 1)},
		{name: "invalid salt character", hash: valid[:7] + "!" + valid[8:]},
		{name: "invalid checksum character", hash: valid[:29] + "!" + valid[30:]},
		{name: "padded salt", hash: valid[:28] + "=" + valid[29:]},
		{name: "padded checksum", hash: valid[:59] + "="},
		{name: "salt newline", hash: valid[:7] + "\n" + valid[8:]},
		{name: "checksum carriage return", hash: valid[:29] + "\r" + valid[30:]},
		{name: "nonzero salt padding bits", hash: valid[:28] + "B" + valid[29:]},
		{name: "nonzero checksum padding bits", hash: valid[:59] + "B"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ValidPasswordHash(tt.hash); got != tt.want {
				t.Fatalf("ValidPasswordHash() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPasswordMatching(t *testing.T) {
	for _, tc := range []struct {
		name, hash, password string
		want                 bool
	}{
		{"correct", testfixture.PasswordHash, testfixture.Password, true},
		{"incorrect", testfixture.PasswordHash, "wrong", false},
		{"invalid hash", "invalid", testfixture.Password, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := (Passwords{}).Matches(tc.hash, tc.password); got != tc.want {
				t.Fatal(got, tc.want)
			}
		})
	}
}
