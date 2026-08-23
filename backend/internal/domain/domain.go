// Package domain holds cross-module types shared by all layers.
// It must not import any other internal package.
package domain

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"
)

type Channel string

const (
	ChannelUU  Channel = "uu"
	ChannelECO Channel = "eco"
)

func (c Channel) Valid() bool {
	return c == ChannelUU || c == ChannelECO
}

// Money amounts are float64 in business logic and rounded to 2 decimals via Round2
// before persistence or display (see pricing.Round2).

type AuditEntry struct {
	Time    time.Time      `json:"ts"`
	Actor   string         `json:"actor"` // "system" | "user:<name>"
	Action  string         `json:"action"`
	Channel string         `json:"channel,omitempty"`
	Target  string         `json:"target,omitempty"`
	Detail  map[string]any `json:"detail,omitempty"`
}

// JSONB implements driver.Valuer / sql.Scanner for pgx jsonb columns.
type JSONB map[string]any

func (j JSONB) Value() (driver.Value, error) {
	if j == nil {
		return nil, nil
	}
	return json.Marshal(map[string]any(j))
}

func (j *JSONB) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*j = nil
	case []byte:
		return json.Unmarshal(v, j)
	case string:
		return json.Unmarshal([]byte(v), j)
	default:
		return fmt.Errorf("jsonb scan: unsupported type %T", src)
	}
	return nil
}
