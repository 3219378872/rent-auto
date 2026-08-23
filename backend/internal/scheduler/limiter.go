package scheduler

import (
	"context"
	"sync"
	"time"
)

// tokenBucket is a minimal rps limiter (no external dependency).
type tokenBucket struct {
	mu       sync.Mutex
	interval time.Duration
	last     time.Time
}

func newTokenBucket(rps float64) *tokenBucket {
	if rps <= 0 {
		rps = 2
	}
	return &tokenBucket{interval: time.Duration(float64(time.Second) / rps), last: time.Now().Add(-time.Hour)}
}

func (b *tokenBucket) Wait(ctx context.Context) error {
	for {
		b.mu.Lock()
		now := time.Now()
		next := b.last.Add(b.interval)
		if !next.After(now) {
			b.last = now
			b.mu.Unlock()
			return nil
		}
		wait := next.Sub(now)
		b.mu.Unlock()
		if err := ctx.Err(); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}
