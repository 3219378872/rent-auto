//go:build integration

package store

import (
	"context"
	"os"
	"testing"

	"github.com/3219378872/rent-auto/backend/internal/domain"
)

// ListListings must surface the feedback-controller factor and the latest
// price action per listing (panel 决策依据 column, round6).
func TestListListingsDecisionContext(t *testing.T) {
	ctx := context.Background()
	st, cleanup := openStoreDB(t)
	defer cleanup()

	if err := st.UpsertTemplate(ctx, Template{HashName: "H-Dec"}); err != nil {
		t.Fatal(err)
	}
	listing := ListingRow{
		Channel: domain.ChannelUU, AssetID: "a-dec", HashName: "H-Dec",
		GoodsRef: "G-DEC", DesiredState: "active", ActualState: "active",
		RentPrice: 1.5, MaxDays: 30,
	}
	if _, err := st.Pool.Exec(ctx,
		`INSERT INTO listings(channel, asset_id, hash_name, goods_ref, desired_state, actual_state, rent_price, max_days, factor)
		 VALUES($1,$2,$3,$4,'active','active',$5,$6,1.06)`,
		string(listing.Channel), listing.AssetID, listing.HashName, listing.GoodsRef,
		listing.RentPrice, listing.MaxDays); err != nil {
		t.Fatal(err)
	}
	var listingID int64
	if err := st.Pool.QueryRow(ctx,
		`SELECT id FROM listings WHERE goods_ref='G-DEC'`).Scan(&listingID); err != nil {
		t.Fatal(err)
	}
	newRent := 1.62
	if _, err := st.InsertPriceAction(ctx, PriceAction{
		Channel: domain.ChannelUU, HashName: "H-Dec", AssetID: "a-dec", ListingID: listingID,
		Action: "reprice", NewRent: &newRent,
	}); err != nil {
		t.Fatal(err)
	}

	rows, total, err := st.ListListings(ctx, ListingFilter{Channel: domain.ChannelUU})
	if err != nil || total < 1 {
		t.Fatalf("list: %v %d", err, total)
	}
	var found *ListingRow
	for i := range rows {
		if rows[i].GoodsRef == "G-DEC" {
			found = &rows[i]
		}
	}
	if found == nil {
		t.Fatal("listing missing")
	}
	if found.Factor != 1.06 {
		t.Fatalf("factor = %v", found.Factor)
	}
	if found.Last == nil || found.Last.Action != "reprice" ||
		found.Last.NewRent == nil || *found.Last.NewRent != 1.62 {
		t.Fatalf("last decision: %+v", found.Last)
	}

	// A skip action's reason must surface too.
	skipRent := 0.0
	if _, err := st.InsertPriceAction(ctx, PriceAction{
		Channel: domain.ChannelUU, HashName: "H-Dec", AssetID: "a-dec", ListingID: listingID,
		Action: "skip", Decision: []byte(`{"skip":"cooldown","reasons":[]}`),
		NewRent: &skipRent,
	}); err != nil {
		t.Fatal(err)
	}
	rows, _, _ = st.ListListings(ctx, ListingFilter{Channel: domain.ChannelUU})
	for i := range rows {
		if rows[i].GoodsRef == "G-DEC" {
			if rows[i].Last == nil || rows[i].Last.Action != "skip" || rows[i].Last.Skip != "cooldown" {
				t.Fatalf("latest skip decision: %+v", rows[i].Last)
			}
		}
	}
}

func openStoreDB(t *testing.T) (*Store, func()) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := Open(context.Background(), url)
	if err != nil {
		t.Skipf("database unavailable: %v", err)
	}
	stt := New(pool)
	if _, err := MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	cleanup := func() {
		_, _ = pool.Exec(context.Background(),
			`TRUNCATE listings, price_actions, templates, audit_log`)
		pool.Close()
	}
	return stt, cleanup
}
