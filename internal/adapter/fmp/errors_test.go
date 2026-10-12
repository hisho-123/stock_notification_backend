package fmp

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hisho-123/stock_notification_backend/internal/usecase"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestClientConfiguration(t *testing.T) {
	for _, timeout := range []time.Duration{0, -time.Second, time.Second, 30 * time.Second, 31 * time.Second} {
		t.Run(timeout.String(), func(t *testing.T) {
			original := &http.Client{Timeout: timeout}
			c := New("key", original)
			want := timeout
			if timeout <= 0 || timeout > 30*time.Second {
				want = 30 * time.Second
			}
			if c.HTTP == original || c.HTTP.Timeout != want || original.Timeout != timeout || !errors.Is(c.HTTP.CheckRedirect(nil, nil), http.ErrUseLastResponse) {
				t.Fatal(c.HTTP, original)
			}
		})
	}
	if c := New("key", nil); c.HTTP.Timeout != 30*time.Second || c.Endpoint != endpoint || c.Now == nil {
		t.Fatal(c)
	}
}

func TestProviderTransportAndBodyFailures(t *testing.T) {
	for _, tc := range []struct {
		name, endpoint     string
		body               io.Reader
		transportErr, want error
	}{
		{"invalid URL", "%zz", nil, nil, usecase.ErrPriceProvider},
		{"network failure", "https://example.invalid", nil, errors.New("secret-network-error"), usecase.ErrPriceProvider},
		{"network timeout", "https://example.invalid", nil, &net.DNSError{IsTimeout: true}, usecase.ErrPriceTimeout},
		{"body read failure", "https://example.invalid", brokenReader{}, nil, usecase.ErrPriceProvider},
		{"oversized body", "https://example.invalid", strings.NewReader(strings.Repeat("x", (1<<20)+1)), nil, usecase.ErrPriceProvider},
		{"malformed JSON", "https://example.invalid", strings.NewReader("[{"), nil, usecase.ErrPriceProvider},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			c := New("secret", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				if tc.transportErr != nil {
					return nil, tc.transportErr
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(tc.body), Header: make(http.Header)}, nil
			})})
			c.Endpoint = tc.endpoint
			_, err := c.Fetch(context.Background(), "AAPL")
			wantCalls := 1
			if tc.name == "invalid URL" {
				wantCalls = 0
			}
			if !errors.Is(err, tc.want) || calls != wantCalls || strings.Contains(err.Error(), "secret") {
				t.Fatal(err, calls)
			}
		})
	}
	for _, tc := range []struct{ err, want error }{{context.DeadlineExceeded, usecase.ErrPriceTimeout}, {&net.DNSError{IsTimeout: false}, usecase.ErrPriceProvider}, {errors.New("failure"), usecase.ErrPriceProvider}} {
		if got := providerError(tc.err); !errors.Is(got, tc.want) {
			t.Fatal(got, tc.want)
		}
	}
}

func TestQuoteTimestampAndFallbackName(t *testing.T) {
	now := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	c := New("key", &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Accept") != "application/json" {
			t.Error("missing Accept")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`[{"symbol":"OTHER"},{"symbol":"AAPL","name":42,"price":100,"timestamp":1791504000}]`))}, nil
	})})
	c.Now = func() time.Time { return now }
	q, err := c.Fetch(context.Background(), "AAPL")
	if err != nil || q.Name != "AAPL" || q.Price.String() != "100" || !q.FetchedAt.Equal(now) || !q.ProvidedAt.Equal(time.Unix(1791504000, 0)) {
		t.Fatal(q, err)
	}
}
