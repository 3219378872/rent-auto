package eco

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/3219378872/rent-auto/backend/internal/platform"
)

func TestAdapterPublishAndReprice(t *testing.T) {
	var publishAssets, modAssets []byte
	c, _ := newTestClient(t, func(t *testing.T, r *http.Request, body map[string]any) string {
		switch body["PublishType"] {
		case float64(1):
			publishAssets, _ = json.Marshal(body["Assets"])
			return okEnv(`[{"AssetId":"a1","IsSuccess":true,"GoodNum":"GN1"},{"AssetId":"a2","IsSuccess":false,"ErrorMsg":"不可租"}]`)
		case float64(2):
			modAssets, _ = json.Marshal(body["Assets"])
			return okEnv(`[{"IsSuccess":true},{"IsSuccess":false,"ErrorMsg":"锁定"}]`)
		default:
			t.Errorf("publish type %v", body["PublishType"])
			return okEnv(`null`)
		}
	})
	ad := NewAdapter(c, "sid")
	ctx := context.Background()

	pub, err := ad.PublishLease(ctx, []platform.PublishLeaseRequest{
		{AssetRef: "a1", RentPrice: 1.2, LongRentPrice: 1.0, MaxDays: 30, Deposit: 140},
		{AssetRef: "a2", RentPrice: 2.0, MaxDays: 8, Deposit: 60},
	})
	// round7 契约：逐项失败以哨兵暴露，结果字段保持权威
	if !errors.Is(err, platform.ErrPartialFailure) {
		t.Fatalf("want ErrPartialFailure, got %v", err)
	}
	if !pub[0].Success || pub[0].GoodsRef != "GN1" || pub[1].Success || pub[1].Error != "不可租" {
		t.Fatalf("pub: %+v", pub)
	}
	var items []RentPublishItem
	if err := json.Unmarshal([]byte(publishAssets), &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].LongRentPrice == nil || *items[0].LongRentPrice != 1.0 {
		t.Fatalf("items: %+v", items)
	}
	if items[1].LongRentPrice != nil || items[1].RentMaxDay != 8 {
		t.Fatalf("items[1]: %+v", items[1])
	}
	for i, it := range items {
		if it.SupportSublet != SubletOn || it.SubletPricingMethod != SubletPricingDynamic {
			t.Fatalf("items[%d] sublet policy: %+v", i, it)
		}
	}

	rep, err := ad.RepriceLease(ctx, []platform.RepriceLeaseRequest{
		{AssetRef: "b1", GoodsRef: "GN1", RentPrice: 1.3, MaxDays: 30, Deposit: 140},
		{AssetRef: "b2", GoodsRef: "GN2", RentPrice: 2.5, MaxDays: 30, Deposit: 150},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !rep[0].Success || rep[1].Success || rep[1].Error != "锁定" {
		t.Fatalf("rep: %+v", rep)
	}
	if !strings.Contains(string(modAssets), `"AssetId":"b1"`) {
		t.Fatalf("mod assets must address by AssetId: %s", modAssets)
	}
	var modItems []RentPublishItem
	if err := json.Unmarshal([]byte(modAssets), &modItems); err != nil {
		t.Fatal(err)
	}
	for i, it := range modItems {
		// reprice must re-assert the sublet policy: the mod body is a full item
		if it.SupportSublet != SubletOn || it.SubletPricingMethod != SubletPricingDynamic {
			t.Fatalf("modItems[%d] sublet policy: %+v", i, it)
		}
	}
}

func TestRepriceMissingResultsFailClosed(t *testing.T) {
	// ResultData null / shorter than request must NEVER read as success —
	// items without an explicit per-item result stay Success=false.
	c, _ := newTestClient(t, func(t *testing.T, r *http.Request, body map[string]any) string {
		return okEnv(`null`)
	})
	rep, err := NewAdapter(c, "sid").RepriceLease(context.Background(), []platform.RepriceLeaseRequest{
		{AssetRef: "b1", GoodsRef: "GN1", RentPrice: 1.3, MaxDays: 30, Deposit: 140},
		{AssetRef: "b2", GoodsRef: "GN2", RentPrice: 2.5, MaxDays: 30, Deposit: 150},
	})
	if !errors.Is(err, platform.ErrPartialFailure) {
		t.Fatalf("want ErrPartialFailure, got %v", err)
	}
	for i, r := range rep {
		if r.Success {
			t.Fatalf("rep[%d] must fail closed: %+v", i, r)
		}
		if r.Error == "" {
			t.Fatalf("rep[%d] must carry an error message: %+v", i, r)
		}
	}
}

func TestAdapterInventoryAndWallet(t *testing.T) {
	c, _ := newTestClient(t, func(t *testing.T, r *http.Request, body map[string]any) string {
		switch r.URL.Path {
		case "/Api/Selling/QueryStock":
			if body["GameId"] != "730" {
				t.Errorf("GameId = %v, want 730", body["GameId"])
			}
			return okEnv(`{"TotalRecord":4,"PageResult":[
				{"StockId":"s1","AssetId":"A1","HashName":"AK","GoodsName":"AK名","SteamPrice":120,"Price":99.5,"Tradable":true,"Status":1},
				{"StockId":"s2","AssetId":"A2","HashName":"AWP","GoodsName":"AWP名","SteamPrice":50,"Price":40,"Tradable":true,"Status":4},
				{"StockId":"s3","AssetId":"A3","HashName":"M4","GoodsName":"M4名","SteamPrice":30,"Price":25,"Tradable":true,"Status":5},
				{"StockId":"s4","AssetId":"A4","HashName":"USP","GoodsName":"USP名","SteamPrice":10,"Price":8,"Tradable":false,"Status":1}
			]}`)
		case "/Api/Merchant/GetTotalMoney":
			return okEnv(`{"Money":1234.56}`)
		default:
			t.Errorf("unexpected %s", r.URL.Path)
			return okEnv(`null`)
		}
	})
	ad := NewAdapter(c, "sid")
	ctx := context.Background()

	inv, err := ad.Inventory(ctx)
	if err != nil || len(inv) != 4 {
		t.Fatalf("inv: %v %d", err, len(inv))
	}
	if inv[0].AssetID != "A1" || inv[0].MarkPrice != 99.5 || inv[0].Status != "in_stock" || !inv[0].Tradable {
		t.Fatalf("inv item: %+v", inv[0])
	}
	if inv[1].Status != "listed" {
		t.Fatalf("出租上架(4) must map to listed: %+v", inv[1])
	}
	if inv[2].Status != "locked" {
		t.Fatalf("出租交易中(5) must map to locked: %+v", inv[2])
	}
	if inv[3].Status != "locked" || inv[3].Tradable {
		t.Fatalf("non-tradable must be locked: %+v", inv[3])
	}

	bal, err := ad.Wallet(ctx)
	if err != nil || bal != 1234.56 {
		t.Fatalf("wallet: %v %f", err, bal)
	}
	if err := ad.Healthy(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestMarshalBodyQuotesStringsOnly(t *testing.T) {
	body, err := marshalBody(map[string]any{
		"B": 1,
		"a": "hello world",
		"c": []map[string]any{{"K": "v"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	if strings.Contains(s, `\"`) && !strings.Contains(s, `"a":"hello world"`) {
		t.Fatalf("body: %s", s)
	}
	for _, want := range []string{`"B":1`, `"a":"hello world"`, `"c":[{"K":"v"}]`} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %s in %s", want, s)
		}
	}
}

func TestParsePrivateKeyRawBase64(t *testing.T) {
	privPEM, _ := testKeyPair(t)
	block, _ := decodePEM(privPEM)
	raw := make([]byte, len(block.Bytes))
	copy(raw, block.Bytes)
	if _, err := ParsePrivateKey([]byte(strings.TrimSpace(base64.StdEncoding.EncodeToString(raw)))); err != nil {
		t.Fatalf("raw base64 key rejected: %v", err)
	}
	if _, err := ParsePrivateKey([]byte("not a key")); err == nil {
		t.Fatal("garbage accepted")
	}
}

// PublishLease 部分失败必须以 ErrPartialFailure 哨兵暴露，同时结果数组
// 保持逐项权威（round7 契约统一）。
func TestPublishLeasePartialFailureSentinel(t *testing.T) {
	c, _ := newTestClient(t, func(t *testing.T, r *http.Request, body map[string]any) string {
		return okEnv(`[{"AssetId":"a1","IsSuccess":true,"GoodNum":"GN1"},{"AssetId":"a2","IsSuccess":false,"ErrorMsg":"不可租"}]`)
	})
	out, err := NewAdapter(c, "sid").PublishLease(context.Background(), []platform.PublishLeaseRequest{
		{AssetRef: "a1", RentPrice: 1.2, MaxDays: 30, Deposit: 140},
		{AssetRef: "a2", RentPrice: 2.0, MaxDays: 8, Deposit: 60},
	})
	if !errors.Is(err, platform.ErrPartialFailure) {
		t.Fatalf("want ErrPartialFailure, got %v", err)
	}
	if len(out) != 2 || !out[0].Success || out[0].GoodsRef != "GN1" || out[1].Success || out[1].Error != "不可租" {
		t.Fatalf("results must stay authoritative: %+v (%v)", out, err)
	}

	allOK, err := NewAdapter(c, "sid").PublishLease(context.Background(), []platform.PublishLeaseRequest{
		{AssetRef: "a1", RentPrice: 1.2, MaxDays: 30, Deposit: 140},
	})
	if err != nil {
		t.Fatalf("all-success publish must be nil error: %v", err)
	}
	if len(allOK) != 1 || !allOK[0].Success {
		t.Fatalf("all-success results: %+v", allOK)
	}
}
