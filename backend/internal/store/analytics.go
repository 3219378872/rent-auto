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

// MarkIncomeRecorded flips the flag after successful rollup.
func (s *Store) MarkIncomeRecorded(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := s.Pool.Exec(ctx, `UPDATE lease_orders SET income_recorded=true WHERE id = ANY($1)`, ids)
	return err
}

// UpsertDailyStat adds a delta into the daily rollup.
func (s *Store) UpsertDailyStat(ctx context.Context, date time.Time, channel domain.Channel, category string, incomeDelta float64, orderDelta int) error {
	_, err := s.Pool.Exec(ctx,
		`INSERT INTO daily_stats(stat_date, channel, category, income, order_count)
		 VALUES($1,$2,$3,$4,$5)
		 ON CONFLICT(stat_date, channel, category) DO UPDATE SET
		   income = daily_stats.income + EXCLUDED.income,
		   order_count = daily_stats.order_count + EXCLUDED.order_count`,
		date.UTC().Format("2006-01-02"), channel, category, incomeDelta, orderDelta)
	if err != nil {
		return fmt.Errorf("upsert daily stat: %w", err)
	}
	return nil
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

// TodayIncome returns today's rolled-up income.
func (s *Store) TodayIncome(ctx context.Context) (float64, error) {
	var f *float64
	err := s.Pool.QueryRow(ctx,
		`SELECT SUM(income) FROM daily_stats WHERE stat_date = CURRENT_DATE`).Scan(&f)
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

// CategoryYields computes per-category cost vs recorded income.
func (s *Store) CategoryYields(ctx context.Context) ([]CategoryYield, error) {
	rows, err := s.Pool.Query(ctx,
		`WITH costs AS (
		   SELECT COALESCE(NULLIF(t.category,''),'未分类') AS cat,
		          SUM(COALESCE(i.cost_basis,0)) AS cost
		   FROM inventory_items i JOIN templates t ON t.hash_name=i.hash_name
		   WHERE i.status IN ('in_stock','listed','leased')
		   GROUP BY 1
		 )
		 SELECT c.cat, c.cost, COALESCE(d.income,0)
		 FROM costs c
		 LEFT JOIN (
		   SELECT ds.category, SUM(ds.income) AS income
		   FROM daily_stats ds GROUP BY ds.category
		 ) d ON d.category = c.cat`)
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

// AssetValuation returns Σ inventory value (anchor preferred, mark fallback).
func (s *Store) AssetValuation(ctx context.Context) (float64, error) {
	var v *float64
	err := s.Pool.QueryRow(ctx,
		`SELECT SUM(COALESCE(t.value_anchor, i.mark_price, 0))
		 FROM inventory_items i JOIN templates t ON t.hash_name=i.hash_name
		 WHERE i.status IN ('in_stock','listed','leased')`).Scan(&v)
	if err != nil || v == nil {
		return 0, err
	}
	return *v, nil
}

// HeldDeposits sums deposits of in-flight orders per channel.
func (s *Store) HeldDeposits(ctx context.Context) (map[domain.Channel]float64, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT channel, SUM(deposits) FROM lease_orders
		 WHERE status IN ('delivering','leasing','returning','arbitrating')
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

// TotalCostBasis sums cost of currently held inventory.
func (s *Store) TotalCostBasis(ctx context.Context) (float64, error) {
	var v *float64
	err := s.Pool.QueryRow(ctx,
		`SELECT SUM(cost_basis) FROM inventory_items
		 WHERE status IN ('in_stock','listed','leased') AND cost_basis IS NOT NULL`).Scan(&v)
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

// IncomeSeries returns the last N days of income.
func (s *Store) IncomeSeries(ctx context.Context, days int) ([]DailyPoint, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT stat_date::text, SUM(income) FROM daily_stats
		 WHERE stat_date > CURRENT_DATE - $1::int GROUP BY stat_date ORDER BY stat_date`, days)
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
	ref := fmt.Sprintf("wallet-%s-%s", channel, time.Now().UTC().Format("20060102150405"))
	_, err := s.Pool.Exec(ctx,
		`INSERT INTO fund_flows(channel, flow_ref, amount, type, occurred_at)
		 VALUES($1,$2,$3,'wallet_snapshot',$4)
		 ON CONFLICT DO NOTHING`,
		channel, ref, amount, time.Now().UTC())
	return err
}
