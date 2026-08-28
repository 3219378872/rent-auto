package scheduler

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestRegisterAndTrigger(t *testing.T) {
	s := New(testLog())
	calls := 0
	err := s.Register(Job{Name: "t", Kind: KindInterval, Every: time.Hour, Fn: func(context.Context) error {
		calls++
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Trigger(context.Background(), "t"); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("calls=%d", calls)
	}
	sts := s.StatusList()
	if len(sts) != 1 || sts[0].Name != "t" || !sts[0].LastOK {
		t.Fatalf("status: %+v", sts)
	}

	if err := s.Trigger(context.Background(), "missing"); err == nil {
		t.Fatal("unknown job must error")
	}
	if err := s.Register(Job{Name: "t"}); err == nil {
		t.Fatal("duplicate must error")
	}
	if err := s.Register(Job{Name: "nofn", Kind: KindInterval, Every: time.Hour}); err == nil {
		t.Fatal("nil fn must error")
	}
	if err := s.Register(Job{Name: "baddaily", Kind: KindDaily, At: "25:99", Fn: func(context.Context) error { return nil }}); err == nil {
		t.Fatal("bad At must error")
	}
}

func TestTriggerErrorRecorded(t *testing.T) {
	s := New(testLog())
	_ = s.Register(Job{Name: "boom", Kind: KindInterval, Every: time.Hour, Fn: func(context.Context) error {
		return errors.New("kaboom")
	}})
	if err := s.Trigger(context.Background(), "boom"); err == nil {
		t.Fatal("expected error")
	}
	st := s.StatusList()[0]
	if st.LastOK || st.LastError != "kaboom" {
		t.Fatalf("status: %+v", st)
	}
}

func TestInitialNextDaily(t *testing.T) {
	s := New(testLog())
	now := time.Date(2026, 8, 23, 10, 0, 0, 0, time.UTC)
	j := Job{Kind: KindDaily, At: "23:30"}
	n := s.initialNext(j, now)
	want := time.Date(2026, 8, 23, 23, 30, 0, 0, time.UTC)
	if !n.Equal(want) {
		t.Fatalf("next=%v want %v", n, want)
	}
	past := time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)
	n2 := s.initialNext(j, past)
	if !n2.After(past) || n2.Day() != 24 || n2.Hour() != 23 {
		t.Fatalf("rollover next=%v", n2)
	}
}

func TestIntervalJitterBounds(t *testing.T) {
	s := New(testLog())
	base := time.Date(2026, 8, 23, 0, 0, 0, 0, time.UTC)
	j := Job{Kind: KindInterval, Every: time.Minute, Jitter: 30 * time.Second}
	for i := 0; i < 50; i++ {
		n := s.initialNext(j, base)
		d := n.Sub(base)
		if d < time.Minute || d > 90*time.Second {
			t.Fatalf("jitter out of bounds: %v", d)
		}
	}
}

func TestChannelLimiter(t *testing.T) {
	cl := NewChannelLimiter(1000)
	l1 := cl.For("uu")
	l2 := cl.For("uu")
	if l1 != l2 {
		t.Fatal("same channel must share bucket")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for i := 0; i < 5; i++ {
		if err := l1.Wait(ctx); err != nil {
			t.Fatal(err)
		}
	}
}

func testLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// orderSyncWindow: the lookback must extend to the earliest provable anchor —
// an open lease_orders row OR a leased listing (order cannot predate its
// listing) — capped at orderSyncMaxWindow. Regression for the 2026-08-27
// bootstrap gap: a pre-existing lease never entered lease_orders, so the
// default 24h window never fetched its order and its income was lost.
func TestOrderSyncWindowAnchors(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	floor := now.Add(-24 * time.Hour)

	if got := orderSyncWindow(now, nil, nil); !got.Equal(floor) {
		t.Fatalf("no anchors: want default floor %v, got %v", floor, got)
	}
	// Recent open order inside the floor still extends by the clock-skew
	// margin (2h ago − 24h margin = 26h lookback), matching the original rule.
	recent := now.Add(-2 * time.Hour)
	wantRecent := recent.Add(-24 * time.Hour)
	if got := orderSyncWindow(now, &recent, nil); !got.Equal(wantRecent) {
		t.Fatalf("recent anchor: want %v, got %v", wantRecent, got)
	}
	// Old open order → extend to it (− margin).
	old := now.Add(-96 * time.Hour)
	wantOld := old.Add(-24 * time.Hour)
	if got := orderSyncWindow(now, &old, nil); !got.Equal(wantOld) {
		t.Fatalf("open order anchor: want %v, got %v", wantOld, got)
	}
	// Only a leased listing → extend from the listing anchor (90d long lease).
	leased := now.Add(-90 * 24 * time.Hour)
	wantLeased := leased.Add(-24 * time.Hour)
	if got := orderSyncWindow(now, nil, &leased); !got.Equal(wantLeased) {
		t.Fatalf("leased listing anchor: want %v, got %v", wantLeased, got)
	}
	// Both → the older (leased, 90d) anchor wins.
	if got := orderSyncWindow(now, &old, &leased); !got.Equal(wantLeased) {
		t.Fatalf("older anchor must win: want %v, got %v", wantLeased, got)
	}
	// Hard cap at 100d.
	ancient := now.Add(-400 * 24 * time.Hour)
	capWant := now.Add(-100 * 24 * time.Hour)
	if got := orderSyncWindow(now, &ancient, nil); !got.Equal(capWant) {
		t.Fatalf("cap: want %v, got %v", capWant, got)
	}
}
