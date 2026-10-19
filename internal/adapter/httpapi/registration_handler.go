package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/hisho-123/stock_notification_backend/internal/usecase"
)

func (s *Server) registrationAllowed(w http.ResponseWriter, r *http.Request) bool {
	ip := s.clientIP(r)
	now := s.Clock.Now()
	if seconds := s.registrationLimiter.consume(ip, now); seconds > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(seconds))
		writeJSON(w, 429, struct {
			Error apiError `json:"error"`
		}{apiError{"TOO_MANY_ATTEMPTS", "時間をおいて再試行してください。", map[string]string{}}})
		return false
	}
	return true
}
func (s *Server) validateRegistration(w http.ResponseWriter, r *http.Request) {
	if !s.registrationAllowed(w, r) {
		return
	}
	fields, err := decodeObject(w, r, "registration_token")
	var token string
	if err == nil && json.Unmarshal(fields["registration_token"], &token) != nil {
		err = usecase.Invalid("registration_token", "文字列で指定してください。")
	}
	if err != nil {
		writeError(w, err)
		return
	}
	expires, err := s.Registration.Validate(r.Context(), token)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, struct {
		ExpiresAt string `json:"expires_at"`
	}{date(expires)})
}
func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	if !s.registrationAllowed(w, r) {
		return
	}
	fields, err := decodeObject(w, r, "registration_token", "login_id", "password")
	var token, id, password string
	if err == nil && (json.Unmarshal(fields["registration_token"], &token) != nil || json.Unmarshal(fields["login_id"], &id) != nil || json.Unmarshal(fields["password"], &password) != nil) {
		err = usecase.Invalid("body", "文字列で指定してください。")
	}
	if err != nil {
		writeError(w, err)
		return
	}
	result, err := s.Registration.Register(r.Context(), token, id, password, cookieValue(r, sessionCookie))
	if err != nil {
		writeError(w, err)
		return
	}
	setCookie(w, sessionCookie, result.Token, 86400)
	setCookie(w, csrfCookie, result.CSRFToken, 86400)
	writeJSON(w, http.StatusCreated, sessionResponse(result.Authenticated))
}
