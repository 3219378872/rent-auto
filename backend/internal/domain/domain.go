// Package domain holds cross-module types shared by all layers.
// It must not import any other internal package.
package domain

import (
	"crypto/rand"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"math/big"
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

// ---- unified business records (channel-neutral) ----

// InventoryItem is one Steam asset owned by us on some channel.
type InventoryItem struct {
	Channel     Channel `json:"channel"`
	AssetID     string  `json:"asset_id"`
	HashName    string  `json:"hash_name"`
	DisplayName string  `json:"display_name"`
	TemplateID  int64   `json:"template_id,omitempty"` // UU template id
	MarkPrice   float64 `json:"mark_price"`
	Tradable    bool    `json:"tradable"`
	Status      string  `json:"status"` // in_stock|listed|leased|locked|sold
	Abrade      float64 `json:"abrade,omitempty"`
}

// ShelfListing is one item currently on our lease shelf.
type ShelfListing struct {
	Channel       Channel   `json:"channel"`
	GoodsRef      string    `json:"goods_ref"` // commodityId(UU) / GoodsNum(ECO)
	AssetID       string    `json:"asset_id"`
	HashName      string    `json:"hash_name"`
	DisplayName   string    `json:"display_name"`
	TemplateID    int64     `json:"template_id,omitempty"`
	RentPrice     float64   `json:"rent_price"`
	LongRentPrice float64   `json:"long_rent_price,omitempty"`
	MaxDays       int       `json:"max_days"`
	Deposit       float64   `json:"deposit"`
	MarkPrice     float64   `json:"mark_price"`
	Leased        bool      `json:"leased"`
	ListedAt      time.Time `json:"listed_at,omitempty"`
}

// LeaseOrder is a rental order in unified state-machine terms (data-model.md).
type LeaseOrder struct {
	Channel   Channel   `json:"channel"`
	OrderRef  string    `json:"order_ref"`
	AssetID   string    `json:"asset_id,omitempty"`
	HashName  string    `json:"hash_name"`
	OrderType string    `json:"order_type"` // short|long|buyout
	Status    string    `json:"status"`
	RentDays  int       `json:"rent_days"`
	RentPrice float64   `json:"rent_price"`
	Amount    float64   `json:"order_amount"`
	Deposits  float64   `json:"deposits"`
	StartedAt time.Time `json:"started_at,omitempty"`
	DueAt     time.Time `json:"due_at,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
	Raw       JSONB     `json:"-"`
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

const sessionAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// RandomSessionID generates the UU SMS-login session identifier. Session IDs
// gate who may complete a login challenge, so they come from crypto/rand —
// math/rand is only sanctioned for non-security identifiers (gosec G404).
func RandomSessionID() string {
	out := make([]byte, 16)
	span := big.NewInt(int64(len(sessionAlphabet)))
	for i := range out {
		n, err := rand.Int(rand.Reader, span)
		if err != nil {
			// crypto/rand failure is unrecoverable system-wide; fall back to a
			// fixed marker so callers see an obviously invalid session id
			// instead of a silently predictable one.
			panic(fmt.Errorf("domain: crypto/rand unavailable: %w", err))
		}
		out[i] = sessionAlphabet[n.Int64()]
	}
	return string(out)
}
