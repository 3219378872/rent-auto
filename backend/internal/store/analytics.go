package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/domain"
	"github.com/jackc/pgx/v5"
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

// UnrecordedTerminalOrders includes corrections and reversals of earlier income.
func (s *Store) UnrecordedTerminalOrders(ctx context.Context, limit int) ([]TerminalUnrecordedOrder, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT o.id, o.channel,
		        COALESCE(NULLIF(t.category,''),'未分类'),
		        o.order_amount,
		        inv.cost_basis,
		        COALESCE(o.finished_at, o.updated_at)
		 FROM lease_orders o
		 LEFT JOIN templates t ON t.hash_name = o.hash_name
		 LEFT JOIN physical_inventory inv ON inv.asset_id<>'' AND inv.asset_id=o.asset_id
		 LEFT JOIN order_income_ledger l ON l.order_id=o.id
		 WHERE (o.status IN ('done','bought_out') AND (
		   NOT o.income_recorded OR l.order_id IS NULL OR
		   l.amount IS DISTINCT FROM o.order_amount OR
		   l.stat_date IS DISTINCT FROM (COALESCE(o.finished_at,o.updated_at) AT TIME ZONE 'utc')::date OR
		   l.category IS DISTINCT FROM COALESCE(NULLIF(t.category,''),'未分类') OR
		   l.channel IS DISTINCT FROM o.channel))
		 OR (o.status NOT IN ('done','bought_out') AND (l.order_id IS NOT NULL OR o.income_recorded))
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

// RecordIncomeBatch rereads locked orders; a stale or duplicated input batch
// cannot double count income or overwrite a correction made after its query.
func (s *Store) RecordIncomeBatch(ctx context.Context, orders []TerminalUnrecordedOrder) error {
	if len(orders) == 0 {
		return nil
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin income tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Serialize bucket updates as orders can move in opposite date/category
	// directions. Keep row locks ordered for concurrent order synchronization.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('income_projection',0))`); err != nil {
		return err
	}
	ids := make([]int64, 0, len(orders))
	for _, o := range orders {
		ids = append(ids, o.ID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for i, id := range ids {
		if i > 0 && ids[i-1] == id {
			continue
		}
		if err := projectOrderIncome(ctx, tx, id); err != nil {
			return fmt.Errorf("project income order %d: %w", id, err)
		}
	}
	return tx.Commit(ctx)
}

type incomeProjection struct {
	Date     string
	Channel  domain.Channel
	Category string
	Amount   float64
}

func projectOrderIncome(ctx context.Context, tx pgx.Tx, id int64) error {
	var next, old incomeProjection
	var eligible bool
	err := tx.QueryRow(ctx, `SELECT (COALESCE(o.finished_at,o.updated_at) AT TIME ZONE 'utc')::date::text,
	 o.channel,COALESCE(NULLIF(t.category,''),'未分类'),o.order_amount,o.status IN ('done','bought_out')
	 FROM lease_orders o LEFT JOIN templates t ON t.hash_name=o.hash_name
	 WHERE o.id=$1 FOR UPDATE OF o`, id).Scan(&next.Date, &next.Channel, &next.Category, &next.Amount, &eligible)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	err = tx.QueryRow(ctx, `SELECT stat_date::text,channel,category,amount FROM order_income_ledger WHERE order_id=$1`, id).
		Scan(&old.Date, &old.Channel, &old.Category, &old.Amount)
	oldExists := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if oldExists && (!eligible || old != next) {
		if err := applyIncomeDelta(ctx, tx, old, -1); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM order_income_ledger WHERE order_id=$1`, id); err != nil {
			return err
		}
	}
	if eligible && (!oldExists || old != next) {
		if err := applyIncomeDelta(ctx, tx, next, 1); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO order_income_ledger(order_id,stat_date,channel,category,amount)
		 VALUES($1,$2::date,$3,$4,$5)`, id, next.Date, next.Channel, next.Category, round2Money(next.Amount)); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE lease_orders SET income_recorded=$2 WHERE id=$1`, id, eligible)
	return err
}

func applyIncomeDelta(ctx context.Context, tx pgx.Tx, p incomeProjection, direction int) error {
	_, err := tx.Exec(ctx, `INSERT INTO daily_stats(stat_date,channel,category,income,order_count)
	 VALUES($1::date,$2,$3,$4,$5) ON CONFLICT(stat_date,channel,category) DO UPDATE SET
	 income=daily_stats.income+excluded.income,order_count=daily_stats.order_count+excluded.order_count`,
		p.Date, p.Channel, p.Category, round2Money(float64(direction)*p.Amount), direction)
	return err
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
// Categories use the current template mapping; the income projection catches
// category corrections at the next rollup, not the category at rental time.
func (s *Store) CategoryYields(ctx context.Context) ([]CategoryYield, error) {
	rows, err := s.Pool.Query(ctx,
		`WITH costs AS (
		   SELECT COALESCE(NULLIF(t.category,''),'未分类') AS cat,
		          SUM(COALESCE(i.cost_basis,0)) AS cost
		   FROM physical_inventory i JOIN templates t ON t.hash_name=i.hash_name
		   GROUP BY 1
		 ),
		 sold AS (
		   SELECT COALESCE(NULLIF(t.category,''),'未分类') AS cat,
		          SUM(COALESCE(i.cost_basis,0)) AS sold_cost
		   FROM physical_inventory i JOIN templates t ON t.hash_name=i.hash_name
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
		 FROM physical_inventory i JOIN templates t ON t.hash_name=i.hash_name
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
		`SELECT SUM(cost_basis) FROM physical_inventory WHERE cost_basis IS NOT NULL`).Scan(&v)
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
		`SELECT SUM(cost_basis) FROM physical_inventory
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
		`SELECT MIN(cost_updated_at) FROM physical_inventory WHERE cost_basis IS NOT NULL`).Scan(&t)
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
