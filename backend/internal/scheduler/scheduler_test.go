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
