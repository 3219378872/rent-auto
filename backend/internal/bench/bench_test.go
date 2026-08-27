package bench

import (
	"math"
	"testing"
)

func TestMedian(t *testing.T) {
	cases := []struct {
		in   []float64
		want float64
	}{
		{[]float64{}, 0},
		{[]float64{5}, 5},
		{[]float64{1, 3}, 2},
		{[]float64{1, 9, 2}, 2},
		{[]float64{10, 30, 20, 40}, 25},
	}
	for _, c := range cases {
		if got := Median(c.in); got != c.want {
			t.Fatalf("Median(%v)=%v want %v", c.in, got, c.want)
		}
	}
}

func TestRound2(t *testing.T) {
	cases := []struct {
		in, want float64
	}{{1.004, 1.0}, // NOTE: 1.005/-1.005 are sub-representable in binary floats and round toward zero
		{1.006, 1.01}, {2.675, 2.68}, {1.005, 1.0}, {-1.005, -1.0}, {0, 0}}
	for _, c := range cases {
		if got := Round2(c.in); got != c.want {
			t.Fatalf("Round2(%v)=%v want %v", c.in, got, c.want)
		}
	}
	// Non-finite market data must collapse to 0, never convert to an
	// implementation-defined int64 (the pre-round10 local copy lost this guard).
	for _, nan := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if got := Round2(nan); got != 0 {
			t.Fatalf("Round2(%v)=%v want 0", nan, got)
		}
	}
}

// pricePtr feeds raw upstream prices into anchors; NaN/Inf must read as absent.
func TestPricePtrNonFinite(t *testing.T) {
	for _, v := range []float64{math.NaN(), math.Inf(1), 0, -3} {
		if pricePtr(v) != nil {
			t.Fatalf("pricePtr(%v) should be nil", v)
		}
	}
	if p := pricePtr(12.345); p == nil || *p != 12.35 {
		t.Fatalf("pricePtr(12.345)=%v", p)
	}
}

// anchor math contract: V = median of the non-null pair (pricing-spec §1)
func TestAnchorContract(t *testing.T) {
	var uu, eco *float64
	p := func(v float64) *float64 { return &v }

	if Median(nil) != 0 {
		t.Fatal("empty median")
	}
	uu, eco = p(100), p(110)
	if got := Median([]float64{*uu, *eco}); got != 105 {
		t.Fatalf("both present: %v", got)
	}
	if got := Median([]float64{*uu}); got != 100 { // eco missing → single-value median
		t.Fatalf("single: %v", got)
	}
}
