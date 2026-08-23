// Package scheduler — ECO delivery loop: Steamauto-aligned four-step flow.
package scheduler

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/domain"
	"github.com/3219378872/rent-auto/backend/internal/platform/eco"
	"github.com/3219378872/rent-auto/backend/internal/platform/steam"
)

// EcoDeliveryDeps abstracts the two platform clients for testability.
type EcoDeliveryDeps struct {
	Eco interface {
		SellerOrderList(ctx context.Context, start, end time.Time, detailsState *int, steamID string) ([]eco.SellerOrder, error)
		SendOffer(ctx context.Context, orderNum string) (*eco.SendOfferResult, error)
		Detail(ctx context.Context, orderNum string) (*eco.SellerOrderDetail, error)
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
	detailsStateWaitDeliver = 8 // ECO DetailsState filter: 待发货
	orderStateOfferNotSent  = 1 // OrderStateCode: 报价未发送
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
		if !seen {
			d.accepted[detail.TradeOfferID] = true
		}
		d.mu.Unlock()
		if seen {
			continue
		}
		ok, aerr := d.Steam.AcceptTradeOffer(ctx, detail.TradeOfferID)
		if aerr != nil {
			d.warn(ctx, "order.accept_offer_failed", detail.TradeOfferID, aerr.Error())
			continue
		}
		if ok && d.Audit != nil {
			d.Audit(ctx, domain.AuditEntry{
				Time: time.Now().UTC(), Actor: "system",
				Action: "order.delivered", Channel: "eco",
				Target: o.GoodsName,
				Detail: map[string]any{"order": o.OrderNum, "offer": detail.TradeOfferID},
			})
		}
		d.info(ctx, "delivered", fmt.Sprintf("%s/%s", o.OrderNum, detail.TradeOfferID))
	}
	return nil
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

var (
	_ = eco.SellerOrder{}
	_ = steam.ErrSevenDayHold
)
