package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/hisho-123/stock_notification_backend/internal/usecase"
)

const sessionCookie = "__Host-stock_session"
const csrfCookie = "__Host-stock_csrf"

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	ip := s.clientIP(r)
	if seconds := s.limiter.retryAfter(ip, s.Clock.Now()); seconds > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(seconds))
		writeJSON(w, 429, struct {
			Error apiError `json:"error"`
		}{apiError{"TOO_MANY_ATTEMPTS", "時間をおいて再試行してください。", map[string]string{}}})
		return
	}
	fields, err := decodeObject(w, r, "login_id", "password")
	var id, password string
	if err == nil && (json.Unmarshal(fields["login_id"], &id) != nil || json.Unmarshal(fields["password"], &password) != nil) {
		err = usecase.Invalid("body", "IDとパスワードは文字列で指定してください。")
	}
	if err != nil {
		writeError(w, err)
		return
	}
	result, err := s.Auth.Login(r.Context(), id, password, cookieValue(r, sessionCookie))
	if err != nil {
		if errors.Is(err, usecase.ErrInvalidCredentials) {
			s.limiter.failed(ip, s.Clock.Now())
		}
		writeError(w, err)
		return
	}
	s.limiter.clear(ip)
	setCookie(w, sessionCookie, result.Token, 86400)
	setCookie(w, csrfCookie, result.CSRFToken, 86400)
	writeJSON(w, 200, sessionResponse(result.Authenticated))
}
func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, sessionResponse(authFrom(r)))
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if err := s.Auth.Logout(r.Context(), authFrom(r)); err != nil {
		writeError(w, err)
		return
	}
	setCookie(w, sessionCookie, "", -1)
	setCookie(w, csrfCookie, "", -1)
	w.WriteHeader(http.StatusNoContent)
}
func cookieValue(r *http.Request, name string) string {
	cookie, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	return cookie.Value
}
func setCookie(w http.ResponseWriter, name, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: maxAge})
}
