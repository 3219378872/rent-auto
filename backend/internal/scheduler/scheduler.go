// Package scheduler runs recurring automation jobs with jitter, per-channel
// rate limiting and a dry-run switch (functional-spec §3).
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"sync"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/platform"
)

type JobKind int

const (
	KindInterval JobKind = iota
	KindDaily
)

type Job struct {
	Name   string
	Kind   JobKind
	Every  time.Duration // interval jobs
	At     string        // "HH:MM" daily jobs
	Jitter time.Duration
	Fn     func(ctx context.Context) error
}

// Status is exposed to the panel.
type Status struct {
	Name      string     `json:"name"`
	NextRun   time.Time  `json:"next_run"`
	LastRun   *time.Time `json:"last_run,omitempty"`
	LastOK    bool       `json:"last_ok"`
	LastError string     `json:"last_error,omitempty"`
	Running   bool       `json:"running"`
}

type entry struct {
	job        Job
	next       time.Time
	last       *time.Time
	lastOK     bool
	lastErr    string
	running    bool
	failStreak int
}

type Scheduler struct {
	mu    sync.Mutex
	log   *slog.Logger
	jobs  map[string]*entry
	order []string
	stop  chan struct{}
	done  sync.WaitGroup
}

func New(log *slog.Logger) *Scheduler {
	return &Scheduler{log: log, jobs: map[string]*entry{}, stop: make(chan struct{})}
}

func (s *Scheduler) Register(j Job) error {
	if j.Fn == nil || j.Name == "" {
		return errors.New("scheduler: job needs name and fn")
	}
	if j.Kind == KindDaily {
		var h, m int
		if len(j.At) != 5 || j.At[2] != ':' {
			return fmt.Errorf("scheduler: daily job %s needs At HH:MM", j.Name)
		}
		if _, err := fmt.Sscanf(j.At, "%d:%d", &h, &m); err != nil || h < 0 || h > 23 || m < 0 || m > 59 {
			return fmt.Errorf("scheduler: daily job %s has invalid At %q", j.Name, j.At)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.jobs[j.Name]; dup {
		return fmt.Errorf("scheduler: duplicate job %s", j.Name)
	}
	e := &entry{job: j, next: s.initialNext(j, time.Now())}
	s.jobs[j.Name] = e
	s.order = append(s.order, j.Name)
	return nil
}

func (s *Scheduler) initialNext(j Job, now time.Time) time.Time {
	if j.Kind == KindInterval {
		base := now.Add(j.Every)
		if j.Jitter > 0 {
			base = base.Add(time.Duration(rand.Int63n(int64(j.Jitter))))
		}
		return base
	}
	h, m := 0, 0
	_, _ = fmt.Sscanf(j.At, "%d:%d", &h, &m)
	n := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, now.Location())
	if !n.After(now) {
		n = n.AddDate(0, 0, 1)
	}
	return n
}

// Start blocks until Stop is called; ticks every 15s.
func (s *Scheduler) Start(ctx context.Context) {
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stop:
			return
		case now := <-tick.C:
			s.runDue(ctx, now)
		}
	}
}

func (s *Scheduler) runDue(ctx context.Context, now time.Time) {
	s.mu.Lock()
	var due []*entry
	for _, name := range s.order {
		e := s.jobs[name]
		if !e.running && !e.next.After(now) {
			e.running = true
			due = append(due, e)
		}
	}
	s.mu.Unlock()
	for _, e := range due {
		e := e
		s.done.Add(1)
		go func() {
			defer s.done.Done()
			cctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
			defer cancel()
			err := safeCall(cctx, e.job.Fn)
			s.mu.Lock()
			defer s.mu.Unlock()
			e.running = false
			t := now
			e.last = &t
			e.lastOK = err == nil
			e.lastErr = ""
			if err != nil {
				e.lastErr = err.Error()
				e.failStreak++
			} else {
				e.failStreak = 0
			}
			e.next = s.initialNext(e.job, time.Now())
		}()
	}
}

func safeCall(ctx context.Context, fn func(context.Context) (err error)) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return fn(ctx)
}

func (s *Scheduler) Stop() { close(s.stop); s.done.Wait() }

// Trigger runs one job immediately (manual run from the panel).
func (s *Scheduler) Trigger(ctx context.Context, name string) error {
	s.mu.Lock()
	e, ok := s.jobs[name]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("scheduler: unknown job %q", name)
	}
	if e.running {
		s.mu.Unlock()
		return errors.New("scheduler: job already running")
	}
	e.running = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); e.running = false; s.mu.Unlock() }()
	err := safeCall(ctx, e.job.Fn)
	s.mu.Lock()
	t := time.Now()
	e.last = &t
	e.lastOK = err == nil
	e.lastErr = ""
	if err != nil {
		e.lastErr = err.Error()
	}
	e.next = s.initialNext(e.job, time.Now())
	s.mu.Unlock()
	return err
}

func (s *Scheduler) StatusList() []Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Status, 0, len(s.order))
	for _, name := range s.order {
		e := s.jobs[name]
		out = append(out, Status{
			Name: name, NextRun: e.next, LastRun: e.last,
			LastOK: e.lastOK, LastError: e.lastErr, Running: e.running,
		})
	}
	return out
}

// ---- rate limiting ----

// ChannelLimiter wraps two token buckets keyed by channel.
type ChannelLimiter struct {
	mu  sync.Mutex
	bkt map[string]limiter
	rps float64
}

type limiter interface{ Wait(context.Context) error }

func NewChannelLimiter(rps float64) *ChannelLimiter {
	return &ChannelLimiter{bkt: map[string]limiter{}, rps: rps}
}

func (c *ChannelLimiter) For(channel string) platform.Limiter {
	c.mu.Lock()
	defer c.mu.Unlock()
	if l, ok := c.bkt[channel]; ok {
		return l
	}
	l := newTokenBucket(c.rps)
	c.bkt[channel] = l
	return l
}
