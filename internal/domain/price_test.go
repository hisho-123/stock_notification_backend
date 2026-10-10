package domain

import (
	"testing"
	"time"
)

func price(t *testing.T, s string) Price {
	t.Helper()
	p, err := ParsePrice(s)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func TestPriceParsing(t *testing.T) {
	for input, want := range map[string]string{"00123.4500": "123.45", "1.2300e2": "123", "1e-8": "0.00000001", "0.00100": "0.001", "9007199254740993": "9007199254740993"} {
		p, err := ParsePrice(input)
		if err != nil || p.String() != want {
			t.Errorf("%s: got %s, %v", input, p.String(), err)
		}
	}
	for _, input := range []string{"", "0", "-1", "NaN", "Infinity", "1/2", "1e999999999999999999", "1e-16384"} {
		if _, err := ParsePrice(input); err == nil {
			t.Errorf("accepted %q", input)
		}
	}
}
func TestExactThresholdAndRounding(t *testing.T) {
	high := price(t, "100")
	for _, tc := range []struct {
		latest, wantRate string
		wantThreshold    int
	}{
		{"90.0001", "10.00", 0}, {"90", "10.00", 10}, {"73", "27.00", 25}, {"89.995", "10.01", 10},
		{"100", "0.00", 0}, {"0.0000000000000000001", "100.00", 30},
	} {
		p := price(t, tc.latest)
		if got := ReachedThreshold(high, p, []int{10, 15, 20, 25, 30}); got != tc.wantThreshold {
			t.Errorf("%s: threshold %d", tc.latest, got)
		}
		if got := DeclineRate(high, p); got != tc.wantRate {
			t.Errorf("%s: rate %s", tc.latest, got)
		}
	}
}
func TestHighAndNotificationState(t *testing.T) {
	last := 20
	a := Asset{HighestValue: price(t, "100"), LastNotifiedThreshold: &last}
	for _, p := range []string{"95", "80", "100"} {
		if got := a.ApplyFreshPrice(Quote{Price: price(t, p)}, []int{10, 20, 30}); got != 0 || *a.LastNotifiedThreshold != 20 {
			t.Fatalf("unexpected re-notification at %s", p)
		}
	}
	if a.ApplyFreshPrice(Quote{Price: price(t, "110")}, []int{10, 20, 30}) != 0 || a.LastNotifiedThreshold != nil {
		t.Fatal("high did not reset")
	}
	if a.ApplyFreshPrice(Quote{Price: price(t, "99")}, []int{10, 20, 30}) != 10 {
		t.Fatal("new high baseline not used")
	}
}
func TestFreshnessBoundary(t *testing.T) {
	now := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	for _, seconds := range []int{863999, 864000, 864001} {
		q := Quote{ProvidedAt: now.Add(-time.Duration(seconds) * time.Second), FetchedAt: now}
		if q.Stale() != (seconds >= 864000) {
			t.Errorf("wrong freshness at %d", seconds)
		}
	}
}
func TestNormalization(t *testing.T) {
	if got, err := NormalizeCode(" aapl "); err != nil || got != "AAPL" {
		t.Fatal(got, err)
	}
	if got, err := NormalizeCode("éa"); err != nil || got != "éA" {
		t.Fatal("non-ASCII case changed", got, err)
	}
	for _, s := range []string{"", "A B", "A\nB", "A\u00a0B"} {
		if _, err := NormalizeCode(s); err == nil {
			t.Errorf("accepted %q", s)
		}
	}
	for _, input := range [][]int{nil, {}, {10, 10}, {0}, {100}} {
		if _, err := NormalizeThresholds(input); err == nil {
			t.Errorf("accepted %v", input)
		}
	}
	values, err := NormalizeThresholds([]int{30, 10, 20})
	if err != nil || values[0] != 10 || values[2] != 30 {
		t.Fatal(values, err)
	}
}
