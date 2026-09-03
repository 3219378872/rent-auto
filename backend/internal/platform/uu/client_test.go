package uu

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/3219378872/rent-auto/backend/internal/platform"
)

// roundTripperTo redirects every request to the mock server base URL.
type roundTripperTo struct{ base string }

func (rt roundTripperTo) RoundTrip(r *http.Request) (*http.Response, error) {
	r.URL.Host = strings.TrimPrefix(rt.base, "http://")
	r.URL.Scheme = "http"
	return http.DefaultTransport.RoundTrip(r)
}

func mockHTTP(base string) *http.Client {
	return &http.Client{Transport: roundTripperTo{base}}
}

func newMockUU(t *testing.T, handle func(w http.ResponseWriter, r *http.Request) bool) (*Client, error) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if handle != nil && handle(w, r) {
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	return newMockedClient(t, srv.URL, "token123")
}

func newMockedClient(t *testing.T, base, token string) (*Client, error) {
	t.Helper()
	c := &Client{
		http:    mockHTTP(base),
		token:   token,
		device:  "testdevice0123",
		limiter: noopLimiter{},
	}
	if err := c.fetchUserInfo(context.Background()); err != nil {
		return nil, err
	}
	return c, nil
}

func okUserInfo(w http.ResponseWriter) {
	_, _ = w.Write([]byte(`{"Code":0,"Data":{"NickName":"tester","UserId":7}}`))
}

func TestClientAuthExpired(t *testing.T) {
	c, err := newMockUU(t, func(w http.ResponseWriter, r *http.Request) bool {
		if strings.HasSuffix(r.URL.Path, "getUserInfo") {
			_, _ = w.Write([]byte(`{"Code":84101,"Msg":"login expired"}`))
			return true
		}
		return false
	})
	if err == nil {
		err = c.fetchUserInfo(context.Background())
	}
	if !errors.Is(err, platform.ErrAuthExpired) {
		t.Fatalf("want ErrAuthExpired, got %v", err)
	}
}

func TestClientEnvelopeCaseInsensitive(t *testing.T) {
	// inventory endpoint uses lowercase "code"/"data" (documented quirk)
	c, err := newMockUU(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/api/user/Account/getUserInfo" {
			okUserInfo(w)
			return true
		}
		if r.URL.Path == "/api/commodity/Inventory/GetUserInventoryDataListV3" {
			_, _ = w.Write([]byte(`{"code":0,"data":{"ItemsInfos":[]}}`))
			return true
		}
		return false
	})
	if err != nil {
		t.Fatal(err)
	}
	items, err := c.GetInventory(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("want empty, got %d", len(items))
	}
}

func TestMarketLeasePriceFiltering(t *testing.T) {
	c, err := newMockUU(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch r.URL.Path {
		case "/api/user/Account/getUserInfo":
			okUserInfo(w)
		case "/api/homepage/v3/detail/commodity/list/lease":
			var req map[string]any
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req["templateId"] != "44444" || req["userId"] != float64(7) {
				t.Errorf("unexpected market payload: %v", req)
			}
			_, _ = w.Write([]byte(`{"Code":0,"Data":{"CommodityList":[
				{"CommodityName":"A","LeaseUnitPrice":"1.5","LongLeaseUnitPrice":"1.2","LeaseDeposit":"100"},
				{"CommodityName":"B","LeaseUnitPrice":"2.0","LeaseDeposit":"5000"},
				{"CommodityName":"C","LeaseDeposit":null}
			]}}`))
		default:
			w.WriteHeader(404)
		}
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	items, err := c.GetMarketLeasePrice(context.Background(), 44444, 0, 2000, 15)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 { // A in window; B deposit 5000 > maxPrice; C nil deposit
		t.Fatalf("filtered len = %d", len(items))
	}
	if items[0].Deposit() != 100 || items[0].UnitPrice() != 1.5 || items[0].LongUnitPrice() != 1.2 {
		t.Fatalf("item0 = %+v", items[0])
	}
}

func TestPublishAndRepriceFlow(t *testing.T) {
	initCalls := 0
	c, err := newMockUU(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch r.URL.Path {
		case "/api/user/Account/getUserInfo":
			okUserInfo(w)
		case "/api/commodity/Inventory/SellInventoryWithLeaseV2":
			var req struct {
				ItemInfos []OnLeaseItem `json:"ItemInfos"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			if len(req.ItemInfos) != 1 || req.ItemInfos[0].LeaseDeposit != "88.5" {
				t.Errorf("publish payload: %+v", req.ItemInfos)
			}
			_, _ = w.Write([]byte(`{"Code":0,"Data":[{"AssetId":111,"Status":1,"CommodityId":999,"Remark":""}]}`))
		case "/api/youpin/bff/new/commodity/commodity/change/price/v3/init/info":
			initCalls++
			_, _ = w.Write([]byte(`{"Code":0,"Data":{}}`))
		case "/api/commodity/Commodity/PriceChangeWithLeaseV2":
			_, _ = w.Write([]byte(`{"Code":0,"Data":{"SuccessCount":1,"FailCount":0,"Commoditys":[{"CommodityId":999,"IsSuccess":1}]}}`))
		default:
			w.WriteHeader(404)
		}
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ad := NewAdapter(c)

	res, err := ad.PublishLease(ctx, []platform.PublishLeaseRequest{
		{AssetRef: "111", RentPrice: 1.2, MaxDays: 60, Deposit: 88.5},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res[0].Success || res[0].GoodsRef != "999" {
		t.Fatalf("publish result: %+v", res[0])
	}

	rep, err := ad.RepriceLease(ctx, []platform.RepriceLeaseRequest{
		{GoodsRef: "999", RentPrice: 1.3, MaxDays: 60, Deposit: 88.5},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !rep[0].Success {
		t.Fatalf("reprice result: %+v", rep[0])
	}
	if initCalls != 1 {
		t.Fatalf("pre-change init must run once, ran %d", initCalls)
	}
}

func TestRepricePartialFailure(t *testing.T) {
	c, err := newMockUU(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch r.URL.Path {
		case "/api/user/Account/getUserInfo":
			okUserInfo(w)
		case "/api/youpin/bff/new/commodity/commodity/change/price/v3/init/info":
			_, _ = w.Write([]byte(`{"Code":0,"Data":{}}`))
		case "/api/commodity/Commodity/PriceChangeWithLeaseV2":
			_, _ = w.Write([]byte(`{"Code":0,"Data":{"SuccessCount":1,"FailCount":1,"Commoditys":[
				{"CommodityId":1,"IsSuccess":1},{"CommodityId":2,"IsSuccess":0,"Message":"locked"}]}}`))
		default:
			w.WriteHeader(404)
		}
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	rep, err := NewAdapter(c).RepriceLease(context.Background(), []platform.RepriceLeaseRequest{
		{GoodsRef: "1", RentPrice: 1, MaxDays: 30, Deposit: 10},
		{GoodsRef: "2", RentPrice: 1, MaxDays: 30, Deposit: 10},
	})
	if rep[0].Success != true || rep[1].Success != false || rep[1].Error != "locked" {
		t.Fatalf("results: %+v", rep)
	}
	if !errors.Is(err, platform.ErrPartialFailure) {
		t.Fatalf("want ErrPartialFailure, got %v", err)
	}
}

func TestHTTP405MapsUKExpired(t *testing.T) {
	_, err := newMockUU(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/api/user/Account/getUserInfo" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return true
		}
		return false
	})
	if !errors.Is(err, ErrUKExpired) {
		t.Fatalf("want ErrUKExpired, got %v", err)
	}
}

func TestEnvelopeMissingCodeFailsClosed(t *testing.T) {
	if _, err := decodeEnvelope([]byte(`{"Msg":"ok","Data":{}}`)); err == nil {
		t.Fatal("want error for envelope without Code/code")
	}
}

func TestRiskControlCode(t *testing.T) {
	_, err := newMockUU(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/api/user/Account/getUserInfo" {
			okUserInfo(w)
			return true
		}
		if r.URL.Path == "/api/homepage/v3/detail/commodity/list/lease" {
			_, _ = w.Write([]byte(`{"Code":84104,"Msg":"risk"}`))
			return true
		}
		return false
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = err
	c, _ := newMockUU(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch r.URL.Path {
		case "/api/user/Account/getUserInfo":
			okUserInfo(w)
		case "/api/homepage/v3/detail/commodity/list/lease":
			_, _ = w.Write([]byte(`{"Code":84104,"Msg":"risk"}`))
		default:
			w.WriteHeader(404)
		}
		return true
	})
	_, err = c.GetMarketLeasePrice(context.Background(), 1, 0, 100, 5)
	if !errors.Is(err, platform.ErrPlatformBlocked) {
		t.Fatalf("want ErrPlatformBlocked, got %v", err)
	}
}
