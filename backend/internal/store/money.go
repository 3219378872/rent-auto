package store

import (
	"fmt"
	"math"
	"sync/atomic"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/domain"
)

// round2Money rounds money to two decimals (half away from zero), mirroring
// pricing.Round2 without importing the pricing package (dependency direction:
// pricing must not be imported by store; see repo-layout.md). Non-finite or
// out-of-range inputs collapse to 0.
func round2Money(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) || v >= 1e15 || v <= -1e15 {
		return 0
	}
	if v >= 0 {
		return float64(int64(v*100+0.5)) / 100
	}
	return float64(int64(v*100-0.5)) / 100
}

// round2MoneyPtr normalizes a nullable money pointer in place (nil-safe):
// every nullable money leg written to the DB must pass through round2Money
// (AGENTS.md 硬规则). Returns the same pointer for call chaining.
func round2MoneyPtr(p *float64) *float64 {
	if p != nil {
		*p = round2Money(*p)
	}
	return p
}

// walletRefSeq is a process-local sequence disambiguating wallet snapshot
// refs generated within the same nanosecond tick.
var walletRefSeq atomic.Uint64

// walletFlowRef builds a unique fund-flow ref for wallet snapshots. The old
// second-precision format collided when the dashboard was built twice within
// one second (BuildDashboard records one row per channel per build): the
// repeat hit the (channel, flow_ref) UNIQUE and was silently dropped by
// ON CONFLICT DO NOTHING, punching gaps in wallet history. Nanosecond
// wall-clock plus a sequence suffix makes repeats impossible.
func walletFlowRef(channel domain.Channel, now time.Time) string {
	return fmt.Sprintf("wallet-%s-%s-%06d",
		channel, now.UTC().Format("20060102150405.000000000"),
		walletRefSeq.Add(1)%1000000)
}
