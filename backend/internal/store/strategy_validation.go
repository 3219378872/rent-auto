package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Global and template edits share the singleton row lock, so validation sees
// exactly the parameter combination that the transaction will commit.
func lockGlobalStrategy(ctx context.Context, tx pgx.Tx) (int64, json.RawMessage, error) {
	var id int64
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT id, params FROM strategies WHERE scope='global' FOR UPDATE`).Scan(&id, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil, ErrNotFound
	}
	return id, raw, err
}

func (s *Store) UpdateGlobalStrategyChecked(ctx context.Context, id int64, p StrategyGlobalPatch,
	validate func(json.RawMessage, []json.RawMessage) error) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin strategy tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	lockedID, global, err := lockGlobalStrategy(ctx, tx)
	if err != nil {
		return err
	}
	if id != lockedID {
		return ErrNotFound
	}
	if p.Params != nil {
		global = p.Params
	}
	if validate != nil {
		rows, err := tx.Query(ctx, `SELECT params FROM strategies WHERE scope='template' AND enabled ORDER BY id`)
		if err != nil {
			return err
		}
		var templates []json.RawMessage
		for rows.Next() {
			var raw []byte
			if err := rows.Scan(&raw); err != nil {
				rows.Close()
				return err
			}
			templates = append(templates, json.RawMessage(raw))
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if err := validate(global, templates); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE strategies SET params=$2,
		channel_route=COALESCE($3,channel_route),
		real_execution_enabled=COALESCE($4,real_execution_enabled),
		updated_by='user',updated_at=now() WHERE id=$1`, id, []byte(global), p.Route, p.RealEnabled)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) UpsertTemplateStrategyChecked(ctx context.Context, ts TemplateStrategy,
	validate func(json.RawMessage, json.RawMessage) error) (int64, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, global, err := lockGlobalStrategy(ctx, tx)
	if err != nil {
		return 0, err
	}
	if validate != nil {
		if err := validate(global, ts.Params); err != nil {
			return 0, err
		}
	}
	var id int64
	err = tx.QueryRow(ctx,
		`INSERT INTO strategies(name,scope,hash_name,channel_route,params,priority,
		                        real_execution_enabled,updated_by)
		 VALUES($1,'template',$2,$3,$4,$5,COALESCE($6,false),'user')
		 ON CONFLICT (scope,hash_name) WHERE scope='template' DO UPDATE SET
		   channel_route=EXCLUDED.channel_route,params=EXCLUDED.params,
		   priority=EXCLUDED.priority,
		   real_execution_enabled=COALESCE($6,strategies.real_execution_enabled),
		   enabled=true,updated_by='user',updated_at=now()
		 RETURNING id`, "tpl:"+ts.HashName, ts.HashName, ts.Route, []byte(ts.Params), ts.Priority, ts.RealEnabled).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("upsert template strategy %s: %w", ts.HashName, err)
	}
	return id, tx.Commit(ctx)
}
