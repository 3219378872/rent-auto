package analytics

import (
	"testing"
	"time"
)

func TestAnnualizedROI(t *testing.T) {
	now := time.Date(2026, 8, 23, 0, 0, 0, 0, time.UTC)
	first := now.AddDate(0, 0, -100)

	// net=500, cost=10000, 100d → (0.05)*(365/100)=18.25%
	got := AnnualizedROI(500, 10000, first, now)
	if got != 0.1825 {
		t.Fatalf("roi=%v want 0.1825", got)
	}

	// day-one floor: days<1 treated as 1
	got = AnnualizedROI(10, 1000, now.Add(-time.Hour), now)
	if got != mathRound4(0.01*365) {
		t.Fatalf("day1 roi=%v", got)
	}

	// degenerate inputs
	if AnnualizedROI(100, 0, first, now) != 0 {
		t.Fatal("zero cost must be 0")
	}
	neg := AnnualizedROI(-50, 1000, first, now)
	if neg != -0.1825 { // finite negatives are legitimate
		t.Fatalf("negative roi=%v", neg)
	}
}

func mathRound4(v float64) float64 { return float64(int64(v*10000+0.5)) / 10000 }

func TestRound2(t *testing.T) {
	if Round2(1.005) != 1.0 || Round2(1.006) != 1.01 || Round2(-1.005) != -1.0 {
		t.Fatalf("round semantics: %v %v %v", Round2(1.005), Round2(1.006), Round2(-1.005))
	}
}
