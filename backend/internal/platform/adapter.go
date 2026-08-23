package platform

import (
	"context"
	"errors"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/domain"
)

// Unified errors adapters may return; scheduler/api branch on these.
var (
	ErrUnsupported     = errors.New("platform: operation unsupported by channel")
	ErrAuthExpired     = errors.New("platform: auth expired")
	ErrRateLimited     = errors.New("platform: rate limited")
	ErrPlatformBlocked = errors.New("platform: blocked by platform risk control")
	ErrPartialFailure  = errors.New("platform: partial failure")
)

// Capabilities describes channel-specific behavioral differences that upper
// layers must respect (ADR-0003).
type Capabilities struct {
	DepositDirect          bool // can set deposit explicitly (UU yes, ECO derived)
	LongLeaseThresholdDays int  // rentals longer than this are "long lease" (ECO 21)
	MaxBatchPublish        int
	MaxBatchReprice        int
	RentMaxDayMin          int // ECO requires >=8
	RentMaxDayMax          int
}

// PublishLeaseRequest is a channel-neutral lease publish instruction.
type PublishLeaseRequest struct {
	AssetRef      string // channel asset identifier
	RentPrice     float64
	LongRentPrice float64 // 0 = omit long lease
	MaxDays       int
	Deposit       float64 // used when Capabilities.DepositDirect
}

type PublishLeaseResult struct {
	AssetRef string
	GoodsRef string // channel listing id after publish
	Success  bool
	Error    string
}

type RepriceLeaseRequest struct {
	GoodsRef      string
	AssetRef      string
	RentPrice     float64
	LongRentPrice float64
	MaxDays       int
	Deposit       float64
}

type RepriceLeaseResult struct {
	GoodsRef string
	Success  bool
	Error    string
}

// Adapter is the uniform face of a trade channel (ADR-0003).
type Adapter interface {
	Channel() domain.Channel
	Caps() Capabilities

	Healthy(ctx context.Context) error

	Inventory(ctx context.Context) ([]domain.InventoryItem, error)
	LeaseShelf(ctx context.Context) ([]domain.ShelfListing, error)

	PublishLease(ctx context.Context, items []PublishLeaseRequest) ([]PublishLeaseResult, error)
	RepriceLease(ctx context.Context, items []RepriceLeaseRequest) ([]RepriceLeaseResult, error)
	Delist(ctx context.Context, goodsRefs []string) error

	// LeaseOrders returns rental orders updated since the given instant.
	LeaseOrders(ctx context.Context, since time.Time) ([]domain.LeaseOrder, error)

	Wallet(ctx context.Context) (float64, error)
}
