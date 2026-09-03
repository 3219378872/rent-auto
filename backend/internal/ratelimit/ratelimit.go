// Package ratelimit provides token-bucket pacers for platform calls.
package ratelimit

import (
	"context"
	"sync"
	"time"
)

// Bucket paces calls to at most rps requests per second.
type Bucket struct {
	mu       sync.Mutex
	interval time.Duration
	last     time.Time
}

func New(rps float64) *Bucket {
	if rps <= 0 {
		rps = 2
	}
	return &Bucket{interval: time.Duration(float64(time.Second) / rps), last: time.Now().Add(-time.Hour)}
}

func (b *Bucket) Wait(ctx context.Context) error {
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
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
