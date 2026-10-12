package line

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPushReceiptAndNoRetry(t *testing.T) {
	for _, status := range []int{200, 202, 409, 429, 500} {
		calls := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer secret" || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("X-Line-Retry-Key") != "" {
				t.Error("wrong request")
			}
			var body struct {
				To       string
				Messages []struct{ Type, Text string }
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil || body.To != "user" || len(body.Messages) != 1 || body.Messages[0].Type != "text" || body.Messages[0].Text != "通知" {
				t.Error("wrong message")
			}
			w.WriteHeader(status)
		}))
		client := New("secret", "user", server.Client())
		client.Endpoint = server.URL
		err := client.Send(context.Background(), "通知")
		server.Close()
		if (err == nil) != (status == 200) || calls != 1 {
			t.Fatal(status, err, calls)
		}
	}
}
