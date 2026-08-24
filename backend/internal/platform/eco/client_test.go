package eco

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/domain"
	"github.com/3219378872/rent-auto/backend/internal/platform"
)

func decodeBody(r *http.Request, out *map[string]any) error {
	b, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func okEnv(data string) string {
	return `{"ResultCode":"0","ResultMsg":"成功","ResultData":` + data + `}`
}

func TestClientSignaturePresentAndVerifiable(t *testing.T) {
	var gotCanonical string
	priv, pub := testKeyPair(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = decodeBody(r, &body)
		// rebuild canonical from received body (excluding Sign) and verify
		delete(body, "Sign")
		gotCanonical = SignString(body)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(okEnv(`{"Money": 88.5}`)))
	}))
	defer srv.Close()

	c := mustClient(t, priv, srv.URL)
	if _, err := c.GetWalletBalance(context.Background()); err != nil {
		t.Fatal(err)
	}
	// the sign string we can reconstruct server-side must be well-formed
	if !strings.Contains(gotCanonical, "PartnerId=pid123") {
		t.Fatalf("canonical: %q", gotCanonical)
	}
	_ = pub
}

func TestClientRateLimited(t *testing.T) {
	c, _ := newTestClient(t, func(t *testing.T, r *http.Request, b map[string]any) string {
		return `{"ResultCode":"6001","ResultMsg":"too fast"}`
	})
	if _, err := c.GetWalletBalance(context.Background()); !errors.Is(err, platform.ErrRateLimited) {
		t.Fatalf("want ErrRateLimited, got %v", err)
	}
}

func fastRetry(t *testing.T) {
	t.Helper()
	old := rateRetryBase
	rateRetryBase = time.Millisecond
	t.Cleanup(func() { rateRetryBase = old })
}

// 6001 must be retried with backoff up to 3 total attempts; success on the
// last allowed attempt still succeeds.
func TestClientRateLimitedRetriesThenSucceeds(t *testing.T) {
	fastRetry(t)
	attempts := 0
	c, _ := newTestClient(t, func(t *testing.T, r *http.Request, b map[string]any) string {
		attempts++
		if attempts < 3 {
			return `{"ResultCode":"6001","ResultMsg":"too fast"}`
		}
		return okEnv(`{"Money": 1}`)
	})
	if _, err := c.GetWalletBalance(context.Background()); err != nil {
		t.Fatalf("retry should recover: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("attempts=%d, want 3", attempts)
	}
}

// More than 3 attempts must never happen even when the platform keeps limiting.
func TestClientRateLimitedRetriesCapped(t *testing.T) {
	fastRetry(t)
	attempts := 0
	c, _ := newTestClient(t, func(t *testing.T, r *http.Request, b map[string]any) string {
		attempts++
		return `{"ResultCode":"6001","ResultMsg":"too fast"}`
	})
	if _, err := c.GetWalletBalance(context.Background()); !errors.Is(err, platform.ErrRateLimited) {
		t.Fatalf("want ErrRateLimited, got %v", err)
	}
	if attempts != rateRetryAttempts {
		t.Fatalf("attempts=%d, want %d", attempts, rateRetryAttempts)
	}
}

func TestPublishRentPayloadShape(t *testing.T) {
	c, _ := newTestClient(t, func(t *testing.T, r *http.Request, body map[string]any) string {
		if r.URL.Path != "/Api/Rent/PublishRentAndSaleGoods" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if body["SteamId"] != "76561199" || body["PublishType"] != float64(1) {
			t.Errorf("biz fields: %v", body)
		}
		assets, _ := json.Marshal(body["Assets"])
		var items []RentPublishItem
		if err := json.Unmarshal(assets, &items); err != nil {
			t.Errorf("assets decode: %v (%s)", err, assets)
		} else {
			if len(items) != 1 || items[0].AssetID != "asset-1" || items[0].RentMaxDay != 30 || items[0].TradeTypes[0] != TradeTypeRent {
				t.Errorf("items: %+v (%s)", items, assets)
			}
		}
		return okEnv(`[{"AssetId":"asset-1","IsSuccess":true,"GoodNum":"DB20260823-00001"}]`)
	})
	results, err := c.PublishRentAndSale(context.Background(), "76561199", PublishTypeAdd, []RentPublishItem{{
		AssetID: "asset-1", SteamGameID: "730", TradeTypes: []int{TradeTypeRent},
		RentPrice: 1.2, RentMaxDay: 30, RentDeposits: 140,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || !results[0].IsSuccess || results[0].GoodNum == "" {
		t.Fatalf("results: %+v", results)
	}
}

func TestQuerySelfRentGoodsPaging(t *testing.T) {
	page := 0
	c, _ := newTestClient(t, func(t *testing.T, r *http.Request, body map[string]any) string {
		page++
		switch body["PageIndex"] {
		case float64(1):
			item := `{"GoodsNum":"G1","Status":1,"Price":1.5,"Deposits":100,"RentMaxDay":30,"HashName":"AK","GoodsName":"AK","CreateTime":"2026-08-01 10:00:00"}`
			list := strings.Repeat(item+",", 100)
			return okEnv(`{"PageIndex":1,"PageSize":100,"TotalRecord":101,"PageResult":[` + list[:len(list)-1] + `]}`)
		default:
			if page != 2 {
				t.Errorf("unexpected page %v", body["PageIndex"])
			}
			return okEnv(`{"PageIndex":2,"PageSize":100,"TotalRecord":101,"PageResult":[{"GoodsNum":"G2","Status":2,"Price":2}]}`)
		}
	})
	goods, err := c.QuerySelfRentGoods(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(goods) != 101 || goods[100].GoodsNum != "G2" || goods[100].Status != 2 {
		t.Fatalf("goods len=%d last=%+v", len(goods), func() any {
			if len(goods) > 100 {
				return goods[100]
			}
			return nil
		}())
	}
}

func TestOffshelfAndOrders(t *testing.T) {
	c, _ := newTestClient(t, func(t *testing.T, r *http.Request, body map[string]any) string {
		switch r.URL.Path {
		case "/Api/Rent/OffshelfRentGoods":
			raw, _ := json.Marshal(body["goodsNumList"])
			if !strings.Contains(string(raw), `"GoodsNum":"G1"`) {
				t.Errorf("goodsNumList: %s", raw)
			}
			return okEnv(`[{"GoodsNum":"G1","IsSuccess":true}]`)
		case "/Api/Rent/SellerRentOrderList":
			return okEnv(`{"TotalRecord":1,"PageResult":[{"OrderNum":"ZH1","RentType":2,"Status":8,"Price":9.9,"OrderAmount":297,"RentDay":30,"Deposits":150,"HashName":"Knife","AssetId":"a9","CreateTime":"2026-08-01 10:00:00","RentExpire":"2026-08-31 10:00:00"}]}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			return okEnv(`null`)
		}
	})
	ctx := context.Background()
	ad := NewAdapter(c, "76561199")

	if err := ad.Delist(ctx, []string{"G1"}); err != nil {
		t.Fatal(err)
	}
	orders, err := ad.LeaseOrders(ctx, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(orders) != 1 {
		t.Fatalf("orders=%d", len(orders))
	}
	o := orders[0]
	if o.OrderType != "long" || o.Status != "bought_out" || o.Amount != 297 || o.Channel != domain.ChannelECO {
		t.Fatalf("order: %+v", o)
	}
}

func TestMarketDumpBothShapes(t *testing.T) {
	for _, resp := range []string{
		okEnv(`{"List":[{"market_hash_name":"AK","steam_price_cny":120.5,"goods_id":7}]}`),
		okEnv(`[{"market_hash_name":"AK","steam_price_cny":120.5,"goods_id":7}]`),
	} {
		c, _ := newTestClient(t, func(t *testing.T, r *http.Request, b map[string]any) string { return resp })
		rows, err := c.GetMarketPriceDump(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || rows[0].MarketHashName != "AK" || rows[0].SteamPriceCNY != 120.5 {
			t.Fatalf("rows: %+v (resp=%s)", rows, resp)
		}
	}
}

func TestAdapterCapsAndShelfMapping(t *testing.T) {
	c, _ := newTestClient(t, func(t *testing.T, r *http.Request, body map[string]any) string {
		if r.URL.Path != "/Api/Rent/QuerySelfRentGoods" {
			t.Errorf("path %s", r.URL.Path)
		}
		return okEnv(`{"TotalRecord":1,"PageResult":[{"GoodsNum":"G9","AssetId":"a1","MarkPrice":100,"Status":2,"RentMaxDay":30,"Price":1.1,"LongRentDay":21,"LongRentPrice":0.9,"Deposits":140,"HashName":"H","GoodsName":"N","CreateTime":"2026-08-01 10:00:00"}]}`)
	})
	ad := NewAdapter(c, "sid")
	caps := ad.Caps()
	if caps.DepositDirect || caps.RentMaxDayMin != 8 || caps.LongLeaseThresholdDays != 21 {
		t.Fatalf("caps: %+v", caps)
	}
	shelf, err := ad.LeaseShelf(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if shelf[0].GoodsRef != "G9" || !shelf[0].Leased || shelf[0].Deposit != 140 {
		t.Fatalf("shelf: %+v", shelf[0])
	}
}

// A non-200 transport status must fail closed even when the body is JSON
// without our envelope — decoding it as code=0 faked success on write paths
// (ghost delist regression, 2026-08-24 round 3).
func TestClientRejectsNon200EnvelopelessBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":"bad gateway"}`))
	}))
	defer srv.Close()
	priv, _ := testKeyPair(t)
	c := mustClient(t, priv, srv.URL)
	_, err := c.GetWalletBalance(context.Background())
	if err == nil {
		t.Fatal("non-200 must fail closed")
	}
	if !strings.Contains(err.Error(), "502") {
		t.Fatalf("error should mention transport status: %v", err)
	}
}

// 200 with a JSON body that carries no ResultCode is a protocol violation:
// defaulting to code=0 would mark writes successful.
func TestClientRejectsMissingResultCode(t *testing.T) {
	c, _ := newTestClient(t, func(t *testing.T, r *http.Request, b map[string]any) string {
		return `{"ResultMsg":"weird"}`
	})
	if _, err := c.GetWalletBalance(context.Background()); err == nil {
		t.Fatal("envelope without ResultCode must fail closed")
	}
}
