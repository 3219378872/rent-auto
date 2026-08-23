package pricing

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/domain"
)

func q(short, long, dep float64) Quote { return Quote{Short: short, Long: long, Deposit: dep} }

// TestBaselinePortedFromSteamauto replicates the upstream formula on a
// hand-computed fixture (see evidence/m4-pricing-engine.md for the arithmetic).
func TestBaselinePortedFromSteamauto(t *testing.T) {
	quotes := []Quote{
		q(2.0, 1.8, 200), q(2.2, 2.0, 210), q(2.5, 2.2, 220), q(3.0, 0, 260),
	}
	p := DefaultParams().Baseline
	b, ok := Baseline(quotes, p, 100)
	if !ok {
		t.Fatal("baseline should exist")
	}
	meanShort := (2.0 + 2.2 + 2.5) / 3 // only first min(10,n)=4 entries but #4 has short=3.0 → included!
	_ = meanShort
	// shorts = [2.0,2.2,2.5,3.0] → mean=2.425 ×0.97=2.35225 → floor max(2.0)=… clamp lo shorts[0]=2.0
	wantShort := Round2(2.425 * 0.97) // 2.35
	if b.Short != wantShort {
		t.Fatalf("short=%v want %v", b.Short, wantShort)
	}
	// longs=[1.8,2.0,2.2] mean=2.0×0.95=1.9 ; short×0.98=2.30345→min=1.9; floor longs[0]=1.8 → 1.9
	wantLong := Round2(math.Min(b.Short*0.98, 1.9))
	if b.Long != wantLong || b.Long > b.Short {
		t.Fatalf("long=%v want≤%v", b.Long, wantLong)
	}
	// shorts window covers all 4 quotes → deposits=[200,210,220,260]
	// mean=222.5 ×0.98=218.05 vs min 200 → 218.05
	if b.Deposit != 218.05 {
		t.Fatalf("dep=%v want 218.05", b.Deposit)
	}
}

func TestBaselineEdgeCases(t *testing.T) {
	p := DefaultParams().Baseline

	if _, ok := Baseline(nil, p, 100); ok {
		t.Fatal("empty quotes must yield no baseline")
	}
	if _, ok := Baseline([]Quote{q(0, 0, 0)}, p, 100); ok {
		t.Fatal("all-zero quotes must yield no baseline")
	}

	// no longs → long = short−0.01
	b, _ := Baseline([]Quote{q(1.0, 0, 50)}, p, 100)
	if b.Long != 0.99 || b.Long > b.Short {
		t.Fatalf("no-longs: %+v", b)
	}

	// single quote: short floor at first element dominates
	b, _ = Baseline([]Quote{q(10, 9, 500)}, p, 100)
	if b.Short != 10 { // 10×0.97=9.7 clamped to floor shorts[0]=10
		t.Fatalf("single-quote short=%v", b.Short)
	}

	// MinLeaseRatio floor with V
	p2 := p
	p2.MinLeaseRatio = 0.01 // 1% of V per day
	b, _ = Baseline([]Quote{q(0.5, 0, 30)}, p2, 100)
	if b.Short < 1.0 {
		t.Fatalf("ratio floor: %v", b.Short)
	}
}

func TestNextFactorBoundsAndEvents(t *testing.T) {
	cp := DefaultParams().Ctrl

	f, why := NextFactor(1.0, EventRentSuccess, cp)
	if f != 1.03 || why == "" {
		t.Fatalf("up: %v %q", f, why)
	}

	f, _ = NextFactor(cp.FMax+0.5, EventRentSuccess, cp)
	if f != cp.FMax {
		t.Fatalf("max clamp: %v", f)
	}

	f, _ = NextFactor(cp.FMin-0.5, EventStaleDay, cp)
	if f != cp.FMin {
		t.Fatalf("min clamp: %v", f)
	}

	f, _ = NextFactor(1.0, EventBoughtOut, cp)
	if f != 1.06 {
		t.Fatalf("bought out: %v", f)
	}

	if f, _ := NextFactor(1.2, EventReset, cp); f != 1.0 {
		t.Fatalf("reset: %v", f)
	}
}

func baseInput() Input {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	return Input{
		Channel: domain.ChannelUU, HasV: true, V: 100,
		Base: Base{Short: 2.0, Long: 1.9, Deposit: 40}, HasBase: true,
		Factor: 1.0, P: DefaultParams(), Now: now,
		RentMaxDayMin: 1, RentMaxDayMax: 90,
	}
}

func TestDecideUUHappyPath(t *testing.T) {
	d := Decide(baseInput())
	if !d.OK || d.Rent != 2.0 || d.MaxDays != 60 {
		t.Fatalf("decision: %+v", d)
	}
	// deposit = max(base 40, floor 0.3×100=30) = 40
	if d.Deposit != 40 {
		t.Fatalf("deposit %v", d.Deposit)
	}
	if d.Long <= 0 || d.Long > d.Rent {
		t.Fatalf("long %v", d.Long)
	}
}

func TestDecideGuards(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Input)
		wantSK string
	}{
		{"no_anchor", func(i *Input) { i.HasV = false }, "no_value_anchor"},
		{"no_baseline", func(i *Input) { i.HasBase = false }, "no_baseline"},
		{"cooldown", func(i *Input) {
			i.Cur.LastActionAt = i.Now.Add(-5 * time.Minute)
			i.Cur.RentPrice = 2.0
		}, "cooldown"},
		{"noise", func(i *Input) {
			i.Cur.RentPrice = 2.0
			i.Cur.LastActionAt = i.Now.Add(-time.Hour)
		}, "noise"},
		{"eco_deposit_cap", func(i *Input) {
			i.Channel = domain.ChannelECO
			i.V = 10000 // rent×30 days ≫ cap 2×V
			i.Base = Base{Short: 800, Long: 700, Deposit: 0}
		}, "deposit_cap_exceeded"},
	}
	for _, c := range cases {
		in := baseInput()
		c.mutate(&in)
		d := Decide(in)
		if d.OK || d.SkipReason != c.wantSK {
			t.Fatalf("%s: got ok=%v skip=%q", c.name, d.OK, d.SkipReason)
		}
	}
}

func TestDecideChangeCapClamp(t *testing.T) {
	in := baseInput()
	in.Cur.RentPrice = 2.0
	in.Cur.LastActionAt = in.Now.Add(-time.Hour)
	in.Base.Short = 5.0 // raw target 5.0 = +150% — must clamp to +15%
	d := Decide(in)
	if !d.OK {
		t.Fatalf("skip: %s", d.SkipReason)
	}
	if d.Rent != Round2(2.0*1.15) {
		t.Fatalf("capped rent=%v want %v", d.Rent, Round2(2.3))
	}
}

func TestDecideECODerivedDeposit(t *testing.T) {
	in := baseInput()
	in.Channel = domain.ChannelECO
	d := Decide(in)
	if !d.OK {
		t.Fatalf("eco skip: %s", d.SkipReason)
	}
	// derived = max(140, rent×30) = 140 when rent≈2
	want := math.Max(in.V*1.4, d.Rent*float64(d.MaxDays))
	if d.Deposit != Round2(want) {
		t.Fatalf("derived deposit=%v want %v", d.Deposit, Round2(want))
	}
	if d.MaxDays != 30 {
		t.Fatalf("eco days=%v", d.MaxDays)
	}
	// ECO day bounds from channel capabilities
	in.P.ECOMaxDays = 5
	in.RentMaxDayMin = 8 // ECO caps require ≥8
	d = Decide(in)
	if !d.OK || d.MaxDays != 8 {
		t.Fatalf("eco min days not enforced: ok=%v days=%d", d.OK, d.MaxDays)
	}
}

func TestParseParamsDeepMerge(t *testing.T) {
	global := json.RawMessage(`{"uu_max_days":45,"factor":{"step_up":0.04}}`)
	template := json.RawMessage(`{"factor":{"stale_days":3},"guardrails":{"cooldown_minutes":10}}`)
	p, err := ParseParams(global, template)
	if err != nil {
		t.Fatal(err)
	}
	if p.UUMaxDays != 45 {
		t.Fatalf("global scalar lost: %+v", p.UUMaxDays)
	}
	if p.Ctrl.StepUp != 0.04 {
		t.Fatalf("global nested override lost: %+v", p.Ctrl.StepUp)
	}
	if p.Ctrl.StaleDays != 3 {
		t.Fatalf("template nested override lost: %+v", p.Ctrl.StaleDays)
	}
	if p.Guard.CooldownMinutes != 10 {
		t.Fatalf("template guard override lost")
	}
	if p.Baseline.K1 != 0.97 {
		t.Fatal("default must survive untouched fields")
	}

	if _, err := ParseParams(json.RawMessage(`{bad`), nil); err == nil {
		t.Fatal("bad json must error")
	}
}
