package security

import "testing"

func TestRandomTokensAndHashComparison(t *testing.T) {
	tokens := Tokens{}
	a, err := tokens.Generate()
	if err != nil {
		t.Fatal(err)
	}
	b, err := tokens.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if len(a) < 32 || a == b || len(tokens.Hash(a)) != 64 || !tokens.EqualHash(a, tokens.Hash(a)) || tokens.EqualHash(b, tokens.Hash(a)) {
		t.Fatal("invalid tokens")
	}
}
