package config

import (
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/hisho-123/stock_notification_backend/internal/adapter/security"
)

type Config struct {
	DatabaseURL, FMPAPIKey, LINEToken, LINEUserID                 string
	AdminID, AdminPasswordHash, PublicOrigin, HTTPAddr, StaticDir string
	TrustedProxies                                                []netip.Prefix
}

func Load(command string) (Config, error) { return FromEnv(command, os.Getenv) }
func FromEnv(command string, get func(string) string) (Config, error) {
	c := Config{DatabaseURL: get("DATABASE_URL"), FMPAPIKey: get("FMP_API_KEY"), LINEToken: get("LINE_TOKEN"), LINEUserID: get("LINE_USER_ID"), AdminID: get("ADMIN_ID"), AdminPasswordHash: get("ADMIN_PASSWORD_HASH"), PublicOrigin: get("PUBLIC_ORIGIN"), HTTPAddr: get("HTTP_ADDR"), StaticDir: get("STATIC_DIR")}
	if command != "serve" && command != "monitor" {
		return c, fmt.Errorf("unknown command")
	}
	required := map[string]string{"DATABASE_URL": c.DatabaseURL, "FMP_API_KEY": c.FMPAPIKey}
	if command == "serve" {
		required["ADMIN_ID"], required["ADMIN_PASSWORD_HASH"], required["PUBLIC_ORIGIN"] = c.AdminID, c.AdminPasswordHash, c.PublicOrigin
	} else {
		required["LINE_TOKEN"], required["LINE_USER_ID"] = c.LINEToken, c.LINEUserID
	}
	for key, value := range required {
		if strings.TrimSpace(value) == "" {
			return c, fmt.Errorf("%s is required", key)
		}
	}
	if command == "serve" {
		if !utf8.ValidString(c.AdminID) || utf8.RuneCountInString(c.AdminID) > 100 {
			return c, fmt.Errorf("ADMIN_ID is invalid")
		}
		if !security.ValidPasswordHash(c.AdminPasswordHash) {
			return c, fmt.Errorf("ADMIN_PASSWORD_HASH must be bcrypt with cost >= 12")
		}
		u, err := url.Parse(c.PublicOrigin)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
			return c, fmt.Errorf("PUBLIC_ORIGIN must be an HTTPS origin without path")
		}
		if c.HTTPAddr == "" {
			c.HTTPAddr = "127.0.0.1:8080"
		}
		if _, _, err := net.SplitHostPort(c.HTTPAddr); err != nil {
			return c, fmt.Errorf("HTTP_ADDR is invalid")
		}
		if c.StaticDir == "" {
			c.StaticDir = "web/dist"
		}
		if proxies := get("TRUSTED_PROXY_CIDRS"); proxies != "" {
			for _, value := range strings.Split(proxies, ",") {
				prefix, err := netip.ParsePrefix(strings.TrimSpace(value))
				if err != nil {
					return c, fmt.Errorf("TRUSTED_PROXY_CIDRS is invalid")
				}
				c.TrustedProxies = append(c.TrustedProxies, prefix)
			}
		}
	}
	return c, nil
}
