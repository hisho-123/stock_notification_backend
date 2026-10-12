package config

import (
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestCommandSpecificConfiguration(t *testing.T) {
	values := map[string]string{"DATABASE_URL": "secret-db-url", "FMP_API_KEY": "secret-api-key", "LINE_TOKEN": "token", "LINE_USER_ID": "user"}
	get := func(key string) string { return values[key] }
	if _, err := FromEnv("monitor", get); err != nil {
		t.Fatal(err)
	}
	if _, err := FromEnv("serve", get); err == nil {
		t.Fatal("missing auth allowed")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte("test"), 12)
	if err != nil {
		t.Fatal(err)
	}
	values["ADMIN_ID"], values["ADMIN_PASSWORD_HASH"], values["PUBLIC_ORIGIN"] = "owner", string(hash), "https://stock.example.com"
	c, err := FromEnv("serve", get)
	if err != nil || c.HTTPAddr != "127.0.0.1:8080" {
		t.Fatal(c, err)
	}
	for _, origin := range []string{"http://stock.example.com", "https://stock.example.com/", "https://user@stock.example.com", "https://stock.example.com?q=1"} {
		values["PUBLIC_ORIGIN"] = origin
		if _, err := FromEnv("serve", get); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatal("invalid origin accepted or secret leaked", err)
		}
	}
}

func TestServeRejectsInvalidPasswordHashes(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("test"), 12)
	if err != nil {
		t.Fatal(err)
	}
	valid := string(hash)
	tests := []struct {
		name string
		hash string
	}{
		{name: "truncated", hash: valid[:59]},
		{name: "extended", hash: valid + "A"},
		{name: "invalid salt", hash: valid[:7] + "!" + valid[8:]},
		{name: "invalid checksum", hash: valid[:29] + "!" + valid[30:]},
		{name: "invalid separator", hash: valid[:6] + "!" + valid[7:]},
		{name: "low cost", hash: strings.Replace(valid, "$12$", "$11$", 1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values := map[string]string{
				"DATABASE_URL":        "secret-db-url",
				"FMP_API_KEY":         "secret-api-key",
				"LINE_TOKEN":          "secret-line-token",
				"LINE_USER_ID":        "user",
				"ADMIN_ID":            "owner",
				"ADMIN_PASSWORD_HASH": tt.hash,
				"PUBLIC_ORIGIN":       "https://stock.example.com",
			}
			get := func(key string) string { return values[key] }
			_, err := FromEnv("serve", get)
			if err == nil {
				t.Fatal("invalid password hash accepted")
			}
			if !strings.Contains(err.Error(), "ADMIN_PASSWORD_HASH") {
				t.Fatalf("unexpected configuration error: %v", err)
			}
			if strings.Contains(err.Error(), tt.hash) || strings.Contains(err.Error(), "secret") {
				t.Fatal("configuration error leaked a secret")
			}
			if _, err := FromEnv("monitor", get); err != nil {
				t.Fatalf("monitor must not require an admin password hash: %v", err)
			}
		})
	}
}
