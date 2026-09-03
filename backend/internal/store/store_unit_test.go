package store

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/domain"
)

func TestLoadMigrations(t *testing.T) {
	migs, err := LoadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(migs) == 0 {
		t.Fatal("no migrations loaded")
	}
	prev := ""
	for i, m := range migs {
		if m.Version < prev {
			t.Fatalf("migrations not sorted at index %d", i)
		}
		prev = m.Version
		if !strings.Contains(m.UpSQL, "CREATE TABLE") && !strings.Contains(m.UpSQL, "CREATE INDEX") && !strings.Contains(m.UpSQL, "CREATE UNIQUE INDEX") && !strings.Contains(m.UpSQL, "ALTER TABLE") {
			t.Fatalf("migration %s up sql looks empty", m.Version)
		}
		if m.DownSQL == "" {
			t.Fatalf("migration %s missing down", m.Version)
		}
	}
}

func TestLoadMigrationsRejectsOrphanDown(t *testing.T) {
	// Regression guard for the 2026-08-23 lost-migration incident:
	// a down without its up pair must fail loudly instead of being skipped.
	ups := map[string]string{"0001_x": ""}
	downs := map[string]string{"0001_x": "", "0002_y": ""}
	if err := validatePairMap(ups, downs); err == nil {
		t.Fatal("orphan down must be rejected")
	}
	if err := validatePairMap(map[string]string{"0001_x": ""}, map[string]string{"0001_x": ""}); err != nil {
		t.Fatalf("healthy pair rejected: %v", err)
	}
}

func TestRound2MoneySemantics(t *testing.T) {
	// Mirrors pricing.Round2 without importing pricing (dependency direction).
	if round2Money(1.005) != 1.0 || round2Money(1.006) != 1.01 || round2Money(-1.005) != -1.0 {
		t.Fatalf("half-away: %v %v %v", round2Money(1.005), round2Money(1.006), round2Money(-1.005))
	}
	for _, bad := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), 1e15, -1e15} {
		if round2Money(bad) != 0 {
			t.Fatalf("round2Money(%v) must collapse to 0", bad)
		}
	}
}

func TestRound2MoneyPtr(t *testing.T) {
	if round2MoneyPtr(nil) != nil {
		t.Fatal("nil must stay nil")
	}
	p := 1.005
	q := round2MoneyPtr(&p)
	if q != &p || p != 1.0 {
		t.Fatalf("in-place rounding failed: %v", p)
	}
}

func TestWalletFlowRefUnique(t *testing.T) {
	now := time.Now()
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		// Same channel AND same timestamp: the old second-precision format
		// collided here and dropped wallet history rows.
		r := walletFlowRef(domain.ChannelUU, now)
		if !strings.HasPrefix(r, "wallet-uu-") {
			t.Fatalf("ref format: %q", r)
		}
		if seen[r] {
			t.Fatalf("duplicate wallet ref: %q", r)
		}
		seen[r] = true
	}
}
