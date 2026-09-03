// Package channels — registry concurrency regression test.
//
// The registry is read from panel/scheduler goroutines while credential
// updates (SetUUToken/SetECOCreds/Refresh/SetUUHTTPClient/SetAuditFn) write
// it. Every shared field must be mutex-guarded; this test hammers the
// lock-protected surface from many goroutines and must pass under -race.
package channels

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/domain"
)

type raceNoopLimiter struct{}

func (raceNoopLimiter) Wait(context.Context) error { return nil }

func TestRegistryConcurrentAccess(t *testing.T) {
	r := NewRegistry(nil, nil, slog.Default())
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				r.SetAuditFn(func(context.Context, domain.AuditEntry) {})
				r.audit(ctx, domain.AuditEntry{Time: time.Now().UTC(), Actor: "test",
					Action: "race.ping", Channel: "uu"})
				r.SetLimiter(domain.ChannelUU, raceNoopLimiter{})
				r.SetLimiter(domain.ChannelECO, raceNoopLimiter{})
				_ = r.uuOptions()
				r.SetUUHTTPClient(&http.Client{})
				_ = r.uuOptions()
				r.SetUUHTTPClient(nil)
				r.dropUU()
				r.dropECO()
				_, _ = r.Get(domain.ChannelUU)
				_, _ = r.Get(domain.ChannelECO)
				_ = r.All()
				_ = r.Health(ctx)
				_ = r.EcoOrderClient()
			}
		}(i)
	}
	wg.Wait()
}

func TestSteamSessionAuditConcurrent(t *testing.T) {
	s := NewSteamSession(nil, nil, slog.Default())
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				s.SetAuditFn(func(context.Context, domain.AuditEntry) {})
				if fn := s.auditFnCopy(); fn != nil {
					fn(ctx, domain.AuditEntry{Time: time.Now().UTC(), Actor: "test",
						Action: "race.ping", Channel: "steam"})
				}
				_ = s.Health(ctx)
			}
		}()
	}
	wg.Wait()
}
