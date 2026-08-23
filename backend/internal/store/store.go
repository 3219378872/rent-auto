package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/3219378872/rent-auto/backend/internal/domain"
)

// Store wraps the pool with typed queries. Handlers depend on this interface only.
type Store struct {
	Pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store { return &Store{Pool: pool} }

func (s *Store) Close() { s.Pool.Close() }

// ---- app_settings ----

type Setting struct {
	Key        string
	ValueEnc   []byte  // encrypted credential material (nullable)
	ValuePlain *string // plain JSON scalar/object (nullable)
	UpdatedAt  time.Time
}

const qGetSetting = `SELECT key, value_enc, value_plain, updated_at FROM app_settings WHERE key=$1`

func (s *Store) GetSetting(ctx context.Context, key string) (*Setting, error) {
	row := s.Pool.QueryRow(ctx, qGetSetting, key)
	var st Setting
	var enc []byte
	var plain []byte
	err := row.Scan(&st.Key, &enc, &plain, &st.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get setting %s: %w", key, err)
	}
	st.ValueEnc = enc
	if plain != nil {
		p := string(plain)
		st.ValuePlain = &p
	}
	return &st, nil
}

// UpsertSettingPlain stores a plain JSON value.
func (s *Store) UpsertSettingPlain(ctx context.Context, key string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal setting %s: %w", key, err)
	}
	_, err = s.Pool.Exec(ctx,
		`INSERT INTO app_settings(key, value_plain) VALUES($1,$2)
		 ON CONFLICT(key) DO UPDATE SET value_plain=EXCLUDED.value_plain, updated_at=now()`,
		key, b)
	if err != nil {
		return fmt.Errorf("upsert setting %s: %w", key, err)
	}
	return nil
}

// UpsertSettingEnc stores encrypted bytes. Any stale plaintext twin is cleared
// so secrets never linger in value_plain after an encrypted rewrite.
func (s *Store) UpsertSettingEnc(ctx context.Context, key string, enc []byte) error {
	_, err := s.Pool.Exec(ctx,
		`INSERT INTO app_settings(key, value_enc) VALUES($1,$2)
		 ON CONFLICT(key) DO UPDATE SET value_enc=EXCLUDED.value_enc, value_plain=NULL, updated_at=now()`,
		key, enc)
	if err != nil {
		return fmt.Errorf("upsert setting %s: %w", key, err)
	}
	return nil
}

// ---- audit_log ----

func (s *Store) InsertAudit(ctx context.Context, e domain.AuditEntry) error {
	var detail []byte
	if e.Detail != nil {
		b, err := json.Marshal(e.Detail)
		if err != nil {
			return fmt.Errorf("audit marshal: %w", err)
		}
		detail = b
	}
	_, err := s.Pool.Exec(ctx,
		`INSERT INTO audit_log(ts, actor, channel, action, target, detail) VALUES($1,$2,NULLIF($3,''),$4,NULLIF($5,''),$6)`,
		e.Time, e.Actor, e.Channel, e.Action, e.Target, detail)
	if err != nil {
		return fmt.Errorf("insert audit: %w", err)
	}
	return nil
}

type AuditFilter struct {
	Action  string
	Channel string
	Before  time.Time
	Limit   int
}

func (s *Store) ListAudit(ctx context.Context, f AuditFilter) ([]domain.AuditEntry, int, error) {
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	where := "WHERE true"
	args := []any{}
	if f.Action != "" {
		args = append(args, f.Action)
		where += fmt.Sprintf(" AND action=$%d", len(args))
	}
	if f.Channel != "" {
		args = append(args, f.Channel)
		where += fmt.Sprintf(" AND channel=$%d", len(args))
	}
	if !f.Before.IsZero() {
		args = append(args, f.Before)
		where += fmt.Sprintf(" AND ts < $%d", len(args))
	}
	var total int
	if err := s.Pool.QueryRow(ctx, "SELECT count(*) FROM audit_log "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	q := `SELECT ts, actor, coalesce(channel,''), action, coalesce(target,''), coalesce(detail::text,'')
	      FROM audit_log ` + where + fmt.Sprintf(" ORDER BY ts DESC LIMIT %d", limit)
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []domain.AuditEntry
	for rows.Next() {
		var (
			e          domain.AuditEntry
			ch, tg     string
			detailText string
		)
		if err := rows.Scan(&e.Time, &e.Actor, &ch, &e.Action, &tg, &detailText); err != nil {
			return nil, 0, err
		}
		e.Channel, e.Target = ch, tg
		if detailText != "" {
			_ = json.Unmarshal([]byte(detailText), &e.Detail)
		}
		out = append(out, e)
	}
	return out, total, rows.Err()
}
