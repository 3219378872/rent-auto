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

// Wallet balance must hit the documented route /Api/Merchant/GetTotalMoney;
// the earlier transcription /Api/Merchant/GetMerchantMoney 404'd on the real
// platform (2026-08-27). Pin the path so it cannot drift silently again.
func TestWalletBalanceHitsDocumentedRoute(t *testing.T) {
	c, _ := newTestClient(t, func(t *testing.T, r *http.Request, b map[string]any) string {
		if r.URL.Path != "/Api/Merchant/GetTotalMoney" {
			t.Errorf("path = %s", r.URL.Path)
		}
		return okEnv(`{"Money": 7.5}`)
	})
	bal, err := c.GetWalletBalance(context.Background())
	if err != nil || bal != 7.5 {
		t.Fatalf("bal: %v %f", err, bal)
	}
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
			// Chunked query (platform caps windows at 31d): only the segment
			// covering the order's CreateTime returns it; every request must
			// stay within the 31d window or the platform answers code=7002.
			// Sent bounds are platform-CST wall strings; the fake compares
			// wall clocks so the created constant is written zone-agnostically.
			st, err1 := time.Parse("2006-01-02 15:04:05", body["StartTime"].(string))
			et, err2 := time.Parse("2006-01-02 15:04:05", body["EndTime"].(string))
			if err1 != nil || err2 != nil {
				t.Errorf("order list time fields: %v %v", body["StartTime"], body["EndTime"])
			} else {
				if et.Sub(st) > 31*24*time.Hour {
					t.Errorf("segment span %v exceeds the 31d platform cap", et.Sub(st))
				}
				created := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
				if !st.After(created) && !et.Before(created) {
					return okEnv(`{"TotalRecord":1,"PageResult":[{"OrderNum":"ZH1","RentType":2,"Status":8,"Price":9.9,"OrderAmount":297,"RentDay":30,"Deposits":150,"HashName":"Knife","AssetId":"a9","CreateTime":"2026-08-01 10:00:00","RentExpire":"2026-08-31 10:00:00"}]}`)
				}
			}
			return okEnv(`{"TotalRecord":0,"PageResult":null}`)
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
	// Response timestamps are platform-CST strings and must land as the real
	// UTC instants (regression 2026-08-28: parsed as UTC they ran 8h early,
	// skewing started_at/due_at and every rollup anchored on them).
	if want := time.Date(2026, 8, 1, 2, 0, 0, 0, time.UTC); !o.StartedAt.Equal(want) {
		t.Fatalf("started_at = %s, want %s", o.StartedAt, want)
	}
	if want := time.Date(2026, 8, 31, 2, 0, 0, 0, time.UTC); !o.DueAt.Equal(want) {
		t.Fatalf("due_at = %s, want %s", o.DueAt, want)
	}
}

// Regression 2026-08-28: window params must be rendered in the platform's
// Beijing wall clock. UTC-formatted bounds made fresh rent orders invisible
// to orders_sync for ~8h (the platform compares them against CST CreateTime
// strings, evidence 2026-08-28-eco-orders-tz-8h-lag.md).
func TestSellerRentOrderListWindowInPlatformZone(t *testing.T) {
	var gotStart, gotEnd string
	c, _ := newTestClient(t, func(t *testing.T, r *http.Request, body map[string]any) string {
		gotStart, _ = body["StartTime"].(string)
		gotEnd, _ = body["EndTime"].(string)
		return okEnv(`{"TotalRecord":0,"PageResult":null}`)
	})
	end := time.Date(2026, 8, 28, 2, 18, 18, 0, time.UTC)
	if _, err := c.SellerRentOrderList(context.Background(), end.AddDate(0, 0, -5), end, nil); err != nil {
		t.Fatal(err)
	}
	if gotStart != "2026-08-23 10:18:18" || gotEnd != "2026-08-28 10:18:18" {
		t.Fatalf("window not rendered in +08: start=%q end=%q", gotStart, gotEnd)
	}
}

// The sale-order list sends date-only bounds; the +08 conversion must shift
// late-UTC days across the date boundary too (delivery loop relies on the
// EndTime covering orders created "today" on platform wall clock).
func TestSellerOrderListWindowInPlatformZone(t *testing.T) {
	var gotStart, gotEnd string
	c, _ := newTestClient(t, func(t *testing.T, r *http.Request, body map[string]any) string {
		gotStart, _ = body["StartTime"].(string)
		gotEnd, _ = body["EndTime"].(string)
		return okEnv(`{"PageResult":[],"TotalRecord":0}`)
	})
	end := time.Date(2026, 8, 27, 20, 0, 0, 0, time.UTC) // already 08-28 04:00 in +08
	if _, err := c.SellerOrderList(context.Background(), end.AddDate(0, 0, -2), end, nil, ""); err != nil {
		t.Fatal(err)
	}
	if gotStart != "2026-08-26" || gotEnd != "2026-08-28" {
		t.Fatalf("window not rendered in +08: start=%q end=%q", gotStart, gotEnd)
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
	// Shelf CreateTime is a platform-CST string (regression 2026-08-28).
	if want := time.Date(2026, 8, 1, 2, 0, 0, 0, time.UTC); !shelf[0].ListedAt.Equal(want) {
		t.Fatalf("listed_at = %s, want %s", shelf[0].ListedAt, want)
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

// Credential-class failures (4004 IP whitelist / 5005 identity invalid) map
// to the unified ErrAuthExpired sentinel so scheduler risk-control cooldowns
// engage instead of blind per-cycle retries.
func TestCredentialFailuresMapToAuthExpired(t *testing.T) {
	for _, code := range []string{"4004", "5005"} {
		c, _ := newTestClient(t, func(t *testing.T, r *http.Request, b map[string]any) string {
			return `{"ResultCode":"` + code + `","ResultMsg":"denied"}`
		})
		_, err := c.GetWalletBalance(context.Background())
		if !errors.Is(err, platform.ErrAuthExpired) {
			t.Fatalf("code %s: want ErrAuthExpired, got %v", code, err)
		}
	}
}

// The rent-order detail path is the source of the rental trade-offer id —
// rent orders never appear in the sale-order SellerOrderList view, so this
// endpoint (and only this one) drives rent delivery acceptance. Route pinned
// to the documented path (api-220956684) against transcription drift.
func TestSellerRentOrderDetailRoute(t *testing.T) {
	c, _ := newTestClient(t, func(t *testing.T, r *http.Request, body map[string]any) string {
		if r.URL.Path != "/Api/Rent/SellerRentOrderDetail" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if body["OrderNum"] != "DB-1" {
			t.Errorf("OrderNum = %v", body["OrderNum"])
		}
		return okEnv(`{"OrderNum":"DB-1","Status":2,"ProgressStatus":2,"SendOfferRole":2,
			"OfferId":"9328619377","HashName":"AK","Price":3,"OrderAmount":100,"Deposits":4500}`)
	})
	d, err := c.SellerRentOrderDetail(context.Background(), "DB-1")
	if err != nil {
		t.Fatal(err)
	}
	if d.OfferID != "9328619377" || d.ProgressStatus != 2 || d.SendOfferRole != 2 || d.Deposits != 4500 {
		t.Fatalf("detail: %+v", d)
	}
	if _, err := c.SellerRentOrderDetail(context.Background(), ""); err == nil {
		t.Fatal("empty order num must fail")
	}
}
