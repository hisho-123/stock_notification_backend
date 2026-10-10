package domain

import (
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
)

var ErrInvalidPrice = errors.New("invalid price")
var decimalPattern = regexp.MustCompile(`^([0-9]+)(?:\.([0-9]+))?(?:[eE]([+-]?[0-9]+))?$`)

// Price is an immutable, positive decimal. Its zero value is invalid.
// Precision is bounded only by PostgreSQL NUMERIC's supported range.
type Price struct{ text string }

func ParsePrice(s string) (Price, error) {
	m := decimalPattern.FindStringSubmatch(s)
	if m == nil {
		return Price{}, ErrInvalidPrice
	}
	exponent := 0
	if m[3] != "" {
		var err error
		exponent, err = strconv.Atoi(m[3])
		if err != nil || exponent > 131072 || exponent < -16383 {
			return Price{}, ErrInvalidPrice
		}
	}
	digits := strings.TrimLeft(m[1]+m[2], "0")
	if digits == "" {
		return Price{}, ErrInvalidPrice
	}
	scale := len(m[2]) - exponent
	if scale > 16383 || len(digits)-scale > 131072 {
		return Price{}, ErrInvalidPrice
	}
	var text string
	switch {
	case scale <= 0:
		text = digits + strings.Repeat("0", -scale)
	case scale >= len(digits):
		text = "0." + strings.Repeat("0", scale-len(digits)) + digits
	default:
		text = digits[:len(digits)-scale] + "." + digits[len(digits)-scale:]
	}
	if strings.Contains(text, ".") {
		text = strings.TrimRight(strings.TrimRight(text, "0"), ".")
	}
	return Price{text: text}, nil
}

func (p Price) String() string { return p.text }
func (p Price) Valid() bool    { return p.text != "" }
func (p Price) rat() *big.Rat {
	r, ok := new(big.Rat).SetString(p.text)
	if !ok {
		panic("invalid domain.Price")
	}
	return r
}
func (p Price) Compare(other Price) int { return p.rat().Cmp(other.rat()) }

// CompareDecline compares two drawdowns without dividing or rounding either ratio.
func CompareDecline(highA, latestA, highB, latestB Price) int {
	lhs := new(big.Rat).Mul(new(big.Rat).Sub(highA.rat(), latestA.rat()), highB.rat())
	rhs := new(big.Rat).Mul(new(big.Rat).Sub(highB.rat(), latestB.rat()), highA.rat())
	return lhs.Cmp(rhs)
}

// ReachedThreshold uses exact cross multiplication; display rounding is irrelevant.
func ReachedThreshold(high, latest Price, thresholds []int) int {
	difference := new(big.Rat).Sub(high.rat(), latest.rat())
	lhs := new(big.Rat).Mul(difference, big.NewRat(100, 1))
	reached := 0
	for _, t := range thresholds {
		rhs := new(big.Rat).Mul(high.rat(), big.NewRat(int64(t), 1))
		if lhs.Cmp(rhs) >= 0 && t > reached {
			reached = t
		}
	}
	return reached
}

func DeclineRate(high, latest Price) string {
	r := new(big.Rat).Sub(high.rat(), latest.rat())
	r.Quo(r, high.rat()).Mul(r, big.NewRat(10000, 1))
	q, rem := new(big.Int), new(big.Int)
	q.QuoRem(r.Num(), r.Denom(), rem)
	if new(big.Int).Mul(rem, big.NewInt(2)).Cmp(r.Denom()) >= 0 {
		q.Add(q, big.NewInt(1))
	}
	whole, fraction := new(big.Int), new(big.Int)
	whole.QuoRem(q, big.NewInt(100), fraction)
	return fmt.Sprintf("%s.%02d", whole.String(), fraction.Int64())
}
