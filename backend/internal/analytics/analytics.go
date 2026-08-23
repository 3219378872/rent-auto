// Package analytics computes financial statistics (data-model.md 口径).
// Pure-logic domain: math over store-provided aggregates.
package analytics

import (
	"context"
	"log/slog"
	"math"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/domain"
	"github.com/3219378872/rent-auto/backend/internal/store"
)

// Round2 rounds money to two decimals, half away from zero.
func Round2(v float64) float64 {
	if v >= 0 {
		return float64(int64(v*100+0.5)) / 100
	}
	return float64(int64(v*100-0.5)) / 100
}

// AnnualizedROI = netIncome / cost × (365d / observation days).
// Returns 0 when inputs are insufficient (never negative-days or div-by-zero panics).
func AnnualizedROI(netIncome, cost float64, firstCost, now time.Time) float64 {
	if cost <= 0 {
		return 0
	}
	days := now.Sub(firstCost).Hours() / 24
	if days < 1 {
		days = 1 // floor to avoid absurd extrapolation on day one
	}
	roi := (netIncome / cost) * (365.0 / days)
	if math.IsInf(roi, 0) || math.IsNaN(roi) {
		return 0
	}
	return math.Round(roi*10000) / 10000
}

// RollupTerminalOrders records income for finished orders into daily_stats.
func RollupTerminalOrders(ctx context.Context, st *store.Store, log *slog.Logger) (int, error) {
	const batch = 200
	orders, err := st.UnrecordedTerminalOrders(ctx, batch)
	if err != nil {
		return 0, err
	}
	n := 0
	ids := make([]int64, 0, len(orders))
	for _, o := range orders {
		if err := st.UpsertDailyStat(ctx, o.Finished, o.Channel, o.Category, o.Amount, 1); err != nil {
			return n, err
		}
		ids = append(ids, o.ID)
		n++
	}
	if err := st.MarkIncomeRecorded(ctx, ids); err != nil {
		return n, err
	}
	if n > 0 {
		log.Info("income rollup", "orders", n)
	}
	return n, nil
}

// Dashboard is the aggregated payload for the panel home page.
type Dashboard struct {
	Assets struct {
		Total     float64            `json:"total"`
		Inventory float64            `json:"inventory"`
		Deposits  map[string]float64 `json:"deposits"`
		Wallets   map[string]float64 `json:"wallets"`
	} `json:"assets"`
	Income struct {
		Total     float64              `json:"total"`
		Today     float64              `json:"today"`
		ByChannel []store.ChannelTotal `json:"by_channel"`
	} `json:"income"`
	LeasedOut     int                   `json:"leased_out"`
	AnnualizedROI float64               `json:"annualized_roi"`
	Categories    []store.CategoryYield `json:"categories"`
	Series30d     []store.DailyPoint    `json:"series_30d"`
}

// BuildDashboard assembles all dashboard aggregates.
func BuildDashboard(ctx context.Context, st *store.Store, wallets map[domain.Channel]float64) (*Dashboard, error) {
	d := &Dashboard{Categories: []store.CategoryYield{}, Series30d: []store.DailyPoint{}}
	d.Assets.Deposits = map[string]float64{}
	d.Assets.Wallets = map[string]float64{}

	inv, err := st.AssetValuation(ctx)
	if err != nil {
		return nil, err
	}
	d.Assets.Inventory = Round2(inv)

	deposits, err := st.HeldDeposits(ctx)
	if err != nil {
		return nil, err
	}
	depositTotal := 0.0
	for ch, v := range deposits {
		r := Round2(v)
		d.Assets.Deposits[string(ch)] = r
		depositTotal += r
	}
	walletTotal := 0.0
	for ch, v := range wallets {
		r := Round2(v)
		d.Assets.Wallets[string(ch)] = r
		walletTotal += r
	}
	d.Assets.Total = Round2(d.Assets.Inventory + depositTotal + walletTotal)

	incomeByCh, err := st.IncomeByChannel(ctx)
	if err != nil {
		return nil, err
	}
	total := 0.0
	for _, c := range incomeByCh {
		total += c.Income
	}
	d.Income.ByChannel = incomeByCh
	d.Income.Total = Round2(total)

	today, err := st.TodayIncome(ctx)
	if err != nil {
		return nil, err
	}
	d.Income.Today = Round2(today)

	if d.LeasedOut, err = st.LeasedCount(ctx); err != nil {
		return nil, err
	}

	cost, err := st.TotalCostBasis(ctx)
	if err != nil {
		return nil, err
	}
	first, err := st.FirstCostDate(ctx)
	if err != nil {
		return nil, err
	}
	if first != nil && cost > 0 {
		d.AnnualizedROI = AnnualizedROI(total-cost, cost, *first, time.Now())
	} else if cost > 0 {
		d.AnnualizedROI = AnnualizedROI(total-cost, cost, time.Now(), time.Now())
	}

	cats, err := st.CategoryYields(ctx)
	if err != nil {
		return nil, err
	}
	for i := range cats {
		cats[i].Cost = Round2(cats[i].Cost)
		cats[i].Income = Round2(cats[i].Income)
		cats[i].Yield = math.Round(cats[i].Yield*10000) / 10000
	}
	d.Categories = cats

	series, err := st.IncomeSeries(ctx, 30)
	if err != nil {
		return nil, err
	}
	d.Series30d = series

	// persist wallet snapshots for history
	for ch, v := range wallets {
		_ = st.RecordWalletSnapshot(ctx, ch, v)
	}
	return d, nil
}
