package fmp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hisho-123/stock_notification_backend/internal/usecase"
)

func TestResponseValidation(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		want       error
	}{
		{"valid", `[{"symbol":"AAPL","name":" Apple ","price":1.234567890123456789e2,"timestamp":1791590400}]`, 200, nil},
		{"empty", `[]`, 200, usecase.ErrSymbolUnavailable},
		{"mismatch", `[{"symbol":"OTHER"}]`, 200, usecase.ErrSymbolUnavailable},
		{"null", `null`, 200, usecase.ErrPriceProvider},
		{"missing timestamp", `[{"symbol":"AAPL","price":1}]`, 200, usecase.ErrPriceProvider},
		{"null timestamp", `[{"symbol":"AAPL","price":1,"timestamp":null}]`, 200, usecase.ErrPriceProvider},
		{"fraction timestamp", `[{"symbol":"AAPL","price":1,"timestamp":1791590400.0}]`, 200, usecase.ErrPriceProvider},
		{"huge timestamp", `[{"symbol":"AAPL","price":1,"timestamp":9223372036854775807}]`, 200, usecase.ErrPriceProvider},
		{"zero", `[{"symbol":"AAPL","price":0,"timestamp":1791590400}]`, 200, usecase.ErrPriceProvider},
		{"string price", `[{"symbol":"AAPL","price":"1","timestamp":1791590400}]`, 200, usecase.ErrPriceProvider},
		{"provider rejection", `secret-response`, 403, usecase.ErrPriceProvider},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("symbol") != "AAPL" || r.URL.Query().Get("apikey") != "secret" {
					t.Error("wrong query")
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			client := New("secret", server.Client())
			client.Endpoint = server.URL
			quote, err := client.Fetch(context.Background(), "AAPL")
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			if err == nil && (quote.Price.String() != "123.4567890123456789" || quote.Name != "Apple" || quote.FetchedAt.IsZero()) {
				t.Fatal(quote)
			}
			if err != nil && strings.Contains(err.Error(), "secret") {
				t.Fatal("secret leaked")
			}
		})
	}
}
func TestTimeoutAndRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	client := New("secret", &http.Client{Timeout: 10 * time.Millisecond})
	client.Endpoint = server.URL
	if _, err := client.Fetch(context.Background(), "AAPL"); !errors.Is(err, usecase.ErrPriceTimeout) {
		t.Fatal(err)
	}
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, server.URL, 302) }))
	defer redirect.Close()
	client.Endpoint = redirect.URL
	if _, err := client.Fetch(context.Background(), "AAPL"); !errors.Is(err, usecase.ErrPriceProvider) {
		t.Fatal(err)
	}
}
