package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/hisho-123/stock_notification_backend/internal/domain"
	"github.com/hisho-123/stock_notification_backend/internal/usecase"
)

type assetDTO struct {
	ID                    string             `json:"id"`
	Code                  string             `json:"code"`
	Name                  string             `json:"name"`
	HighestValue          string             `json:"highest_value"`
	LatestValue           string             `json:"latest_value"`
	DeclineRate           string             `json:"decline_rate"`
	LatestFetchedAt       string             `json:"latest_fetched_at"`
	MonitoringEnabled     bool               `json:"monitoring_enabled"`
	LastNotifiedThreshold *int               `json:"last_notified_threshold"`
	LastCheckedAt         string             `json:"last_checked_at"`
	LastErrorCode         *domain.AssetError `json:"last_error_code"`
	CreatedAt             string             `json:"created_at"`
	UpdatedAt             string             `json:"updated_at"`
}

func date(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
func assetResponse(a domain.Asset) assetDTO {
	return assetDTO{ID: strconv.FormatInt(a.ID, 10), Code: a.Code, Name: a.Name, HighestValue: a.HighestValue.String(), LatestValue: a.LatestValue.String(), DeclineRate: domain.DeclineRate(a.HighestValue, a.LatestValue), LatestFetchedAt: date(a.LatestFetchedAt), MonitoringEnabled: a.MonitoringEnabled, LastNotifiedThreshold: a.LastNotifiedThreshold, LastCheckedAt: date(a.LastCheckedAt), LastErrorCode: a.LastErrorCode, CreatedAt: date(a.CreatedAt), UpdatedAt: date(a.UpdatedAt)}
}

type settingsDTO struct {
	Thresholds []int  `json:"thresholds"`
	UpdatedAt  string `json:"updated_at"`
}

func settingsResponse(s domain.NotificationSettings) settingsDTO {
	return settingsDTO{s.Thresholds, date(s.UpdatedAt)}
}

type sessionDTO struct {
	LoginID   string `json:"login_id"`
	ExpiresAt string `json:"expires_at"`
	CSRFToken string `json:"csrf_token"`
}

func sessionResponse(s usecase.Authenticated) sessionDTO {
	return sessionDTO{s.LoginID, date(s.Session.ExpiresAt), s.CSRFToken}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// decodeObject checks presence and null separately, including null array members.
func decodeObject(w http.ResponseWriter, r *http.Request, required ...string) (map[string]json.RawMessage, error) {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return nil, errMediaType
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil || !utf8.Valid(data) {
		return nil, usecase.Invalid("body", "JSONを確認してください。")
	}
	// Token decoding rejects duplicate keys as well as unknown properties.
	d := json.NewDecoder(bytes.NewReader(data))
	start, err := d.Token()
	if err != nil || start != json.Delim('{') {
		return nil, usecase.Invalid("body", "JSONオブジェクトを指定してください。")
	}
	result := make(map[string]json.RawMessage, len(required))
	allowed := make(map[string]bool, len(required))
	for _, key := range required {
		allowed[key] = true
	}
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return nil, usecase.Invalid("body", "JSONを確認してください。")
		}
		// The decoder only returns string tokens in an object's key position.
		key := token.(string)
		if !allowed[key] || result[key] != nil {
			return nil, usecase.Invalid("body", "未定義または重複した項目があります。")
		}
		var raw json.RawMessage
		if err := d.Decode(&raw); err != nil || string(raw) == "null" {
			return nil, usecase.Invalid(key, "入力内容を確認してください。")
		}
		result[key] = raw
	}
	if _, err := d.Token(); err != nil {
		return nil, usecase.Invalid("body", "JSONを確認してください。")
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, usecase.Invalid("body", "JSONを確認してください。")
	}
	for _, key := range required {
		if result[key] == nil {
			return nil, usecase.Invalid(key, "必須項目です。")
		}
	}
	return result, nil
}

func parseID(r *http.Request) (int64, error) {
	id := r.PathValue("id")
	if len(id) == 0 || id[0] < '1' || id[0] > '9' {
		return 0, usecase.Invalid("id", "IDを確認してください。")
	}
	for _, c := range id {
		if c < '0' || c > '9' {
			return 0, usecase.Invalid("id", "IDを確認してください。")
		}
	}
	value, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return 0, usecase.Invalid("id", "IDを確認してください。")
	}
	return value, nil
}
