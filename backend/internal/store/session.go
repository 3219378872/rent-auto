package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

const sessionEpochKey = "jwt_session_epoch"

func (s *Store) SessionEpoch(ctx context.Context) (int64, error) {
	setting, err := s.GetSetting(ctx, sessionEpochKey)
	if errors.Is(err, ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if setting.ValuePlain == nil {
		return 0, errors.New("session epoch has no value")
	}
	var epoch *int64
	if err := json.Unmarshal([]byte(*setting.ValuePlain), &epoch); err != nil || epoch == nil || *epoch < 0 {
		return 0, errors.New("session epoch is invalid")
	}
	return *epoch, nil
}

const bumpEpochSQL = `INSERT INTO app_settings(key,value_plain) VALUES('jwt_session_epoch','1'::jsonb)
	ON CONFLICT(key) DO UPDATE SET
	  value_plain=to_jsonb((app_settings.value_plain #>> '{}')::bigint+1),updated_at=now()
	WHERE jsonb_typeof(app_settings.value_plain)='number'
	  AND (app_settings.value_plain #>> '{}') ~ '^[0-9]+$'
	RETURNING value_plain`

type epochQuery interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func bumpEpoch(ctx context.Context, q epochQuery) (int64, error) {
	var epoch int64
	if err := q.QueryRow(ctx, bumpEpochSQL).Scan(&epoch); err != nil {
		return 0, fmt.Errorf("advance session epoch: %w", err)
	}
	return epoch, nil
}

func (s *Store) BumpSessionEpoch(ctx context.Context) (int64, error) {
	return bumpEpoch(ctx, s.Pool)
}

func (s *Store) AdminPasswordHash(ctx context.Context) (string, error) {
	setting, err := s.GetSetting(ctx, "admin_password_hash")
	if err != nil {
		return "", err
	}
	if len(setting.ValueEnc) == 0 {
		return "", errors.New("admin password hash missing")
	}
	return string(setting.ValueEnc), nil
}

var ErrPasswordChanged = errors.New("admin password changed concurrently")

func (s *Store) ChangeAdminPassword(ctx context.Context, expectedHash, newHash string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `UPDATE app_settings SET value_enc=$2,value_plain=NULL,updated_at=now()
		WHERE key='admin_password_hash' AND value_enc=$1`, []byte(expectedHash), []byte(newHash))
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrPasswordChanged
	}
	if _, err := bumpEpoch(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
