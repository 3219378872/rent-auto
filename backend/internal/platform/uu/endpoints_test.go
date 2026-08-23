package uu

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/platform"
)

func TestListLeaseShelfPaginationAndZeroCD(t *testing.T) {
	page := 0
	c, err := newMockUU(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch path := r.URL.Path; {
		case path == "/api/user/Account/getUserInfo":
			okUserInfo(w)
		case strings.HasSuffix(path, "/list/lease"):
			var req map[string]any
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req["pageIndex"] == float64(1) && page == 0 {
				page++
				item := `{"id":1,"steamAssetId":"a1","templateId":11,"name":"N","depositAmount":"10","shortLeaseAmount":"1","longLeaseAmount":"","leaseMaxDays":30,"commodityCanSell":false,"commodityCanLease":true}`
				list := strings.Repeat(item+",", 100)
				_, _ = w.Write([]byte(`{"code":0,"data":{"commodityInfoList":[` + list[:len(list)-1] + `]}}`))
				return true
			}
			_, _ = w.Write([]byte(`{"code":0,"data":{"commodityInfoList":[{"id":2,"steamAssetId":"a2","templateId":12,"name":"M","depositAmount":"20","shortLeaseAmount":"2","longLeaseAmount":"1.8","leaseMaxDays":60,"commodityCanSell":true,"commodityCanLease":true}]}}`))
		case strings.HasSuffix(r.URL.Path, "/list/zeroCDLease"):
			_, _ = w.Write([]byte(`{"code":9004001}`))
		default:
			w.WriteHeader(404)
		}
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	items, err := c.ListLeaseShelf(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 101 {
		t.Fatalf("items = %d", len(items))
	}
	if items[100].LongRent() != 1.8 || items[0].Deposit() != 10 {
		t.Fatalf("parse mismatch: %+v", items[100])
	}
}

func TestOffShelfPayload(t *testing.T) {
	var ids string
	c, err := newMockUU(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch r.URL.Path {
		case "/api/user/Account/getUserInfo":
			okUserInfo(w)
		case "/api/commodity/Commodity/OffShelf":
			var req map[string]any
			_ = json.NewDecoder(r.Body).Decode(&req)
			ids, _ = req["Ids"].(string)
			_, _ = w.Write([]byte(`{"Code":0,"Data":{}}`))
		default:
			w.WriteHeader(404)
		}
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := NewAdapter(c).Delist(context.Background(), []string{"11", "22"}); err != nil {
		t.Fatal(err)
	}
	if ids != "11,22" {
		t.Fatalf("Ids = %q", ids)
	}
}

func TestLeasedOutOrdersPaging(t *testing.T) {
	first := true
	c, err := newMockUU(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch r.URL.Path {
		case "/api/user/Account/getUserInfo":
			okUserInfo(w)
		case "/api/youpin/bff/trade/v1/order/lease/out/list":
			var req map[string]any
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req["pageIndex"] == float64(1) && first {
				first = false
				list := strings.Repeat(`{"orderId":"o","commodityInfo":{"name":"X"},"orderStatus":2,"leaseDays":7,"rentPrice":"3.5","deposit":"50","startTime":"2026-08-01 10:00:00","endTime":"2026-08-08 10:00:00"},`, 50)
				_, _ = w.Write([]byte(`{"Code":0,"data":{"orderDataList":[` + list[:len(list)-1] + `]}}`))
				return true
			}
			_, _ = w.Write([]byte(`{"Code":0,"data":{"orderDataList":[{"orderId":"o2","commodityInfo":{"name":"Y"},"orderStatus":3,"leaseDays":30,"rentPrice":"9.9","deposit":"120","startTime":"2026-07-01 10:00:00","endTime":"2026-07-31 10:00:00"}]}}`))
		default:
			w.WriteHeader(404)
		}
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	orders, err := NewAdapter(c).LeaseOrders(context.Background(), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(orders) != 51 {
		t.Fatalf("orders = %d", len(orders))
	}
	last := orders[50]
	if last.OrderRef != "o2" || last.Status != "bought_out" || last.RentPrice != 9.9 {
		t.Fatalf("last = %+v", last)
	}
	if orders[0].Status != "done" || orders[0].RentDays != 7 {
		t.Fatalf("first = %+v", orders[0])
	}
}

func TestZeroCDFlow(t *testing.T) {
	var opened []int64
	c, err := newMockUU(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch r.URL.Path {
		case "/api/user/Account/getUserInfo":
			okUserInfo(w)
		case "/api/youpin/bff/trade/v1/order/lease/sublet/canEnable/list":
			_, _ = w.Write([]byte(`{"Code":0,"Data":[{"orderId":555,"commodityInfo":{"name":"Z"}}]}`))
		case "/api/youpin/bff/order/sublet/open":
			var req struct {
				OrderIDs []int64 `json:"orderIds"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			opened = req.OrderIDs
			_, _ = w.Write([]byte(`{"Code":0,"Data":{}}`))
		default:
			w.WriteHeader(404)
		}
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	orders, err := c.GetZeroCDList(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(orders) != 1 || orders[0].OrderID != 555 {
		t.Fatalf("zerocd list: %+v", orders)
	}
	if err := c.EnableZeroCD(ctx, []int64{555}); err != nil {
		t.Fatal(err)
	}
	if len(opened) != 1 || opened[0] != 555 {
		t.Fatalf("opened: %v", opened)
	}
}

func TestPublishFailureRemark(t *testing.T) {
	c, err := newMockUU(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch r.URL.Path {
		case "/api/user/Account/getUserInfo":
			okUserInfo(w)
		case "/api/commodity/Inventory/SellInventoryWithLeaseV2":
			_, _ = w.Write([]byte(`{"Code":0,"Data":[{"AssetId":111,"Status":0,"Remark":"cannot lease"}]}`))
		default:
			w.WriteHeader(404)
		}
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := NewAdapter(c).PublishLease(context.Background(), []platform.PublishLeaseRequest{
		{AssetRef: "111", RentPrice: 1, MaxDays: 30, Deposit: 10},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res[0].Success || res[0].Error != "cannot lease" {
		t.Fatalf("result: %+v", res[0])
	}
}

func TestSMSLoginFlow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/user/Auth/SendSignInSmsCode":
			var req map[string]any
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req["Mobile"] != "13800000000" || req["Sessionid"] != "sess" {
				t.Errorf("sms payload: %v", req)
			}
			_, _ = w.Write([]byte(`{"Code":0,"Msg":"发送成功"}`))
		case "/api/user/Auth/SmsSignIn":
			_, _ = w.Write([]byte(`{"Code":0,"Msg":"ok","Data":{"Token":"tok123"}}`))
		case "/api/user/Auth/SmsUpSignIn":
			_, _ = w.Write([]byte(`{"Code":0,"Msg":"ok","Data":{"Token":"tokUp"}}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	hc := mockHTTP(srv.URL)
	ctx := context.Background()

	res, err := SendLoginSmsCode(ctx, hc, "13800000000", "sess")
	if err != nil || res.Mode != SmsModeDownlink {
		t.Fatalf("mode=%q err=%v", res.Mode, err)
	}
	tok, err := SmsSignIn(ctx, hc, "13800000000", "123456", "sess")
	if err != nil || tok != "tok123" {
		t.Fatalf("token=%q err=%v", tok, err)
	}
	tok, err = SmsSignIn(ctx, hc, "13800000000", "", "sess")
	if err != nil || tok != "tokUp" {
		t.Fatalf("smsup token=%q err=%v", tok, err)
	}
}

func TestSMSUplinkFlow(t *testing.T) {
	sendMsg := ""
	var gotHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/user/Auth/SendSignInSmsCode":
			gotHeaders = r.Header.Clone()
			_, _ = w.Write([]byte(`{"Code":0,"Msg":"` + sendMsg + `"}`))
		case "/api/user/Auth/GetSmsUpSignInConfig":
			if r.Method != http.MethodGet {
				t.Errorf("upconfig method = %s", r.Method)
			}
			_, _ = w.Write([]byte(`{"Code":0,"Msg":"ok","Data":{"SmsUpContent":"YZ#123456","SmsUpNumber":"106900001"}}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	hc := mockHTTP(srv.URL)
	ctx := context.Background()

	sendMsg = "验证码发送成功"
	res, err := SendLoginSmsCode(ctx, hc, "13800000000", "sess")
	if err != nil || res.Mode != SmsModeDownlink {
		t.Fatalf("down mode=%q err=%v", res.Mode, err)
	}
	if gotHeaders.Get("User-Agent") != "okhttp/3.14.9" ||
		gotHeaders.Get("App-Version") == "" ||
		gotHeaders.Get("DeviceToken") != "sess" {
		t.Fatalf("auth headers missing: %v", gotHeaders)
	}

	sendMsg = "暂未收到您的短信，请重新点击一键发送后，再次点击“我已发送”"
	res, err = SendLoginSmsCode(ctx, hc, "13800000000", "sess")
	if err != nil || res.Mode != SmsModeUplink {
		t.Fatalf("up mode=%q err=%v", res.Mode, err)
	}

	cfg, err := GetSmsUpSignInConfig(ctx, hc)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Content != "YZ#123456" || cfg.Number != "106900001" {
		t.Fatalf("upconfig: %+v", cfg)
	}
}

func TestSMSLoginPlatformError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/user/Auth/SendSignInSmsCode" {
			_, _ = w.Write([]byte(`{"Code":84104,"Msg":"risk"}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	res, err := SendLoginSmsCode(context.Background(), mockHTTP(srv.URL), "13800000000", "sess")
	if !errors.Is(err, platform.ErrPlatformBlocked) {
		t.Fatalf("want ErrPlatformBlocked, got %v (res=%+v)", err, res)
	}
}
