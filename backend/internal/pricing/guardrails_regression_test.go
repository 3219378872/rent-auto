package pricing

import (
	"math"
	"testing"

	"github.com/3219378872/rent-auto/backend/internal/domain"
)

func TestFinalECOTripleRespectsDepositCap(t *testing.T) {
	in := baseInput()
	in.Channel, in.V = domain.ChannelECO, 100
	in.Base, in.Cur.RentPrice = Base{Short: 1, Long: .9}, 10
	d := Decide(in)
	if d.OK || d.SkipReason != "deposit_cap_exceeded" {
		t.Fatalf("pre-cap deposit must not validate final triple: %+v", d)
	}
	in.P.Guard.DepositCapRatio = 3
	d = Decide(in)
	if !d.OK || d.Deposit != math.Max(1.4*in.V, d.Rent*float64(d.MaxDays)) {
		t.Fatalf("final deposit mismatch: %+v", d)
	}
}

func TestZeroAndSubCentChangeCaps(t *testing.T) {
	for _, ratio := range []float64{0, .001} {
		in := baseInput()
		in.Cur.RentPrice = 1
		in.Base.Short = 10
		in.P.Guard.MaxChangeRatio = ratio
		if d := Decide(in); d.OK && d.Rent != 1 {
			t.Fatalf("ratio %v escaped cap: %+v", ratio, d)
		}
	}
}

func TestRatioFloorAndChangeCapConflict(t *testing.T) {
	in := baseInput()
	in.V, in.Base.Short, in.Cur.RentPrice = 1000, 10, 1
	in.P.Baseline.MinLeaseRatio = .01
	if d := Decide(in); d.OK || d.SkipReason != "guardrail_conflict" {
		t.Fatalf("conflicting constraints must skip: %+v", d)
	}
}

func TestStableRentStillCorrectsOtherTerms(t *testing.T) {
	base := baseInput()
	initial := Decide(base)
	base.Cur = Current{RentPrice: initial.Rent, LongPrice: initial.Long, Deposit: initial.Deposit, MaxDays: initial.MaxDays, TermsKnown: true}
	if d := Decide(base); d.OK || d.SkipReason != "noise" {
		t.Fatalf("identical payload should skip: %+v", d)
	}
	for name, change := range map[string]func(*Input){
		"deposit": func(in *Input) { in.P.Guard.DepositFloorRatio = .5 },
		"long":    func(in *Input) { in.Base.Long = 1.5 },
		"days":    func(in *Input) { in.P.UUMaxDays = 30 },
	} {
		t.Run(name, func(t *testing.T) {
			in := base
			change(&in)
			if d := Decide(in); !d.OK || d.Rent != initial.Rent {
				t.Fatalf("other term correction was lost: %+v", d)
			}
			in.Cur.LastActionAt = in.Now
			if d := Decide(in); d.OK || d.SkipReason != "cooldown" {
				t.Fatalf("correction must respect cooldown: %+v", d)
			}
		})
	}
}
