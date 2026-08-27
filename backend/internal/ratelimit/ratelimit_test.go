package ratelimit

import (
	"context"
	"testing"
	"time"
)

func TestWaitImmediateOnFreshBucket(t *testing.T) {
	b := New(1000)
	start := time.Now()
	if err := b.Wait(context.Background()); err != nil {
		t.Fatalf("wait: %v", err)
	}
	if d := time.Since(start); d > 50*time.Millisecond {
		t.Fatalf("first wait took %v, want immediate", d)
	}
}

func TestWaitPacesToInterval(t *testing.T) {
	b := New(50) // 20ms interval
	if err := b.Wait(context.Background()); err != nil {
		t.Fatalf("wait: %v", err)
	}
	start := time.Now()
	if err := b.Wait(context.Background()); err != nil {
		t.Fatalf("wait: %v", err)
	}
	if d := time.Since(start); d < 5*time.Millisecond {
		t.Fatalf("second wait took %v, want ≥ interval pacing", d)
	}
}

func TestWaitRespectsCancelledContext(t *testing.T) {
	b := New(0.5) // 2s interval: the second call must abort, not sleep it out
	if err := b.Wait(context.Background()); err != nil {
		t.Fatalf("wait: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := b.Wait(ctx); err == nil {
		t.Fatal("want context error on cancelled wait")
	}
}

func TestNonPositiveRpsFallsBackToDefault(t *testing.T) {
	if b := New(0); b.interval != 500*time.Millisecond {
		t.Fatalf("New(0) interval=%v, want 500ms", b.interval)
	}
	if b := New(-3); b.interval != 500*time.Millisecond {
		t.Fatalf("New(-3) interval=%v, want 500ms", b.interval)
	}
}
