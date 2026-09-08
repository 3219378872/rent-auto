package scheduler

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/domain"
	"github.com/3219378872/rent-auto/backend/internal/platform"
	"github.com/3219378872/rent-auto/backend/internal/platform/eco"
)

// ---- fakes ----

type fakeEco struct {
	orders        []eco.SellerOrder
	sent          []string
	details       map[string]eco.SellerOrderDetail
	sendErr       map[string]error
	rentOrders    []eco.SellerRentOrder
	rentDetail    map[string]eco.SellerRentOrderDetailResult
	detailErr     map[string]error
	rentDetailErr map[string]error
	sendAttempts  []string
	rentCalls     int
}

func (f *fakeEco) SellerOrderList(context.Context, time.Time, time.Time, *int, string) ([]eco.SellerOrder, error) {
	return f.orders, nil
}

func (f *fakeEco) SendOffer(_ context.Context, orderNum string) (*eco.SendOfferResult, error) {
	f.sendAttempts = append(f.sendAttempts, orderNum)
	if e, ok := f.sendErr[orderNum]; ok {
		return nil, e
	}
	f.sent = append(f.sent, orderNum)
	return &eco.SendOfferResult{OrderNum: orderNum}, nil
}

func (f *fakeEco) Detail(_ context.Context, orderNum string) (*eco.SellerOrderDetail, error) {
	if err := f.detailErr[orderNum]; err != nil {
		return nil, err
	}
	if d, ok := f.details[orderNum]; ok {
		return &d, nil
	}
	return &eco.SellerOrderDetail{}, nil
}

func (f *fakeEco) SellerRentOrderList(context.Context, time.Time, time.Time, []int) ([]eco.SellerRentOrder, error) {
	f.rentCalls++
	return f.rentOrders, nil
}

func (f *fakeEco) SellerRentOrderDetail(_ context.Context, orderNum string) (*eco.SellerRentOrderDetailResult, error) {
	if err := f.rentDetailErr[orderNum]; err != nil {
		return nil, err
	}
	if d, ok := f.rentDetail[orderNum]; ok {
		return &d, nil
	}
	return &eco.SellerRentOrderDetailResult{OrderNum: orderNum}, nil
}

func TestECODeliveryRiskStopsBeforeRemainingOrders(t *testing.T) {
	for _, phase := range []string{"sale_send", "sale_detail", "rent_detail", "rent_send"} {
		t.Run(phase, func(t *testing.T) {
			ef := &fakeEco{sendErr: map[string]error{}, detailErr: map[string]error{}, rentDetailErr: map[string]error{}}
			sf := &fakeSteamAccept{}
			switch phase {
			case "sale_send", "sale_detail":
				ef.orders = []eco.SellerOrder{{OrderNum: "first", OrderStateCode: 1}, {OrderNum: "second", OrderStateCode: 1}}
				if phase == "sale_send" {
					ef.sendErr["first"] = platform.ErrRateLimited
				} else {
					ef.detailErr["first"] = platform.ErrPlatformBlocked
				}
			case "rent_detail", "rent_send":
				ef.rentOrders = []eco.SellerRentOrder{{OrderNum: "first"}, {OrderNum: "second"}}
				ef.rentDetail = map[string]eco.SellerRentOrderDetailResult{"first": {SendOfferRole: 2}, "second": {SendOfferRole: 2}}
				if phase == "rent_detail" {
					ef.rentDetailErr["first"] = platform.ErrAuthExpired
				} else {
					ef.sendErr["first"] = platform.ErrRateLimited
				}
			}
			err := newTestDeps(ef, sf, &auditSpy{}).RunECODelivery(context.Background())
			if err == nil || riskCooldown(err) == 0 {
				t.Fatalf("risk not returned: %v", err)
			}
			for _, ref := range ef.sendAttempts {
				if ref == "second" {
					t.Fatal("continued writing after risk response")
				}
			}
			if strings.HasPrefix(phase, "sale") && ef.rentCalls != 0 {
				t.Fatal("risk response did not stop before rent pass")
			}
		})
	}
}

func TestECODeliveryAggregatesFailureAndKeepsOtherOrders(t *testing.T) {
	ef := &fakeEco{orders: []eco.SellerOrder{{OrderNum: "first"}, {OrderNum: "second"}}, details: map[string]eco.SellerOrderDetail{"first": {TradeOfferID: "one"}, "second": {TradeOfferID: "two"}}}
	sf := &fakeSteamAccept{failOn: map[string]error{"one": platform.ErrAuthExpired}}
	err := newTestDeps(ef, sf, &auditSpy{}).RunECODelivery(context.Background())
	if !errors.Is(err, platform.ErrAuthExpired) || len(sf.accepted) != 1 || sf.accepted[0] != "two" || ef.rentCalls != 1 {
		t.Fatalf("Steam error wrongly stopped ECO batch: err=%v accepted=%v", err, sf.accepted)
	}
	ef.sendErr = map[string]error{"first": errors.New("bad order")}
	ef.orders[0].OrderStateCode = 1
	if err := newTestDeps(ef, &fakeSteamAccept{}, &auditSpy{}).RunECODelivery(context.Background()); err == nil {
		t.Fatal("per-order failure swallowed")
	}
}

type fakeSteamAccept struct {
	attempts []string // 每次调用必记录（无论成败）
	accepted []string
	failOn   map[string]error
}

func (f *fakeSteamAccept) AcceptTradeOffer(_ context.Context, offerID string) (bool, error) {
	f.attempts = append(f.attempts, offerID)
	if e, ok := f.failOn[offerID]; ok {
		return false, e
	}
	f.accepted = append(f.accepted, offerID)
	return true, nil
}

type auditSpy struct{ actions []string }

func (a *auditSpy) record(_ context.Context, e domain.AuditEntry) {
	a.actions = append(a.actions, e.Action)
}

func newTestDeps(ef *fakeEco, sf *fakeSteamAccept, spy *auditSpy) *EcoDeliveryDeps {
	return &EcoDeliveryDeps{
		Eco: ef, Steam: sf,
		Audit: func(_ context.Context, e domain.AuditEntry) { spy.record(context.Background(), e) },
		Log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// ---- tests ----

func TestECODeliveryFourStepLoop(t *testing.T) {
	ef := &fakeEco{
		orders: []eco.SellerOrder{
			{OrderNum: "ZH1", OrderStateCode: 1, GoodsName: "刀", OrderAmount: 1000}, // 需先发报价
			{OrderNum: "ZH2", OrderStateCode: 3, GoodsName: "枪", OrderAmount: 50},   // 已发送，直接取号接受
			{OrderNum: "ZH3", OrderStateCode: 1, GoodsName: "印花", OrderAmount: 5},   // 报价号未就绪
		},
		details: map[string]eco.SellerOrderDetail{
			"ZH1": {TradeOfferID: "80001", GoodsName: "刀"},
			"ZH2": {TradeOfferID: "80002", GoodsName: "枪"},
			"ZH3": {}, // TradeOfferId 为空 → 本轮跳过
		},
	}
	sf := &fakeSteamAccept{}
	spy := &auditSpy{}

	if err := newTestDeps(ef, sf, spy).RunECODelivery(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(ef.sent) != 2 || ef.sent[0] != "ZH1" || ef.sent[1] != "ZH3" {
		t.Fatalf("all state=1 orders should send: %v", ef.sent)
	}
	if len(sf.accepted) != 2 || sf.accepted[0] != "80001" || sf.accepted[1] != "80002" {
		t.Fatalf("accepted: %v", sf.accepted)
	}
	found := false
	for _, a := range spy.actions {
		if a == "order.delivered" {
			found = true
		}
	}
	if !found {
		t.Fatalf("delivered audit missing: %v", spy.actions)
	}
}

func TestECODeliveryMemoAndFailureAudit(t *testing.T) {
	ef := &fakeEco{
		orders:  []eco.SellerOrder{{OrderNum: "Z1", OrderStateCode: 3}},
		details: map[string]eco.SellerOrderDetail{"Z1": {TradeOfferID: "90001"}},
	}
	sf := &fakeSteamAccept{failOn: map[string]error{"90001": errors.New("7-day hold")}}
	spy := &auditSpy{}
	d := newTestDeps(ef, sf, spy)
	ctx := context.Background()

	_ = d.RunECODelivery(ctx) // accept 失败 → 审计；不进 memo（下轮重试）
	_ = d.RunECODelivery(ctx) // 失败的 offer 第二轮必须重试

	if len(sf.attempts) != 2 {
		t.Fatalf("accept attempted %d times, want 2 (failure must not be memoized)", len(sf.attempts))
	}
	if len(sf.accepted) != 0 {
		t.Fatalf("failed accept must not count as accepted: %v", sf.accepted)
	}
	found := false
	for _, a := range spy.actions {
		if strings.Contains(a, "accept_offer_failed") {
			found = true
		}
	}
	if !found {
		t.Fatalf("failure not audited: %v", spy.actions)
	}

	// 平台恢复后：第三次成功并审计，第四次被 memo 跳过
	sf.failOn = nil
	_ = d.RunECODelivery(ctx)
	if len(sf.accepted) != 1 {
		t.Fatalf("recovered offer must deliver: %v", sf.accepted)
	}
	_ = d.RunECODelivery(ctx)
	if len(sf.attempts) != 3 {
		t.Fatalf("successful offer must be memoized: attempts=%d", len(sf.attempts))
	}
}

func TestECODeliverySendFailureSkipsAccept(t *testing.T) {
	ef := &fakeEco{
		orders:  []eco.SellerOrder{{OrderNum: "ZF", OrderStateCode: 1}},
		sendErr: map[string]error{"ZF": errors.New("7009 price changed")},
	}
	sf := &fakeSteamAccept{}
	spy := &auditSpy{}
	_ = newTestDeps(ef, sf, spy).RunECODelivery(context.Background())
	if len(sf.accepted) != 0 {
		t.Fatalf("must skip accept after send failure: %v", sf.accepted)
	}
	found := false
	for _, a := range spy.actions {
		if strings.Contains(a, "send_offer_failed") {
			found = true
		}
	}
	if !found {
		t.Fatal("send failure not audited")
	}
}

// Rent delivery pass (real-machine finding 2026-08-27): rent orders never
// appear in the sale-order list — they arrive via SellerRentOrderList and the
// platform pre-creates the trade offer exposed as SellerRentOrderDetail.OfferId.
// The loop must accept it once; when SendOfferRole=卖家 and no offer exists,
// it triggers SellerSendOffer and defers acceptance to the next cycle.
func TestECODeliveryRentPass(t *testing.T) {
	ef := &fakeEco{
		rentOrders: []eco.SellerRentOrder{
			{OrderNum: "RENT1", Status: 2}, // 平台已建报价 → 直接接受
			{OrderNum: "RENT2", Status: 2}, // 卖家未发报价(SendOfferRole=2, OfferId空) → 触发 SellerSendOffer
		},
		rentDetail: map[string]eco.SellerRentOrderDetailResult{
			"RENT1": {OrderNum: "RENT1", ProgressStatus: 2, OfferID: "88001", HashName: "刀"},
			"RENT2": {OrderNum: "RENT2", ProgressStatus: 2, SendOfferRole: 2},
		},
	}
	sf := &fakeSteamAccept{}
	spy := &auditSpy{}
	deps := newTestDeps(ef, sf, spy)
	Run := func() { _ = deps.RunECODelivery(context.Background()) }
	Run()

	if len(sf.accepted) != 1 || sf.accepted[0] != "88001" {
		t.Fatalf("rent offer must be accepted once: %+v", sf.attempts)
	}
	if len(ef.sent) != 1 || ef.sent[0] != "RENT2" {
		t.Fatalf("seller-role rent order must trigger send-offer: %v", ef.sent)
	}
	if len(spy.actions) != 1 || spy.actions[0] != "order.delivered" {
		t.Fatalf("audit: %v", spy.actions)
	}

	// Cross-cycle idempotency: the accepted offer is not re-accepted; RENT2
	// now has its offer id (post-send) and gets accepted this time.
	ef.rentDetail["RENT2"] = eco.SellerRentOrderDetailResult{OrderNum: "RENT2", ProgressStatus: 2, OfferID: "88002"}
	Run()
	if len(sf.accepted) != 2 || sf.accepted[1] != "88002" {
		t.Fatalf("second cycle: %+v", sf.accepted)
	}
	if len(spy.actions) != 2 {
		t.Fatalf("audit after second cycle: %v", spy.actions)
	}
}
