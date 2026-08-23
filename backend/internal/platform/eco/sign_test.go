package eco

import (
	"strings"
	"testing"
)

// TestSignStringMatchesDocExample reproduces the canonical pre-sign string from
// the official docs "拼接示例" (field order = case-insensitive ascending).
func TestSignStringMatchesDocExample(t *testing.T) {
	params := map[string]any{
		"Timestamp":    "1706838763",
		"PartnerId":    "2487b5cadd774bf1b228c37e4a389bd1",
		"idempotentId": "564789",
		"purchasingInfoList": []map[string]any{{
			"commodityId":    422270,
			"commodityPrice": 50,
			"tradeLinks":     "https://steamcommunity.com/tradeoffer/new/?partner=12345678912&token=LBPW679",
		}},
	}
	got := SignString(params)
	want := `idempotentId=564789&PartnerId=2487b5cadd774bf1b228c37e4a389bd1&purchasingInfoList=[{"commodityId":422270,"commodityPrice":50,"tradeLinks":"https://steamcommunity.com/tradeoffer/new/?partner=12345678912&token=LBPW679"}]&Timestamp=1718434793`
	// Timestamp in the doc's signed example differs from body example; compare
	// everything except the timestamp literal.
	if !strings.HasPrefix(got, want[:len(want)-10]) {
		t.Fatalf("canonical mismatch:\n got=%s\nwant~%s", got, want)
	}
	if !strings.HasSuffix(got, "&Timestamp=1706838763") {
		t.Fatalf("timestamp segment wrong: %s", got)
	}
}

func TestSignStringSkipsEmptyAndNil(t *testing.T) {
	s := SignString(map[string]any{
		"A": "", "B": nil, "C": "x", "b2": 1,
	})
	if s != "b2=1&C=x" { // case-insensitive sort: b2 < c
		t.Fatalf("got %q", s)
	}
}

func TestFloatCanonical(t *testing.T) {
	if v := canonicalValue(50.0); v != "50" {
		t.Fatalf("float 50 -> %q", v)
	}
	if v := canonicalValue(1.5); v != "1.5" {
		t.Fatalf("float 1.5 -> %q", v)
	}
}

func TestSignVerifyRoundTrip(t *testing.T) {
	priv, pub := testKeyPair(t)
	sig, err := Sign(priv, "A=1&B=x")
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifySignature(pub, "A=1&B=x", sig); err != nil {
		t.Fatalf("verify failed: %v", err)
	}
	if err := VerifySignature(pub, "A=1&B=y", sig); err == nil {
		t.Fatal("tampered payload must fail verification")
	}
}
