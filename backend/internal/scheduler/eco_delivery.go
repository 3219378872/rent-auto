// Package scheduler — ECO delivery loop: Steamauto-aligned four-step flow.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/domain"
	"github.com/3219378872/rent-auto/backend/internal/platform"
	"github.com/3219378872/rent-auto/backend/internal/platform/eco"
)

// EcoDeliveryDeps abstracts the two platform clients for testability.
type EcoDeliveryDeps struct {
	Eco interface {
		SellerOrderList(ctx context.Context, start, end time.Time, detailsState *int, steamID string) ([]eco.SellerOrder, error)
		SendOffer(ctx context.Context, orderNum string) (*eco.SendOfferResult, error)
		Detail(ctx context.Context, orderNum string) (*eco.SellerOrderDetail, error)
		SellerRentOrderList(ctx context.Context, start, end time.Time, status []int) ([]eco.SellerRentOrder, error)
		SellerRentOrderDetail(ctx context.Context, orderNum string) (*eco.SellerRentOrderDetailResult, error)
	}
	Steam interface {
		AcceptTradeOffer(ctx context.Context, offerID string) (bool, error)
	}
	Audit func(ctx context.Context, e domain.AuditEntry)
	Log   interface {
		Info(string, ...any)
		Warn(string, ...any)
	}

	mu       sync.Mutex
	accepted map[string]bool // TradeOfferIds already handled
}

const (
	ecoLookbackDays         = 30
	detailsStateWaitDeliver = 8     // ECO DetailsState filter: 待发货
	orderStateOfferNotSent  = 1     // OrderStateCode: 报价未发送
	acceptedSetMax          = 10000 // cross-cycle accepted-offer memory cap

	// Rent delivery (api-220956683/684): rent orders NEVER appear in the
	// sale-order SellerOrderList view (real-machine finding 2026-08-27) —
	// they are listed by /Api/Rent/SellerRentOrderList with Status
	// 等待发货=2, and the platform pre-creates the rental trade offer whose
	// id is exposed via SellerRentOrderDetail.OfferId.
	rentStatusWaitDeliver = 2
	rentSendRoleSeller    = 2 // SendOfferRole: 卖家=2
)

// RunECODelivery executes the four-step fulfilment loop for every
// wait-deliver order: send offer → resolve TradeOfferId → steam accept → confirm.
func (d *EcoDeliveryDeps) RunECODelivery(ctx context.Context) error {
	if d.Eco == nil || d.Steam == nil {
		return nil
	}
	d.mu.Lock()
	if d.accepted == nil {
		d.accepted = map[string]bool{}
	}
	d.mu.Unlock()

	end := time.Now().AddDate(0, 0, 1)
	start := end.AddDate(0, 0, -ecoLookbackDays-1)
	state := detailsStateWaitDeliver
	orders, err := d.Eco.SellerOrderList(ctx, start, end, &state, "")
	if err != nil {
		if errors.Is(err, platform.ErrUnsupported) {
			return nil // ECO not configured — nothing to deliver
		}
		return fmt.Errorf("eco: order list: %w", err)
	}
	for _, o := range orders {
		if o.OrderStateCode == orderStateOfferNotSent {
			if _, serr := d.Eco.SendOffer(ctx, o.OrderNum); serr != nil {
				d.warn(ctx, "order.send_offer_failed", o.OrderNum, serr.Error())
				continue
			}
			d.info(ctx, "offer sent", o.OrderNum)
		}
		detail, derr := d.Eco.Detail(ctx, o.OrderNum)
		if derr != nil {
			d.warn(ctx, "order.detail_failed", o.OrderNum, derr.Error())
			continue
		}
		if detail.TradeOfferID == "" {
			d.info(ctx, "trade offer not ready yet", o.OrderNum)
			continue
		}
		d.mu.Lock()
		seen := d.accepted[detail.TradeOfferID]
		d.mu.Unlock()
		if seen {
			continue
		}
		ok, aerr := d.Steam.AcceptTradeOffer(ctx, detail.TradeOfferID)
		if aerr != nil {
			// not marked as handled: retried on the next cycle
			d.warn(ctx, "order.accept_offer_failed", detail.TradeOfferID, aerr.Error())
			continue
		}
		if ok {
			d.markAccepted(ctx, detail.TradeOfferID, o.GoodsName, o.OrderNum)
		}
	}
	return d.runRentDelivery(ctx, start, end)
}

// runRentDelivery is the rent-order fulfilment pass. The platform creates the
// rental trade offer itself; if SendOfferRole says the seller must send and
// no offer exists yet, SellerSendOffer (shared with the sale flow) triggers
// it. Otherwise the loop just accepts the offer id from the rent detail.
func (d *EcoDeliveryDeps) runRentDelivery(ctx context.Context, start, end time.Time) error {
	rentOrders, err := d.Eco.SellerRentOrderList(ctx, start, end, []int{rentStatusWaitDeliver})
	if err != nil {
		if errors.Is(err, platform.ErrUnsupported) {
			return nil
		}
		return fmt.Errorf("eco: rent order list: %w", err)
	}
	for _, o := range rentOrders {
		detail, derr := d.Eco.SellerRentOrderDetail(ctx, o.OrderNum)
		if derr != nil {
			d.warn(ctx, "rent.detail_failed", o.OrderNum, derr.Error())
			continue
		}
		if detail.OfferID == "" {
			if detail.SendOfferRole == rentSendRoleSeller {
				if _, serr := d.Eco.SendOffer(ctx, o.OrderNum); serr != nil {
					d.warn(ctx, "rent.send_offer_failed", o.OrderNum, serr.Error())
				} else {
					d.info(ctx, "rent offer sent", o.OrderNum)
				}
				continue // offer id resolved on the next cycle
			}
			d.info(ctx, "rent trade offer not ready yet", o.OrderNum)
			continue
		}
		d.mu.Lock()
		seen := d.accepted[detail.OfferID]
		d.mu.Unlock()
		if seen {
			continue
		}
		ok, aerr := d.Steam.AcceptTradeOffer(ctx, detail.OfferID)
		if aerr != nil {
			d.warn(ctx, "rent.accept_offer_failed", detail.OfferID, aerr.Error())
			continue
		}
		if ok {
			d.markAccepted(ctx, detail.OfferID, detail.HashName, o.OrderNum)
		}
	}
	return nil
}

func (d *EcoDeliveryDeps) markAccepted(ctx context.Context, offerID, goodsName, orderNum string) {
	d.mu.Lock()
	d.accepted[offerID] = true
	// The accepted-set grows with every delivered order for the
	// process lifetime. Volume is bounded by real deliveries, but a
	// cap keeps the map from silently accumulating forever; on
	// eviction, a re-accept attempt is idempotent (offer already
	// accepted upstream) and merely logs.
	if len(d.accepted) > acceptedSetMax {
		d.accepted = map[string]bool{offerID: true}
		d.info(ctx, "accepted set overflow, reset", fmt.Sprintf("%d offers", acceptedSetMax))
	}
	d.mu.Unlock()
	if d.Audit != nil {
		d.Audit(ctx, domain.AuditEntry{
			Time: time.Now().UTC(), Actor: "system",
			Action: "order.delivered", Channel: "eco",
			Target: goodsName,
			Detail: map[string]any{"order": orderNum, "offer": offerID},
		})
	}
	d.info(ctx, "delivered", fmt.Sprintf("%s/%s", orderNum, offerID))
}

func (d *EcoDeliveryDeps) info(_ context.Context, msg, target string) {
	if d.Log != nil {
		d.Log.Info("eco delivery: "+msg, "target", target)
	}
}

func (d *EcoDeliveryDeps) warn(ctx context.Context, action, target, errMsg string) {
	if d.Log != nil {
		d.Log.Warn("eco delivery: "+action, "target", target, "err", errMsg)
	}
	if d.Audit != nil {
		d.Audit(ctx, domain.AuditEntry{
			Time: time.Now().UTC(), Actor: "system", Action: action,
			Channel: "eco", Target: target, Detail: map[string]any{"error": errMsg},
		})
	}
}
