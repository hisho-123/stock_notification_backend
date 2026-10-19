package config

import (
	"strings"
	"testing"
)

func TestCommandSpecificConfiguration(t *testing.T) {
	values := map[string]string{"DATABASE_URL": "secret-db-url", "FMP_API_KEY": "secret-api-key", "LINE_TOKEN": "token"}
	get := func(key string) string { return values[key] }
	if _, err := FromEnv("monitor", get); err != nil {
		t.Fatal(err)
	}
	if _, err := FromEnv("serve", get); err == nil {
		t.Fatal("missing webhook configuration accepted")
	}
	values["LINE_CHANNEL_SECRET"], values["PUBLIC_ORIGIN"] = "channel-secret", "https://stock.example.com"
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
func TestLegacyAdministratorSettingsAreUnused(t *testing.T) {
	values := validEnv()
	values["ADMIN_ID"] = ""
	values["ADMIN_PASSWORD_HASH"] = "invalid"
	values["LINE_USER_ID"] = ""
	for _, cmd := range []string{"serve", "monitor"} {
		if _, err := FromEnv(cmd, func(k string) string { return values[k] }); err != nil {
			t.Fatal(err)
		}
	}
}
