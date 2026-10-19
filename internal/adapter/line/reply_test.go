package line

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestReplyReceiptAndNoRetry(t *testing.T) {
	for _, status := range []int{200, 201, 202, 409, 429, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer fixture-channel-token" || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("X-Line-Retry-Key") != "" {
					t.Error("incorrect reply request headers")
				}
				var body map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				var token string
				var messages []struct{ Type, Text string }
				if len(body) != 2 || json.Unmarshal(body["replyToken"], &token) != nil || token != "fixture-reply-token" || json.Unmarshal(body["messages"], &messages) != nil || len(messages) != 1 || messages[0].Type != "text" || messages[0].Text != "24時間有効の専用登録URL" {
					t.Error("reply payload must use the event reply token and one text message")
				}
				w.WriteHeader(status)
				_, _ = w.Write([]byte("provider-private-response"))
			}))
			defer server.Close()
			client := New("fixture-channel-token", server.Client())
			client.ReplyEndpoint = server.URL
			err := client.Reply(context.Background(), "fixture-reply-token", "24時間有効の専用登録URL")
			// 受付後の再送は二重返信になるため、200だけを成功とし失敗でも1回で終える。
			if (err == nil) != (status == 200) || calls.Load() != 1 {
				t.Fatal(status, err, calls.Load())
			}
			if err != nil && (!errors.Is(err, ErrNotification) || strings.Contains(err.Error(), "provider-private-response")) {
				t.Fatal("provider details leaked", err)
			}
		})
	}
}

func TestReplyTransportFailureAndDeadline(t *testing.T) {
	for _, stage := range []string{"invalid endpoint", "network", "cancelled"} {
		t.Run(stage, func(t *testing.T) {
			calls := 0
			client := New("fixture-channel-token", &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				deadline, ok := r.Context().Deadline()
				remaining := time.Until(deadline)
				if !ok || remaining > 30*time.Second {
					t.Error("reply must have a deadline no later than 30 seconds")
				}
				if stage == "cancelled" {
					return nil, r.Context().Err()
				}
				return nil, errors.New("private transport details")
			})})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if stage == "invalid endpoint" {
				client.ReplyEndpoint = "%zz"
			}
			if stage == "cancelled" {
				cancel()
			}
			err := client.Reply(ctx, "fixture-reply-token", "登録URL")
			if !errors.Is(err, ErrNotification) || strings.Contains(err.Error(), "private transport details") {
				t.Fatal("reply failure must be a safe fixed error", err)
			}
			if stage == "invalid endpoint" && calls != 0 || stage == "network" && calls != 1 {
				t.Fatal("unexpected transport retries", calls)
			}
		})
	}
}

func TestReplyDoesNotFollowRedirect(t *testing.T) {
	var targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetCalls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	client := New("fixture-channel-token", redirect.Client())
	client.ReplyEndpoint = redirect.URL
	// 返信トークンとチャネル秘密情報を別の宛先へ転送しない。
	if err := client.Reply(context.Background(), "fixture-reply-token", "登録URL"); !errors.Is(err, ErrNotification) || targetCalls.Load() != 0 {
		t.Fatal(err, targetCalls.Load())
	}
}
