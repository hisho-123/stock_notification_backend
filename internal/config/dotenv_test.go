package config

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestLoadDotEnvOverridesWithoutChangingEnvironment(t *testing.T) {
	for _, command := range []string{"serve", "monitor"} {
		t.Run(command, func(t *testing.T) {
			t.Chdir(t.TempDir())
			values := validEnv()
			values["HTTP_ADDR"] = "127.0.0.1:9000"
			values["STATIC_DIR"] = "dotenv/dist"
			values["TRUSTED_PROXY_CIDRS"] = "10.0.0.0/8, 2001:db8::/32"
			var contents strings.Builder
			contents.WriteString("# Local application configuration\n\n")
			for key, value := range values {
				t.Setenv(key, "environment-"+key)
				fmt.Fprintf(&contents, "%s='%s'\n", key, value)
			}
			if err := os.WriteFile(".env", []byte(contents.String()), 0600); err != nil {
				t.Fatal(err)
			}
			got, err := Load(command)
			want, expectedErr := FromEnv(command, func(key string) string { return values[key] })
			if err != nil || expectedErr != nil || !reflect.DeepEqual(got, want) {
				t.Fatal(got, want, err, expectedErr)
			}
			for key := range values {
				if os.Getenv(key) != "environment-"+key {
					t.Fatalf("Load changed the process environment for %s", key)
				}
			}
		})
	}
}

func TestLoadDotEnvFallbacksAndEmptyValues(t *testing.T) {
	for _, tc := range []struct {
		name, contents string
		present        bool
		wantKey        string
		wantError      string
	}{
		{name: "missing file", wantKey: "secret-api-key"},
		{name: "missing items", present: true, contents: "FMP_API_KEY='dotenv-api-key'\n", wantKey: "dotenv-api-key"},
		{name: "empty required item", present: true, contents: "FMP_API_KEY=''\n", wantError: "FMP_API_KEY is required"},
		{name: "empty optional items", present: true, contents: "HTTP_ADDR=''\nSTATIC_DIR=''\nTRUSTED_PROXY_CIDRS=''\n", wantKey: "secret-api-key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			for key, value := range validEnv() {
				t.Setenv(key, value)
			}
			t.Setenv("HTTP_ADDR", "127.0.0.1:9000")
			t.Setenv("STATIC_DIR", "environment/dist")
			t.Setenv("TRUSTED_PROXY_CIDRS", "10.0.0.0/8")
			if tc.present {
				if err := os.WriteFile(".env", []byte(tc.contents), 0600); err != nil {
					t.Fatal(err)
				}
			}
			for _, command := range []string{"serve", "monitor"} {
				got, err := Load(command)
				if tc.wantError != "" {
					if err == nil || err.Error() != tc.wantError {
						t.Fatal(command, err)
					}
					continue
				}
				if err != nil || got.FMPAPIKey != tc.wantKey || got.DatabaseURL != "secret-db-url" || got.LINEToken != "secret-token" || got.AdminID != "owner" {
					t.Fatal(command, got, err)
				}
				if command == "serve" {
					if tc.name == "empty optional items" {
						if got.HTTPAddr != "127.0.0.1:8080" || got.StaticDir != "web/dist" || len(got.TrustedProxies) != 0 {
							t.Fatal("empty values did not override the environment", got)
						}
					} else if got.HTTPAddr != "127.0.0.1:9000" || got.StaticDir != "environment/dist" || len(got.TrustedProxies) != 1 {
						t.Fatal("missing values did not fall back to the environment", got)
					}
				}
			}
		})
	}
}

func TestLoadRejectsInvalidDotEnvWithoutLeakingSecrets(t *testing.T) {
	for _, tc := range []struct {
		name, contents string
		directory      bool
	}{
		{name: "invalid key", contents: "SECRET!KEY=secret-api-key\n"},
		{name: "unterminated quote", contents: "FMP_API_KEY='secret-api-key\n"},
		{name: "unreadable file", directory: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			for key, value := range validEnv() {
				t.Setenv(key, value)
			}
			if tc.directory {
				if err := os.Mkdir(".env", 0700); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(".env", []byte(tc.contents), 0600); err != nil {
				t.Fatal(err)
			}
			for _, command := range []string{"serve", "monitor"} {
				_, err := Load(command)
				if err == nil || !strings.Contains(err.Error(), ".env") || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "SECRET!KEY") {
					t.Fatal(command, "missing or unsafe configuration error", err)
				}
			}
		})
	}
}
