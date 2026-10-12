package domain

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDecimalStorageLimits(t *testing.T) {
	for _, input := range []string{"1e131071", "1e-16383"} {
		if _, err := ParsePrice(input); err != nil {
			t.Fatal(input, err)
		}
	}
	for _, input := range []string{"1e131072", "1.1e-16383", strings.Repeat("1", 131073)} {
		if _, err := ParsePrice(input); !errors.Is(err, ErrInvalidPrice) {
			t.Fatal("accepted out-of-range price", err)
		}
	}
	defer func() {
		if recover() == nil {
			t.Error("invalid zero Price did not panic")
		}
	}()
	(Price{}).Compare(price(t, "1"))
}

func TestCodeAndNameBoundaries(t *testing.T) {
	for _, code := range []string{"\xff", strings.Repeat("界", 65), "A\x00B"} {
		if _, err := NormalizeCode(code); err == nil {
			t.Fatalf("accepted %q", code)
		}
	}
	code := strings.Repeat("界", 64)
	if got, err := NormalizeCode(code); err != nil || got != code {
		t.Fatal(got, err)
	}
	for _, tc := range []struct{ input, want string }{{" ", "AAPL"}, {" Apple ", "Apple"}, {strings.Repeat("界", 200), strings.Repeat("界", 200)}, {strings.Repeat("界", 201), strings.Repeat("界", 200)}} {
		if got := NormalizeName(tc.input, "AAPL"); got != tc.want {
			t.Fatal(got, tc.want)
		}
	}
}

func TestThresholdCollectionBoundaries(t *testing.T) {
	values := make([]int, 99)
	for i := range values {
		values[i] = 99 - i
	}
	got, err := NormalizeThresholds(values)
	if err != nil || got[0] != 1 || got[98] != 99 || values[0] != 99 {
		t.Fatal(got, values, err)
	}
	if _, err := NormalizeThresholds(append(values, 1)); err == nil {
		t.Fatal("accepted 100 thresholds")
	}
	if got := ReachedThreshold(price(t, "100"), price(t, "70"), []int{30, 10, 20}); got != 30 {
		t.Fatal(got)
	}
	if got := ReachedThreshold(price(t, "100"), price(t, "90"), nil); got != 0 {
		t.Fatal(got)
	}
}

func TestExactDeclineComparison(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{{"90", "80", -1}, {"80", "90", 1}, {"90", "90", 0}} {
		if got := CompareDecline(price(t, "100"), price(t, tc.a), price(t, "100"), price(t, tc.b)); got != tc.want {
			t.Fatal(got, tc)
		}
	}
}

func TestHigherThresholdAfterPreviousNotification(t *testing.T) {
	last := 20
	a := Asset{HighestValue: price(t, "100"), LastNotifiedThreshold: &last}
	if got := a.ApplyFreshPrice(Quote{Price: price(t, "75")}, []int{5, 15, 25}); got != 25 || !reflect.DeepEqual(a.LastNotifiedThreshold, &last) {
		t.Fatal(got, a)
	}
}

func TestSessionExpiration(t *testing.T) {
	now := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	s := Session{ExpiresAt: now}
	for _, delta := range []time.Duration{-time.Nanosecond, 0, time.Nanosecond} {
		if got := s.ValidAt(now.Add(delta)); got != (delta < 0) {
			t.Fatal(delta, got)
		}
	}
}
