package line

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
)

var ErrNotification = errors.New("line notification failed")

type Client struct {
	Token, Endpoint, ReplyEndpoint string
	HTTP                           *http.Client
}

func New(token string, client *http.Client) *Client {
	h := http.Client{}
	if client != nil {
		h = *client
	}
	if h.Timeout <= 0 || h.Timeout > 30*time.Second {
		h.Timeout = 30 * time.Second
	}
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{Token: token, ReplyEndpoint: "https://api.line.me/v2/bot/message/reply", Endpoint: "https://api.line.me/v2/bot/message/push", HTTP: &h}
}
func (c *Client) Send(ctx context.Context, userID, text string) error {
	// This payload contains only strings, so JSON encoding cannot fail.
	body, _ := json.Marshal(struct {
		To       string              `json:"to"`
		Messages []map[string]string `json:"messages"`
	}{To: userID, Messages: []map[string]string{{"type": "text", "text": text}}})
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, bytes.NewReader(body))
	if err != nil {
		return ErrNotification
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return ErrNotification
	}
	defer res.Body.Close()
	// Receipt, rather than delivery, is successful only for HTTP 200.
	if res.StatusCode != http.StatusOK {
		return ErrNotification
	}
	_, err = io.Copy(io.Discard, io.LimitReader(res.Body, 8192))
	if err != nil {
		return ErrNotification
	}
	return nil
}

func (c *Client) Reply(ctx context.Context, replyToken, text string) error {
	body, _ := json.Marshal(struct {
		ReplyToken string              `json:"replyToken"`
		Messages   []map[string]string `json:"messages"`
	}{replyToken, []map[string]string{{"type": "text", "text": text}}})
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.ReplyEndpoint, bytes.NewReader(body))
	if err != nil {
		return ErrNotification
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return ErrNotification
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return ErrNotification
	}
	return nil
}
