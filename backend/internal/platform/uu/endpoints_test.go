package uu

import (
	"bytes"
	"compress/gzip"
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

// A 200 response carrying a business error code is a FAILED delist — it must
// surface as an error (and never be audited as applied), or ghost shelves keep
// being repriced/delisted forever.
func TestOffShelfBusinessFailureEnvelope(t *testing.T) {
	tests := []struct {
		name string
		body string
		want error  // non-nil: errors.Is target; nil: match wantMsg instead
		msg  string // substring expected when want == nil
	}{
		{"auth-expired", `{"Code":84101,"Msg":"login required"}`, platform.ErrAuthExpired, ""},
		{"risk-control", `{"Code":84104,"Msg":"blocked"}`, platform.ErrPlatformBlocked, ""},
		{"generic-biz-code", `{"Code":84103,"Msg":"commodity busy"}`, nil, "offshelf code=84103"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, err := newMockUU(t, func(w http.ResponseWriter, r *http.Request) bool {
				switch r.URL.Path {
				case "/api/user/Account/getUserInfo":
					okUserInfo(w)
				case "/api/commodity/Commodity/OffShelf":
					_, _ = w.Write([]byte(tc.body))
				default:
					w.WriteHeader(404)
				}
				return true
			})
			if err != nil {
				t.Fatal(err)
			}
			err = NewAdapter(c).Delist(context.Background(), []string{"11"})
			if tc.want != nil {
				if !errors.Is(err, tc.want) {
					t.Fatalf("want %v, got %v", tc.want, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.msg) {
				t.Fatalf("want failure containing %q, got %v", tc.msg, err)
			}
		})
	}
}

func TestEnableZeroCDBusinessFailureEnvelope(t *testing.T) {
	c, err := newMockUU(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch r.URL.Path {
		case "/api/user/Account/getUserInfo":
			okUserInfo(w)
		case "/api/youpin/bff/order/sublet/open":
			_, _ = w.Write([]byte(`{"Code":1,"Msg":"order not sublettable"}`))
		default:
			w.WriteHeader(404)
		}
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	err = c.EnableZeroCD(context.Background(), []int64{555})
	if err == nil || !strings.Contains(err.Error(), "zerocd-open code=1") {
		t.Fatalf("zeroCD business failure must fail loudly, got %v", err)
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
	// round7 契约：逐项失败以哨兵暴露，结果字段保持权威
	if !errors.Is(err, platform.ErrPartialFailure) {
		t.Fatalf("want ErrPartialFailure, got %v", err)
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
			_, _ = w.Write([]byte(`{"Code":0,"Msg":"发送成功","Data":{"loginReqTicket":"lr-7","secs":60}}`))
		case "/api/user/Auth/SmsSignIn":
			var req map[string]any
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req["loginReqTicket"] != "lr-7" {
				t.Errorf("signin payload missing loginReqTicket: %v", req)
			}
			_, _ = w.Write([]byte(`{"Code":0,"Msg":"ok","Data":{"Token":"tok123"}}`))
		case "/api/user/Auth/SmsUpSignIn":
			var up map[string]any
			_ = json.NewDecoder(r.Body).Decode(&up)
			if up["loginReqTicket"] != nil {
				t.Errorf("smsup payload must omit empty loginReqTicket: %v", up)
			}
			_, _ = w.Write([]byte(`{"Code":0,"Msg":"ok","Data":{"Token":"tokUp"}}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	hc := mockHTTP(srv.URL)
	ctx := context.Background()

	res, err := SendLoginSmsCode(ctx, hc, "13800000000", "sess", nil)
	if err != nil || res.Mode != SmsModeDownlink {
		t.Fatalf("mode=%q err=%v", res.Mode, err)
	}
	if res.LoginReqTicket != "lr-7" || res.Secs != 60 {
		t.Fatalf("verify data parse: %+v", res)
	}
	tok, err := SmsSignIn(ctx, hc, "13800000000", "123456", "sess", res.LoginReqTicket)
	if err != nil || tok != "tok123" {
		t.Fatalf("token=%q err=%v", tok, err)
	}
	tok, err = SmsSignIn(ctx, hc, "13800000000", "", "sess", "")
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
	res, err := SendLoginSmsCode(ctx, hc, "13800000000", "sess", nil)
	if err != nil || res.Mode != SmsModeDownlink {
		t.Fatalf("down mode=%q err=%v", res.Mode, err)
	}
	if gotHeaders.Get("User-Agent") != "okhttp/3.14.9" ||
		gotHeaders.Get("App-Version") == "" ||
		gotHeaders.Get("DeviceToken") != "sess" {
		t.Fatalf("auth headers missing: %v", gotHeaders)
	}

	sendMsg = "暂未收到您的短信，请重新点击一键发送后，再次点击“我已发送”"
	res, err = SendLoginSmsCode(ctx, hc, "13800000000", "sess", nil)
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

	res, err := SendLoginSmsCode(context.Background(), mockHTTP(srv.URL), "13800000000", "sess", nil)
	if !errors.Is(err, platform.ErrPlatformBlocked) {
		t.Fatalf("want ErrPlatformBlocked, got %v (res=%+v)", err, res)
	}
}

func TestSMSSendCaptchaBlocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/user/Auth/SendSignInSmsCode" {
			_, _ = w.Write([]byte(`{"Code":0,"Msg":"需进行图形校验","Data":{"BehaviorVerifyReqTicket":"tk-1","Secs":30}}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	res, err := SendLoginSmsCode(context.Background(), mockHTTP(srv.URL), "13800000000", "sess", nil)
	if err != nil {
		t.Fatalf("captcha mode must not error, got %v", err)
	}
	if res.Mode != SmsModeCaptcha {
		t.Fatalf("mode=%q, want captcha", res.Mode)
	}
	if res.ReqTicket != "tk-1" || res.Secs != 30 {
		t.Fatalf("captcha correlation data: %+v", res)
	}
}

func TestSMSSendCaptchaRetryPayload(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/user/Auth/SendSignInSmsCode" {
			_ = json.NewDecoder(r.Body).Decode(&got)
			_, _ = w.Write([]byte(`{"Code":0,"Msg":"发送成功","Data":{"loginReqTicket":"lr-9","secs":60}}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	res, err := SendLoginSmsCode(context.Background(), mockHTTP(srv.URL), "13800000000", "sess",
		&CaptchaResult{Ticket: "tr03-x*", Randstr: "@Z4w", ReqTicket: "tk-1"})
	if err != nil || res.Mode != SmsModeDownlink {
		t.Fatalf("mode=%q err=%v", res.Mode, err)
	}
	bvr, ok := got["behaviorVerifyResult"].(map[string]any)
	if !ok || bvr["ticket"] != "tr03-x*" || bvr["randstr"] != "@Z4w" || bvr["reqTicket"] != "tk-1" {
		t.Fatalf("behaviorVerifyResult payload: %v", got["behaviorVerifyResult"])
	}
	if res.LoginReqTicket != "lr-9" || res.Secs != 60 {
		t.Fatalf("success verify data: %+v", res)
	}
}

func TestSmsSignInGzipBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/user/Auth/SmsSignIn" {
			w.WriteHeader(404)
			return
		}
		if r.Header.Get("Accept-Encoding") != "" && r.Header.Get("Accept-Encoding") != "gzip" {
			t.Errorf("unexpected accept-encoding %q", r.Header.Get("Accept-Encoding"))
		}
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		_, _ = gz.Write([]byte(`{"Code":0,"Msg":"ok","Data":{"Token":"tokGz"}}`))
		_ = gz.Close()
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(buf.Bytes())
	}))
	defer srv.Close()

	tok, err := SmsSignIn(context.Background(), mockHTTP(srv.URL), "13800000000", "123456", "sess", "")
	if err != nil || tok != "tokGz" {
		t.Fatalf("token=%q err=%v", tok, err)
	}
}

// The leased-out payload's asset field name awaits real-machine calibration;
// candidate spellings must map into LeaseOrder.AssetID so the factor
// controller's listing join can work the moment the platform sends one.
func TestLeasedOutOrderAssetRefMapping(t *testing.T) {
	c, err := newMockUU(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch r.URL.Path {
		case "/api/user/Account/getUserInfo":
			okUserInfo(w)
		case "/api/youpin/bff/trade/v1/order/lease/out/list":
			_, _ = w.Write([]byte(`{"Code":0,"data":{"orderDataList":[
				{"orderId":"o1","assetId":"asset-top","commodityInfo":{"name":"X","steamAssetId":"asset-nested"},"orderStatus":2},
				{"orderId":"o2","commodityInfo":{"name":"Y","steamAssetId":"asset-nested-only"},"orderStatus":2},
				{"orderId":"o3","commodityInfo":{"name":"Z"},"orderStatus":2}
			]}}`))
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
	if len(orders) != 3 {
		t.Fatalf("orders=%d", len(orders))
	}
	if orders[0].AssetID != "asset-top" || orders[1].AssetID != "asset-nested-only" || orders[2].AssetID != "" {
		t.Fatalf("asset mapping: %q %q %q", orders[0].AssetID, orders[1].AssetID, orders[2].AssetID)
	}
}
