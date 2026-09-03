// Package pricing is the revenue-maximizing decision engine (pricing-spec.md).
// It is a pure-logic domain: no network, no persistence.
package pricing

import (
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/domain"
)

// Round2 rounds money to two decimals, half away from zero.
// All monetary outputs of this package MUST pass through Round2.
// Non-finite or out-of-range inputs collapse to 0 — a NaN must never become
// an int64-undefined price on its way to the platform.
func Round2(v float64) float64 {
	if !finite(v) || v >= 1e15 || v <= -1e15 {
		return 0
	}
	if v >= 0 {
		return float64(int64(v*100+0.5)) / 100
	}
	return float64(int64(v*100-0.5)) / 100
}

// Quote is one normalized lease-market offer (UU public market).
type Quote struct {
	Name    string
	Short   float64 // LeaseUnitPrice; 0 = absent
	Long    float64 // LongLeaseUnitPrice; 0 = absent
	Deposit float64 // 0 = absent
}

// ---- parameters ----

type BaselineParams struct {
	TopN          int     `json:"topn"`            // quotes considered
	K1            float64 `json:"k1"`              // short multiplier (0.97)
	K2            float64 `json:"k2"`              // long mean multiplier (0.95)
	K3            float64 `json:"k3"`              // deposit mean multiplier (0.98)
	MinLeaseRatio float64 `json:"min_lease_ratio"` // short floor = ratio×V; 0 = off
}

type ControllerParams struct {
	FMin      float64 `json:"min"`
	FMax      float64 `json:"max"`
	StepUp    float64 `json:"step_up"`
	StepDown  float64 `json:"step_down"`
	StaleDays int     `json:"stale_days"`
}

type Guardrails struct {
	MinRent           float64 `json:"min_rent"`
	MaxRent           float64 `json:"max_rent"`
	MaxChangeRatio    float64 `json:"max_change_ratio"`
	NoiseRatio        float64 `json:"noise_ratio"`
	CooldownMinutes   int     `json:"cooldown_minutes"`
	DepositFloorRatio float64 `json:"deposit_floor_ratio"` // UU: dep ≥ ratio×V
	DepositCapRatio   float64 `json:"deposit_cap_ratio"`   // ECO: derived dep ≤ ratio×V
}

type Params struct {
	Baseline   BaselineParams   `json:"baseline"`
	Ctrl       ControllerParams `json:"factor"`
	Guard      Guardrails       `json:"guardrails"`
	UUMaxDays  int              `json:"uu_max_days"`
	ECOMaxDays int              `json:"eco_max_days"`
}

func DefaultParams() Params {
	return Params{
		Baseline: BaselineParams{TopN: 15, K1: 0.97, K2: 0.95, K3: 0.98},
		Ctrl:     ControllerParams{FMin: 0.85, FMax: 1.25, StepUp: 0.03, StepDown: 0.05, StaleDays: 7},
		Guard: Guardrails{
			MinRent: 0.5, MaxRent: 20000, MaxChangeRatio: 0.15, NoiseRatio: 0.02,
			CooldownMinutes: 30, DepositFloorRatio: 0.3, DepositCapRatio: 2.0,
		},
		UUMaxDays:  60,
		ECOMaxDays: 30,
	}
}

// ParseParams merges global and template-level jsonb params over defaults.
// Template fields override global; absent fields inherit.
func ParseParams(globalJSON, templateJSON json.RawMessage) (Params, error) {
	p := DefaultParams()
	if len(globalJSON) > 0 {
		if err := json.Unmarshal(globalJSON, &p); err != nil {
			return p, fmt.Errorf("pricing: global params: %w", err)
		}
	}
	if len(templateJSON) > 0 {
		var g, o map[string]any
		_ = json.Unmarshal(globalJSON, &g)
		if err := json.Unmarshal(templateJSON, &o); err != nil {
			return p, fmt.Errorf("pricing: template params: %w", err)
		}
		merged := deepMerge(typedToRaw(p), g, o)
		b, err := json.Marshal(merged)
		if err != nil {
			return p, err
		}
		if err := json.Unmarshal(b, &p); err != nil {
			return p, err
		}
	}
	if err := p.Validate(); err != nil {
		return p, err
	}
	return p, nil
}

// Validate rejects misconfigured ranges that would otherwise silently
// collapse (e.g. FMin>FMax pins the factor at FMax for every template).
func (p Params) Validate() error {
	if p.Ctrl.FMin > p.Ctrl.FMax {
		return fmt.Errorf("pricing: factor.min %.4f > max %.4f", p.Ctrl.FMin, p.Ctrl.FMax)
	}
	if p.Guard.MaxChangeRatio < 0 || p.Guard.NoiseRatio < 0 {
		return fmt.Errorf("pricing: change/noise ratios must be >= 0")
	}
	if p.Guard.MinRent < 0 || p.Guard.MaxRent < 0 || p.Guard.MinRent > p.Guard.MaxRent {
		return fmt.Errorf("pricing: min_rent/max_rent range invalid")
	}
	if p.Guard.DepositFloorRatio < 0 || p.Guard.DepositCapRatio < 0 {
		return fmt.Errorf("pricing: deposit ratios must be >= 0")
	}
	return nil
}

func typedToRaw(p Params) map[string]any {
	b, _ := json.Marshal(p)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

func deepMerge(base map[string]any, layers ...map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range base {
		out[k] = v
	}
	for _, l := range layers {
		for k, v := range l {
			if vm, ok := v.(map[string]any); ok {
				if bm, ok := out[k].(map[string]any); ok {
					out[k] = deepMerge(bm, vm)
					continue
				}
			}
			out[k] = v
		}
	}
	return out
}

// ---- baseline (port of Steamauto get_lease_price, parameterized) ----

// Baseline computes reference short/long/deposit from market quotes.
// Behavior contract with upstream:
//
//	shorts   = first min(10,n) non-zero Short, in order
//	baseShort= clamp(mean(shorts)×K1, floor=shorts[0], min=0.01)
//	baseLong : no longs → max(baseShort−0.01, 0.01)
//	           else     → max(min(baseShort×0.98, mean(longs)×K2), longs[0], 0.01)
//	baseDep  : deposits from the shorts window → max(mean×K3, min(deposits))
//	MinLeaseRatio floor: baseShort = max(baseShort, ratio×V) when V>0
func Baseline(quotes []Quote, p BaselineParams, V float64) (Base, bool) {
	topn := p.TopN
	if topn <= 0 {
		topn = 15
	}
	if len(quotes) > topn {
		quotes = quotes[:topn]
	}
	var shorts, longs, deps []float64
	for i, q := range quotes {
		// Non-finite market data (float8 'Infinity' upstream) is absent data,
		// never a price.
		if posFinite(q.Short) && i < 10 {
			shorts = append(shorts, q.Short)
			if posFinite(q.Deposit) {
				deps = append(deps, q.Deposit)
			}
		}
		if posFinite(q.Long) {
			longs = append(longs, q.Long)
		}
	}
	if len(shorts) == 0 {
		return Base{}, false
	}
	short := clampF(mean(shorts)*p.K1, math.Max(shorts[0], 0.01), math.Inf(1))
	if p.MinLeaseRatio > 0 && V > 0 && finite(p.MinLeaseRatio*V) {
		short = math.Max(short, p.MinLeaseRatio*V)
	}
	var long float64
	if len(longs) == 0 {
		long = math.Max(short-0.01, 0.01)
	} else {
		long = math.Max(math.Min(short*0.98, mean(longs)*p.K2), math.Max(longs[0], 0.01))
	}
	var dep float64
	if len(deps) > 0 {
		dep = math.Max(mean(deps)*p.K3, minF(deps))
	}
	b := Base{Short: Round2(short), Long: Round2(math.Min(long, short)), Deposit: Round2(dep)}
	return b, true
}

type Base struct{ Short, Long, Deposit float64 }

func mean(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := 0.0
	for _, x := range v {
		s += x
	}
	return s / float64(len(v))
}

func minF(v []float64) float64 {
	m := v[0]
	for _, x := range v[1:] {
		if x < m {
			m = x
		}
	}
	return m
}

func clampF(v, lo, hi float64) float64 { return math.Min(math.Max(v, lo), hi) }

// finite reports v ∈ ℝ (excludes NaN and ±Inf).
func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// posFinite reports a usable positive price: strictly positive AND real.
func posFinite(v float64) bool { return v > 0 && finite(v) }

// ---- feedback controller ----

type FactorEvent string

const (
	EventRentSuccess FactorEvent = "rent_success"
	EventBoughtOut   FactorEvent = "bought_out"
	EventStaleDay    FactorEvent = "stale_day"
	EventReset       FactorEvent = "reset"
)

// NextFactor advances the controller factor; result clamped to [FMin,FMax].
// A non-finite incoming factor (corrupted storage) recovers at FMin.
func NextFactor(cur float64, ev FactorEvent, p ControllerParams) (float64, string) {
	reason := ""
	if !finite(cur) {
		cur = p.FMin
		ev = EventReset
		reason = "non_finite_recovered"
	}
	switch ev {
	case EventRentSuccess:
		cur += p.StepUp
		reason = "rented"
	case EventBoughtOut:
		cur += 2 * p.StepUp
		reason = "bought_out"
	case EventStaleDay:
		cur -= p.StepDown
		reason = "stale"
	case EventReset:
		cur = 1.0
		reason = "reset"
	}
	if cur < p.FMin {
		cur = p.FMin
		reason += "|clamped_min"
	}
	if cur > p.FMax {
		cur = p.FMax
		reason += "|clamped_max"
	}
	return Round4(cur), reason
}

func Round4(v float64) float64 { return math.Round(v*10000) / 10000 }

// ---- decision ----

type Current struct {
	RentPrice    float64
	LastActionAt time.Time
}

type Input struct {
	Channel domain.Channel
	HasV    bool
	V       float64
	Base    Base
	HasBase bool
	Factor  float64
	Cur     Current
	P       Params
	Now     time.Time
	// RentMaxDayMin/Max from channel capabilities (ECO 8..90)
	RentMaxDayMin int
	RentMaxDayMax int
	// IgnoreNoiseFloor bypasses the noise-floor skip (a price move below
	// NoiseRatio normally aborts the submission). Used for one-shot policy
	// backfills (ECO sublet flag) where the payload must reach the platform
	// even when the computed price is unchanged; the change-rate cap and
	// cooldown still apply.
	IgnoreNoiseFloor bool
}

type Decision struct {
	OK         bool     `json:"ok"`
	SkipReason string   `json:"skip_reason,omitempty"`
	Reasons    []string `json:"reasons"`
	Rent       float64  `json:"rent"`
	Long       float64  `json:"long"`
	MaxDays    int      `json:"max_days"`
	Deposit    float64  `json:"deposit"`
}

// Decide produces the next listing parameters for one (channel, template).
func Decide(in Input) Decision {
	if !in.HasV || in.V <= 0 {
		return Decision{SkipReason: "no_value_anchor"}
	}
	if !in.HasBase || in.Base.Short <= 0 {
		return Decision{SkipReason: "no_baseline"}
	}
	// Finite guards: one NaN/Inf anywhere in the numeric inputs must abort the
	// decision instead of poisoning prices downstream (Round2 would clamp it
	// to a bogus value and the platform would receive it).
	if !finite(in.V) || !finite(in.Base.Short) || !finite(in.Base.Long) ||
		!finite(in.Base.Deposit) || !finite(in.Factor) {
		return Decision{SkipReason: "non_finite_input"}
	}
	g := in.P.Guard

	// 1. target rent from baseline × factor
	f := clampF(in.Factor, in.P.Ctrl.FMin, in.P.Ctrl.FMax)
	rent := in.Base.Short * f
	if in.P.Baseline.MinLeaseRatio > 0 {
		rent = math.Max(rent, in.P.Baseline.MinLeaseRatio*in.V)
	}

	// 2. absolute bounds
	if rent < g.MinRent {
		rent = g.MinRent
	}
	if rent > g.MaxRent {
		rent = g.MaxRent
	}
	rent = Round2(rent)

	// 3. cooldown
	cd := time.Duration(g.CooldownMinutes) * time.Minute
	if !in.Cur.LastActionAt.IsZero() && in.Now.Sub(in.Cur.LastActionAt) < cd {
		return Decision{SkipReason: "cooldown"}
	}

	// 4. change-rate cap + noise floor (only when a current price exists).
	// The cap is re-clamped to absolute bounds: near max_rent an upward
	// jump must not escape the ceiling (and symmetrically for the floor).
	if in.Cur.RentPrice > 0 {
		old := in.Cur.RentPrice
		capped := clampF(rent, old*(1-g.MaxChangeRatio), old*(1+g.MaxChangeRatio))
		capped = clampF(capped, g.MinRent, g.MaxRent)
		capped = Round2(capped)
		if math.Abs(capped-old) > 1e-9 && capped != rent {
			rent = capped
		}
		if math.Abs(rent-old)/math.Max(old, 0.01) < g.NoiseRatio && !in.IgnoreNoiseFloor {
			return Decision{SkipReason: "noise"}
		}
	}

	// 5. channel-specific long/days/deposit
	days := in.P.UUMaxDays
	long := math.Min(rent*0.98, in.Base.Long)
	dep := math.Max(in.Base.Deposit, g.DepositFloorRatio*in.V)
	switch in.Channel {
	case domain.ChannelECO:
		days = in.P.ECOMaxDays
		if in.RentMaxDayMin > 0 && days < in.RentMaxDayMin {
			days = in.RentMaxDayMin
		}
		if in.RentMaxDayMax > 0 && days > in.RentMaxDayMax {
			days = in.RentMaxDayMax
		}
		derived := math.Max(in.V*1.4, rent*float64(days))
		if long > 0 {
			derived = math.Max(derived, long*float64(days))
		}
		if g.DepositCapRatio > 0 && derived > g.DepositCapRatio*in.V {
			return Decision{
				SkipReason: "deposit_cap_exceeded",
				Reasons: []string{fmt.Sprintf("derived_deposit=%.2f cap=%.2f V=%.2f",
					derived, g.DepositCapRatio*in.V, in.V)},
			}
		}
		dep = derived
	default: // UU
		if days < 8 { // upstream rule: ≤8 days must not carry long price
			long = 0
		}
	}

	long = Round2(math.Max(math.Min(long, rent), 0))
	dep = Round2(dep)

	return Decision{
		OK: true, Rent: rent, Long: long, MaxDays: days, Deposit: dep,
		Reasons: []string{fmt.Sprintf("factor=%.4f base=%.2f V=%.2f", f, in.Base.Short, in.V)},
	}
}
