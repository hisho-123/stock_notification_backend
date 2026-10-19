package httpapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/hisho-123/stock_notification_backend/internal/domain"
	"github.com/hisho-123/stock_notification_backend/internal/usecase"
)

var lineIDPattern = regexp.MustCompile(`^U[0-9a-f]{32}$`)

type lineSource struct {
	Type   string `json:"type"`
	UserID string `json:"userId"`
}
type lineEvent struct {
	Type       string      `json:"type"`
	ID         string      `json:"webhookEventId"`
	Timestamp  *int64      `json:"timestamp"`
	ReplyToken string      `json:"replyToken"`
	Source     *lineSource `json:"source"`
	Message    *struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"message"`
}

func (s *Server) lineWebhook(w http.ResponseWriter, r *http.Request) {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		writeError(w, errMediaType)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeError(w, usecase.Invalid("body", "JSONを確認してください。"))
		return
	}
	signature, err := base64.StdEncoding.DecodeString(r.Header.Get("X-Line-Signature"))
	mac := hmac.New(sha256.New, []byte(s.LINEChannelSecret))
	mac.Write(body)
	if s.LINEChannelSecret == "" || len(r.Header.Values("X-Line-Signature")) != 1 || err != nil || !hmac.Equal(signature, mac.Sum(nil)) {
		writeError(w, usecase.ErrInvalidLINESignature)
		return
	}
	var payload struct {
		Destination *string      `json:"destination"`
		Events      *[]lineEvent `json:"events"`
	}
	if !utf8.Valid(body) || json.Unmarshal(body, &payload) != nil || payload.Events == nil || payload.Destination == nil {
		writeError(w, usecase.Invalid("body", "Webhookの構造を確認してください。"))
		return
	}
	events := make([]domain.LINEEvent, 0, len(*payload.Events))
	// 全イベントを検証してから業務処理し、後半の構造不正で部分更新しない。
	for _, e := range *payload.Events {
		if e.Type == "" || e.Source != nil && e.Source.Type == "" || e.Message != nil && e.Message.Type == "" {
			writeError(w, usecase.Invalid("events", "イベントの必須項目を確認してください。"))
			return
		}
		if e.Source == nil || e.Source.Type != "user" || e.Source.UserID == "" {
			continue
		}
		if !lineIDPattern.MatchString(e.Source.UserID) {
			writeError(w, usecase.Invalid("events", "LINEユーザーIDを確認してください。"))
			return
		}
		kind := e.Type
		if kind == "message" {
			if e.Message == nil || e.Message.Type == "" {
				writeError(w, usecase.Invalid("events", "メッセージを確認してください。"))
				return
			}
			if e.Message.Type != "text" || strings.TrimSpace(e.Message.Text) != "登録" {
				continue
			}
			kind = "registration"
		}
		if kind != "follow" && kind != "unfollow" && kind != "registration" {
			continue
		}
		if e.ID == "" || utf8.RuneCountInString(e.ID) > 128 || e.Timestamp == nil || *e.Timestamp < 0 || kind != "unfollow" && e.ReplyToken == "" {
			writeError(w, usecase.Invalid("events", "返信トークンを確認してください。"))
			return
		}
		events = append(events, domain.LINEEvent{ID: e.ID, LINEUserID: e.Source.UserID, Type: kind, Timestamp: *e.Timestamp, ReplyToken: e.ReplyToken})
	}
	if err = s.Webhook.Handle(r.Context(), events); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}
