package line

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestClientConfigurationAndFailures(t *testing.T) {
	for _, timeout := range []time.Duration{0, time.Second, 30 * time.Second, 31 * time.Second} {
		original := &http.Client{Timeout: timeout}
		c := New("token", "user", original)
		want := timeout
		if timeout <= 0 || timeout > 30*time.Second {
			want = 30 * time.Second
		}
		if c.HTTP == original || c.HTTP.Timeout != want || original.Timeout != timeout {
			t.Fatal(c.HTTP, original)
		}
	}
	if c := New("token", "user", nil); c.HTTP.Timeout != 30*time.Second || c.Endpoint != "https://api.line.me/v2/bot/message/push" {
		t.Fatal(c)
	}
	for _, name := range []string{"invalid URL", "network", "body read"} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			c := New("secret", "user", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				if name == "network" {
					return nil, errors.New("secret")
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(brokenReader{})}, nil
			})})
			if name == "invalid URL" {
				c.Endpoint = "%zz"
			}
			err := c.Send(context.Background(), "通知")
			wantCalls := 1
			if name == "invalid URL" {
				wantCalls = 0
			}
			if !errors.Is(err, ErrNotification) || calls != wantCalls {
				t.Fatal(err, calls)
			}
		})
	}
}

func TestRedirectIsNotFollowed(t *testing.T) {
	targetCalls := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalls++; w.WriteHeader(200) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer redirect.Close()
	c := New("token", "user", redirect.Client())
	c.Endpoint = redirect.URL
	if err := c.Send(context.Background(), "通知"); !errors.Is(err, ErrNotification) || targetCalls != 0 {
		t.Fatal(err, targetCalls)
	}
}
