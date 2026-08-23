package eco

import (
	"context"
	"net/http"
	"strings"
	"testing"
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
