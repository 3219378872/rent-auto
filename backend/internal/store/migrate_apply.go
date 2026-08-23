package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

const schemaMigrationsDDL = `CREATE TABLE IF NOT EXISTS schema_migrations (
	version    text PRIMARY KEY,
	applied_at timestamptz NOT NULL DEFAULT now()
)`

// MigrateUp applies all pending migrations in order.
func MigrateUp(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
	migs, err := LoadMigrations()
	if err != nil {
		return nil, err
	}
	if _, err := pool.Exec(ctx, schemaMigrationsDDL); err != nil {
		return nil, fmt.Errorf("ensure schema_migrations: %w", err)
	}
	applied, err := AppliedVersions(ctx, pool)
	if err != nil {
		return nil, err
	}
	var newly []string
	for _, m := range migs {
		if applied[m.Version] {
			continue
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, m.UpSQL); err != nil {
			_ = tx.Rollback(ctx)
			return nil, fmt.Errorf("apply %s up: %w", m.Version, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES($1)`, m.Version); err != nil {
			_ = tx.Rollback(ctx)
			return nil, fmt.Errorf("record %s: %w", m.Version, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		newly = append(newly, m.Version)
	}
	return newly, nil
}

// MigrateDown rolls back the newest n applied migrations.
func MigrateDown(ctx context.Context, pool *pgxpool.Pool, n int) ([]string, error) {
	migs, err := LoadMigrations()
	if err != nil {
		return nil, err
	}
	byVer := map[string]Migration{}
	for _, m := range migs {
		byVer[m.Version] = m
	}
	appliedList, err := appliedVersionList(ctx, pool)
	if err != nil {
		return nil, err
	}
	var rolled []string
	for i := len(appliedList) - 1; i >= 0 && len(rolled) < n; i-- {
		v := appliedList[i]
		m, ok := byVer[v]
		if !ok {
			return nil, fmt.Errorf("applied version %s has no local file", v)
		}
		if m.DownSQL == "" {
			return nil, fmt.Errorf("version %s has no down migration", v)
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, m.DownSQL); err != nil {
			_ = tx.Rollback(ctx)
			return nil, fmt.Errorf("apply %s down: %w", v, err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM schema_migrations WHERE version=$1`, v); err != nil {
			_ = tx.Rollback(ctx)
			return nil, fmt.Errorf("unrecord %s: %w", v, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		rolled = append(rolled, v)
	}
	return rolled, nil
}

// AppliedVersions returns the set of applied migration versions.
func AppliedVersions(ctx context.Context, pool *pgxpool.Pool) (map[string]bool, error) {
	rows, err := pool.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out[v] = true
	}
	return out, rows.Err()
}

func appliedVersionList(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
	rows, err := pool.Query(ctx, `SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

var ErrNotFound = errors.New("store: not found")

// Open connects and verifies the database is reachable.
func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return pool, nil
}

// AdvisoryLock prevents double instances (single-instance deployment model).
func TryAdvisoryLock(ctx context.Context, pool *pgxpool.Pool) (unlock func(), ok bool, err error) {
	var got bool
	if err = pool.QueryRow(ctx, `SELECT pg_try_advisory_lock(918273645)`).Scan(&got); err != nil {
		return nil, false, fmt.Errorf("advisory lock: %w", err)
	}
	if !got {
		return nil, false, errors.New("another instance holds the advisory lock")
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, false, err
	}
	return func() {
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock(918273645)`)
		conn.Release()
	}, true, nil
}
