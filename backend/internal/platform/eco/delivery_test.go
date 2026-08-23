package eco

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestOneClickResolveOffer(t *testing.T) {
	var calledPath string
	c, _ := newTestClient(t, func(t *testing.T, r *http.Request, body map[string]any) string {
		calledPath = r.URL.Path
		if body["PartnerId"] != "pid123" {
			t.Errorf("partner id missing: %v", body)
		}
		return okEnv(`{"sendOfferResults":[
			{"OrderNum":"ZH1","OfferId":"100001","NeedsMobileConfirmation":false},
			{"OrderNum":"ZH2","NeedsMobileConfirmation":true,"Error":"需要手机确认"}],
			"acceptOfferResults":[
			{"OrderNum":"DB3","OfferId":"100002","NeedMobileConfirmation":false,"ErrorCode":1},
			{"OrderNum":"DB4","ErrorCode":108,"Error":"TooManyPending"}]}`)
	})
	out, err := c.OneClickResolveOffer(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if calledPath != "/Api/open/order/OneClickResolveOffer" {
		t.Fatalf("path=%s", calledPath)
	}
	if len(out.SendOffers) != 2 || len(out.AcceptOffers) != 2 {
		t.Fatalf("results: %+v", out)
	}
	if out.SendOffers[1].NeedsMobileConfirmation != true || out.SendOffers[1].Error == "" {
		t.Fatalf("mobile conf flag lost: %+v", out.SendOffers[1])
	}
	if out.AcceptOffers[1].ErrorCode != 108 { // TooManyPending
		t.Fatalf("error code: %+v", out.AcceptOffers[1])
	}

	// 失败项判定语义：ErrorCode==1(OK) 或 Error 为空才算成功
	failures := 0
	for _, a := range out.AcceptOffers {
		if a.ErrorCode != 1 && a.Error != "" { // 1 = ErrorCodes.OK
			failures++
		}
	}
	if failures != 1 {
		t.Fatalf("failures=%d want 1", failures)
	}
}

func TestSellerSendOfferPayload(t *testing.T) {
	c, _ := newTestClient(t, func(t *testing.T, r *http.Request, body map[string]any) string {
		if r.URL.Path != "/Api/open/order/SellerSendOffer" {
			t.Errorf("path=%s", r.URL.Path)
		}
		if body["OrderNum"] != "ZH9" || body["GameId"] != "730" {
			t.Errorf("biz: %v", body)
		}
		return okEnv(`{"OrderNum":"ZH9","OfferId":"3000000001","NeedsMobileConfirmation":true}`)
	})
	res, err := c.SellerSendOffer(context.Background(), "ZH9")
	if err != nil {
		t.Fatal(err)
	}
	if res.OfferID != "3000000001" || !res.NeedsMobileConfirmation {
		t.Fatalf("res: %+v", res)
	}
	if _, err := c.SellerSendOffer(context.Background(), ""); err == nil {
		t.Fatal("empty order num must error")
	}
}

var _ = strings.Contains

func TestSellerOrderListAndDetail(t *testing.T) {
	var listBody map[string]any
	c, _ := newTestClient(t, func(t *testing.T, r *http.Request, body map[string]any) string {
		switch r.URL.Path {
		case "/Api/open/order/SellerOrderList":
			listBody = body
			if body["DetailsState"] != float64(8) || body["PageSize"] != float64(100) {
				t.Errorf("list payload: %v", body)
			}
			return okEnv(`{"TotalRecord":1,"PageResult":[{"OrderNum":"ZH1","OrderStateCode":1,"GoodsName":"AK","OrderAmount":99.5}]}`)
		case "/Api/open/order/SellerOrderDetail":
			if body["OrderNum"] == "ZH1" {
				return okEnv(`{"TradeOfferId":"3000000009","GoodsName":"AK","TotalMoney":99.5,"BuyerNickname":"买家甲"}`)
			}
			return okEnv(`{}`)
		default:
			t.Errorf("unexpected %s", r.URL.Path)
			return okEnv(`null`)
		}
	})
	state := 8
	orders, err := c.SellerOrderList(context.Background(),
		time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 23, 0, 0, 0, 0, time.UTC), &state, "")
	if err != nil || len(orders) != 1 || orders[0].OrderStateCode != 1 {
		t.Fatalf("orders: %v %+v", err, orders)
	}
	if listBody == nil {
		t.Fatal("list body not captured")
	}
	d, err := c.Detail(context.Background(), "ZH1")
	if err != nil || d.TradeOfferID != "3000000009" || d.BuyerNickname != "买家甲" {
		t.Fatalf("detail: %v %+v", err, d)
	}
	if _, err := c.Detail(context.Background(), "EMPTY"); err != nil {
		t.Fatalf("empty-offer detail must not error: %v", err)
	}
}
