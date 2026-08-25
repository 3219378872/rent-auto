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
		case "/Api/Selling/QuerySteamStock":
			return okEnv(`{"TotalRecord":1,"PageResult":[{"StockId":"s1","AssetId":"A1","HashName":"AK","GoodsName":"AK名","MarkPrice":99.5,"Status":0}]}`)
		case "/Api/Merchant/GetMerchantMoney":
			return okEnv(`{"Money":1234.56}`)
		default:
			t.Errorf("unexpected %s", r.URL.Path)
			return okEnv(`null`)
		}
	})
	ad := NewAdapter(c, "sid")
	ctx := context.Background()

	inv, err := ad.Inventory(ctx)
	if err != nil || len(inv) != 1 {
		t.Fatalf("inv: %v %d", err, len(inv))
	}
	if inv[0].AssetID != "A1" || inv[0].MarkPrice != 99.5 || inv[0].Status != "in_stock" {
		t.Fatalf("inv item: %+v", inv[0])
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
