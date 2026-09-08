package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const instanceLockKey = 918273645

var ErrInstanceLockLost = errors.New("instance advisory lock lost")

type InstanceLock struct {
	mu   sync.Mutex
	conn *pgxpool.Conn
	lost bool
}

func AcquireInstanceLock(ctx context.Context, pool *pgxpool.Pool) (*InstanceLock, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	var acquired bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, instanceLockKey).Scan(&acquired); err != nil {
		conn.Release()
		return nil, err
	}
	if !acquired {
		conn.Release()
		return nil, errors.New("another instance holds the advisory lock")
	}
	return &InstanceLock{conn: conn}, nil
}

func (l *InstanceLock) Check(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.lost || l.conn == nil {
		return ErrInstanceLockLost
	}
	checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var held bool
	err := l.conn.QueryRow(checkCtx, `SELECT EXISTS (
		SELECT 1 FROM pg_locks WHERE locktype='advisory' AND pid=pg_backend_pid()
		AND classid=0 AND objid=$1 AND objsubid=1 AND granted)`, instanceLockKey).Scan(&held)
	if err != nil || !held {
		l.lost = true
		if err != nil {
			return fmt.Errorf("%w: %v", ErrInstanceLockLost, err)
		}
		return ErrInstanceLockLost
	}
	return nil
}

func (l *InstanceLock) Watch(ctx context.Context, every time.Duration) error {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := l.Check(ctx); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
		}
	}
}

func (l *InstanceLock) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.conn == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _ = l.conn.Exec(ctx, `SELECT pg_advisory_unlock($1)`, instanceLockKey)
	l.conn.Release()
	l.conn = nil
	l.lost = true
}
