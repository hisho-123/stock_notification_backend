package httpapi

import (
	"context"
	"io"
	"math"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/hisho-123/stock_notification_backend/internal/usecase"
)

type authKey struct{}

func authFrom(r *http.Request) usecase.Authenticated {
	auth, _ := r.Context().Value(authKey{}).(usecase.Authenticated)
	return auth
}

func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		modifies := r.Method == "POST" || r.Method == "PATCH" || r.Method == "PUT" || r.Method == "DELETE"
		if modifies && (len(r.Header.Values("Origin")) != 1 || r.Header.Get("Origin") != s.PublicOrigin) {
			writeError(w, usecase.ErrCSRF)
			return
		}
		if r.URL.Path != "/api/v1/auth/login" || r.Method != http.MethodPost {
			auth, err := s.Auth.Authenticate(r.Context(), cookieValue(r, sessionCookie), cookieValue(r, csrfCookie))
			if err != nil {
				writeError(w, err)
				return
			}
			if modifies && (len(r.Header.Values("X-CSRF-Token")) != 1 || !s.Auth.CheckCSRF(auth, r.Header.Get("X-CSRF-Token"))) {
				writeError(w, usecase.ErrCSRF)
				return
			}
			r = r.WithContext(context.WithValue(r.Context(), authKey{}, auth))
		}
		query, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			writeError(w, usecase.Invalid("query", "クエリを確認してください。"))
			return
		}
		for key, values := range query {
			allowed := r.Method == http.MethodGet && r.URL.Path == "/api/v1/assets" && (key == "q" || key == "sort" || key == "order")
			if !allowed || len(values) != 1 {
				writeError(w, usecase.Invalid("query", "未定義または重複したクエリです。"))
				return
			}
			if (key == "sort" || key == "order") && values[0] == "" {
				writeError(w, usecase.Invalid(key, "値を指定してください。"))
				return
			}
		}
		if r.Method == http.MethodDelete || r.Method == http.MethodPost && r.URL.Path == "/api/v1/auth/logout" {
			data, err := io.ReadAll(io.LimitReader(r.Body, 1))
			if err != nil {
				writeError(w, usecase.Invalid("body", "要求本文を確認してください。"))
				return
			}
			if len(data) > 0 {
				media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
				if err != nil || media != "application/json" {
					writeError(w, errMediaType)
					return
				}
				writeError(w, usecase.Invalid("body", "要求本文は指定できません。"))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
func (s *Server) recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recover() != nil {
				writeError(w, context.Canceled)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type responseWriter struct{ http.ResponseWriter }

func (w responseWriter) WriteHeader(status int) {
	if status == http.StatusServiceUnavailable && strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
	}
	w.ResponseWriter.WriteHeader(status)
}
func responseHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(responseWriter{w}, r)
	})
}

// Forwarded addresses are used only through an explicitly trusted proxy chain.
func (s *Server) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	remote, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	remote = remote.Unmap()
	trusted := func(addr netip.Addr) bool {
		for _, prefix := range s.TrustedProxies {
			if prefix.Contains(addr) {
				return true
			}
		}
		return false
	}
	if !trusted(remote) {
		return remote.String()
	}
	parts := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(parts) - 1; i >= 0; i-- {
		addr, err := netip.ParseAddr(strings.TrimSpace(parts[i]))
		if err != nil {
			return remote.String()
		}
		remote = addr.Unmap()
		if !trusted(remote) {
			break
		}
	}
	return remote.String()
}

type attemptLimiter struct {
	mu       sync.Mutex
	failures map[string][]time.Time
}

func (l *attemptLimiter) prune(now time.Time) {
	cutoff := now.Add(-15 * time.Minute)
	for ip, failures := range l.failures {
		index := 0
		for index < len(failures) && !failures[index].After(cutoff) {
			index++
		}
		if index == len(failures) {
			delete(l.failures, ip)
		} else {
			l.failures[ip] = failures[index:]
		}
	}
}
func (l *attemptLimiter) retryAfter(ip string, now time.Time) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prune(now)
	failures := l.failures[ip]
	if len(failures) < 5 {
		return 0
	}
	return int(math.Ceil(failures[0].Add(15 * time.Minute).Sub(now).Seconds()))
}
func (l *attemptLimiter) failed(ip string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prune(now)
	l.failures[ip] = append(l.failures[ip], now)
}
func (l *attemptLimiter) clear(ip string) { l.mu.Lock(); defer l.mu.Unlock(); delete(l.failures, ip) }
