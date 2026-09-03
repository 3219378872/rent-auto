package store

import (
	"context"
	"fmt"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/domain"
)

// TerminalUnrecordedOrder is one finished rental awaiting income rollup.
type TerminalUnrecordedOrder struct {
	ID       int64
	Channel  domain.Channel
	Category string
	Amount   float64
	Cost     *float64 // cost basis from inventory (nullable)
	Finished time.Time
}

// UnrecordedTerminalOrders lists finished/bought-out orders not yet rolled up.
func (s *Store) UnrecordedTerminalOrders(ctx context.Context, limit int) ([]TerminalUnrecordedOrder, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT o.id, o.channel,
		        COALESCE(NULLIF(t.category,''),'未分类'),
		        o.order_amount,
		        inv.cost_basis,
		        COALESCE(o.finished_at, o.due_at, o.updated_at)
		 FROM lease_orders o
		 LEFT JOIN templates t ON t.hash_name = o.hash_name
		 LEFT JOIN inventory_items inv ON inv.channel=o.channel AND inv.asset_id=o.asset_id
		 WHERE o.status IN ('done','bought_out') AND o.income_recorded = false
		 ORDER BY o.id LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("unrecorded orders: %w", err)
	}
	defer rows.Close()
	var out []TerminalUnrecordedOrder
	for rows.Next() {
		var r TerminalUnrecordedOrder
		if err := rows.Scan(&r.ID, &r.Channel, &r.Category, &r.Amount, &r.Cost, &r.Finished); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RecordIncomeBatch applies daily-stat deltas for finished orders and flips
// their income_recorded flags in one transaction: either every order is
// counted and marked, or neither — a crash can never double-count income.
func (s *Store) RecordIncomeBatch(ctx context.Context, orders []TerminalUnrecordedOrder) error {
	if len(orders) == 0 {
		return nil
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin income tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	ids := make([]int64, 0, len(orders))
	for _, o := range orders {
		if _, err := tx.Exec(ctx,
			`INSERT INTO daily_stats(stat_date, channel, category, income, order_count)
			 VALUES($1,$2,$3,$4,$5)
			 ON CONFLICT(stat_date, channel, category) DO UPDATE SET
			   income = daily_stats.income + EXCLUDED.income,
			   order_count = daily_stats.order_count + EXCLUDED.order_count`,
			o.Finished.UTC().Format("2006-01-02"), o.Channel, o.Category, round2Money(o.Amount), 1); err != nil {
			return fmt.Errorf("upsert daily stat: %w", err)
		}
		ids = append(ids, o.ID)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE lease_orders SET income_recorded=true WHERE id = ANY($1)`, ids); err != nil {
		return fmt.Errorf("mark income recorded: %w", err)
	}
	return tx.Commit(ctx)
}

type ChannelTotal struct {
	Channel domain.Channel `json:"channel"`
	Income  float64        `json:"income"`
	Orders  int            `json:"orders"`
}

// IncomeByChannel sums recorded income from daily_stats.
func (s *Store) IncomeByChannel(ctx context.Context) ([]ChannelTotal, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT channel, SUM(income), SUM(order_count) FROM daily_stats GROUP BY channel ORDER BY channel`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChannelTotal
	for rows.Next() {
		var r ChannelTotal
		var f float64
		var n int
		if err := rows.Scan(&r.Channel, &f, &n); err != nil {
			return nil, err
		}
		r.Income, r.Orders = f, n
		out = append(out, r)
	}
	return out, rows.Err()
}

// TodayIncome returns today's rolled-up income (UTC day boundary, matching writes).
func (s *Store) TodayIncome(ctx context.Context) (float64, error) {
	var f *float64
	err := s.Pool.QueryRow(ctx,
		`SELECT SUM(income) FROM daily_stats
		 WHERE stat_date = (now() AT TIME ZONE 'utc')::date`).Scan(&f)
	if err != nil {
		return 0, err
	}
	if f == nil {
		return 0, nil
	}
	return *f, nil
}

type CategoryYield struct {
	Category string  `json:"category"`
	Cost     float64 `json:"cost"`
	Income   float64 `json:"income"`
	Yield    float64 `json:"yield"` // income / cost; 0 when cost==0
}

// CategoryYields computes per-category yield per data-model 口径 B:
// numerator = recorded income − sold-out inventory cost, denominator = all-time
// category cost basis (every status, not just held stock).
//
// 已知偏差 (review): category 取自当前 templates 映射——daily_stats.category
// 在 rollup 时按当时模板写入，而 costs/sold 按查询时模板分组——都不是订单发生
// 时的品类快照。模板改品类会追溯性漂移历史收益归属。精确到订单时刻需要先给
// lease_orders/daily_stats 加品类快照列；在那之前本口径保持现状，特此说明。
func (s *Store) CategoryYields(ctx context.Context) ([]CategoryYield, error) {
	rows, err := s.Pool.Query(ctx,
		`WITH costs AS (
		   SELECT COALESCE(NULLIF(t.category,''),'未分类') AS cat,
		          SUM(COALESCE(i.cost_basis,0)) AS cost
		   FROM inventory_items i JOIN templates t ON t.hash_name=i.hash_name
		   GROUP BY 1
		 ),
		 sold AS (
		   SELECT COALESCE(NULLIF(t.category,''),'未分类') AS cat,
		          SUM(COALESCE(i.cost_basis,0)) AS sold_cost
		   FROM inventory_items i JOIN templates t ON t.hash_name=i.hash_name
		   WHERE i.status='sold'
		   GROUP BY 1
		 )
		 SELECT c.cat, c.cost,
		        COALESCE(d.income,0) - COALESCE(s.sold_cost,0)
		 FROM costs c
		 LEFT JOIN (
		   SELECT ds.category, SUM(ds.income) AS income
		   FROM daily_stats ds GROUP BY ds.category
		 ) d ON d.category = c.cat
		 LEFT JOIN sold s ON s.cat = c.cat`)
	if err != nil {
		return nil, fmt.Errorf("category yields: %w", err)
	}
	defer rows.Close()
	var out []CategoryYield
	for rows.Next() {
		var r CategoryYield
		if err := rows.Scan(&r.Category, &r.Cost, &r.Income); err != nil {
			return nil, err
		}
		if r.Cost > 0 {
			r.Yield = r.Income / r.Cost
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// AssetValuation returns Σ inventory value over value_anchor only. Templates
// without an anchor contribute nothing: the old mark_price fallback mixed a
// channel-side list price into the valuation anchor and inflated total assets.
func (s *Store) AssetValuation(ctx context.Context) (float64, error) {
	var v *float64
	err := s.Pool.QueryRow(ctx,
		`SELECT SUM(t.value_anchor)
		 FROM inventory_items i JOIN templates t ON t.hash_name=i.hash_name
		 WHERE i.status IN ('in_stock','listed','leased')`).Scan(&v)
	if err != nil || v == nil {
		return 0, err
	}
	return *v, nil
}

// HeldDeposits sums deposits of leasing orders per channel. Only 'leasing'
// counts: delivering/returning are in-transit (押金归属不明) and arbitrating
// is dispute-frozen — including them overstated held funds and total assets.
// (returning could arguably count while the deposit is still withheld, but
// the conservative 口径 excludes it; revisit with 真机 deposit-lifecycle data.)
func (s *Store) HeldDeposits(ctx context.Context) (map[domain.Channel]float64, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT channel, SUM(deposits) FROM lease_orders
		 WHERE status='leasing'
		 GROUP BY channel`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[domain.Channel]float64{}
	for rows.Next() {
		var ch domain.Channel
		var f *float64
		if err := rows.Scan(&ch, &f); err != nil {
			return nil, err
		}
		if f != nil {
			out[ch] = *f
		}
	}
	return out, rows.Err()
}

// TotalCostEverBasis sums cost basis over all inventory statuses — the capital
// ever deployed (denominator of the annualized ROI per data-model 口径).
func (s *Store) TotalCostEverBasis(ctx context.Context) (float64, error) {
	var v *float64
	err := s.Pool.QueryRow(ctx,
		`SELECT SUM(cost_basis) FROM inventory_items WHERE cost_basis IS NOT NULL`).Scan(&v)
	if err != nil || v == nil {
		return 0, err
	}
	return *v, nil
}

// SoldCostBasis sums cost of inventory already consumed (status='sold') —
// subtracted from income for net realized return (data-model 口径 B).
func (s *Store) SoldCostBasis(ctx context.Context) (float64, error) {
	var v *float64
	err := s.Pool.QueryRow(ctx,
		`SELECT SUM(cost_basis) FROM inventory_items
		 WHERE status='sold' AND cost_basis IS NOT NULL`).Scan(&v)
	if err != nil || v == nil {
		return 0, err
	}
	return *v, nil
}

// FirstCostDate anchors the annualization window.
func (s *Store) FirstCostDate(ctx context.Context) (*time.Time, error) {
	var t *time.Time
	err := s.Pool.QueryRow(ctx,
		`SELECT MIN(cost_updated_at) FROM inventory_items WHERE cost_basis IS NOT NULL`).Scan(&t)
	return t, err
}

type DailyPoint struct {
	Date   string  `json:"date"`
	Income float64 `json:"income"`
}

// IncomeSeries returns the last N days of income (UTC day boundary).
func (s *Store) IncomeSeries(ctx context.Context, days int) ([]DailyPoint, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT stat_date::text, SUM(income) FROM daily_stats
		 WHERE stat_date > (now() AT TIME ZONE 'utc')::date - $1::int
		 GROUP BY stat_date ORDER BY stat_date`, days)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DailyPoint
	for rows.Next() {
		var p DailyPoint
		var f *float64
		if err := rows.Scan(&p.Date, &f); err != nil {
			return nil, err
		}
		if f != nil {
			p.Income = *f
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// LeasedCount counts orders in leasing state.
func (s *Store) LeasedCount(ctx context.Context) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx,
		`SELECT count(*) FROM lease_orders WHERE status='leasing'`).Scan(&n)
	return n, err
}

// RecordWalletSnapshot stores a wallet balance as a fund-flow row.
func (s *Store) RecordWalletSnapshot(ctx context.Context, channel domain.Channel, amount float64) error {
	amount = round2Money(amount)
	ref := walletFlowRef(channel, time.Now().UTC())
	_, err := s.Pool.Exec(ctx,
		`INSERT INTO fund_flows(channel, flow_ref, amount, type, occurred_at)
		 VALUES($1,$2,$3,'wallet_snapshot',$4)
		 ON CONFLICT DO NOTHING`,
		channel, ref, amount, time.Now().UTC())
	return err
}
