package config

import (
	"reflect"
	"strings"
	"testing"

	"github.com/hisho-123/stock_notification_backend/internal/testfixture"
)

func validEnv() map[string]string {
	return map[string]string{
		"DATABASE_URL": "secret-db-url", "FMP_API_KEY": "secret-api-key", "LINE_TOKEN": "secret-token", "LINE_USER_ID": "user",
		"ADMIN_ID": "owner", "ADMIN_PASSWORD_HASH": testfixture.PasswordHash, "PUBLIC_ORIGIN": "https://stock.example.com",
	}
}

func TestRequiredEnvironmentVariables(t *testing.T) {
	for _, command := range []string{"serve", "monitor"} {
		keys := []string{"DATABASE_URL", "FMP_API_KEY"}
		if command == "serve" {
			keys = append(keys, "ADMIN_ID", "ADMIN_PASSWORD_HASH", "PUBLIC_ORIGIN")
		} else {
			keys = append(keys, "LINE_TOKEN", "LINE_USER_ID")
		}
		for _, key := range keys {
			t.Run(command+"/"+key, func(t *testing.T) {
				values := validEnv()
				values[key] = " \t"
				_, err := FromEnv(command, func(k string) string { return values[k] })
				if err == nil || err.Error() != key+" is required" {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestConfigurationBoundariesAndOverrides(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, tc := range []struct{ name, key, value string }{
		{"invalid ID UTF8", "ADMIN_ID", "\xff"}, {"long ID", "ADMIN_ID", strings.Repeat("界", 101)},
		{"bad address", "HTTP_ADDR", "localhost"}, {"bad proxies", "TRUSTED_PROXY_CIDRS", "10.0.0.0/8,bad"},
		{"bad URL", "PUBLIC_ORIGIN", "https://%zz"}, {"no host", "PUBLIC_ORIGIN", "https:"},
		{"empty hostname", "PUBLIC_ORIGIN", "https://:443"}, {"fragment", "PUBLIC_ORIGIN", "https://stock.example.com#x"},
		{"empty query", "PUBLIC_ORIGIN", "https://stock.example.com?"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values := validEnv()
			values[tc.key] = tc.value
			_, err := FromEnv("serve", func(k string) string { return values[k] })
			if err == nil || !strings.Contains(err.Error(), tc.key) || strings.Contains(err.Error(), "secret") {
				t.Fatal(err)
			}
		})
	}
	values := validEnv()
	values["HTTP_ADDR"] = "127.0.0.1:9000"
	values["STATIC_DIR"] = "/tmp/web"
	values["TRUSTED_PROXY_CIDRS"] = "10.0.0.0/8, 2001:db8::/32"
	c, err := FromEnv("serve", func(k string) string { return values[k] })
	if err != nil || c.HTTPAddr != values["HTTP_ADDR"] || c.StaticDir != "/tmp/web" || len(c.TrustedProxies) != 2 || c.TrustedProxies[1].String() != "2001:db8::/32" {
		t.Fatal(c, err)
	}
	values["ADMIN_ID"] = strings.Repeat("界", 100)
	if _, err := FromEnv("serve", func(k string) string { return values[k] }); err != nil {
		t.Fatal(err)
	}
	for key, value := range validEnv() {
		t.Setenv(key, value)
	}
	t.Setenv("HTTP_ADDR", "")
	t.Setenv("STATIC_DIR", "")
	t.Setenv("TRUSTED_PROXY_CIDRS", "")
	got, err := Load("serve")
	want, expectedErr := FromEnv("serve", func(k string) string { return validEnv()[k] })
	if err != nil || expectedErr != nil || !reflect.DeepEqual(got, want) || got.StaticDir != "web/dist" {
		t.Fatal(got, want, err)
	}
	if _, err := FromEnv("unknown", func(string) string { return "" }); err == nil || err.Error() != "unknown command" {
		t.Fatal(err)
	}
}
