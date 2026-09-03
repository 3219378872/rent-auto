package uu

// Mock regression tests for the review-fix round: envelope-gated login,
// pre-change init check, delivery lastErr, reprice length gate, new
// sentinels (5050/1110205) and CST time fallback.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/platform"
)

func smsSignInMock(t *testing.T, signInBody string) *http.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "SmsSignIn") || strings.HasSuffix(r.URL.Path, "SmsUpSignIn") {
			_, _ = w.Write([]byte(signInBody))
			return
		}
		w.WriteHeader(404)
	}))
	t.Cleanup(srv.Close)
	return mockHTTP(srv.URL)
}

func TestSmsSignInEmptyTokenFails(t *testing.T) {
	hc := smsSignInMock(t, `{"Code":0,"Msg":"ok","Data":{}}`)
	if _, err := SmsSignIn(context.Background(), hc, "13800000000", "123456", "sess", ""); err == nil ||
		!strings.Contains(err.Error(), "empty token") {
		t.Fatalf("missing token must fail loudly, got %v", err)
	}
}

func TestSmsSignInSentinelMapping(t *testing.T) {
	cases := []struct {
		name string
		body string
		want error
	}{
		{"version-blocked", `{"Code":5050,"Msg":"请更新至最新版本APP进行注册"}`, platform.ErrVersionBlocked},
		{"captcha-required", `{"Code":1110205,"Msg":"behavior verify"}`, platform.ErrCaptchaRequired},
		{"auth-expired", `{"Code":84101,"Msg":"login expired"}`, platform.ErrAuthExpired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hc := smsSignInMock(t, tc.body)
			_, err := SmsSignIn(context.Background(), hc, "13800000000", "123456", "sess", "")
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
		})
	}
}

func TestSendSmsCodeVersionBlocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"Code":5050,"Msg":"为了保证您的账户安全，请更新至最新版本APP进行注册"}`))
	}))
	defer srv.Close()
	_, err := SendLoginSmsCode(context.Background(), mockHTTP(srv.URL), "13800000000", "sess", nil)
	if !errors.Is(err, platform.ErrVersionBlocked) {
		t.Fatalf("want ErrVersionBlocked, got %v", err)
	}
}

func TestSendSmsCodeCaptchaCodeWithoutCaptchaMsg(t *testing.T) {
	// Code=1110205 with a non-captcha Msg still surfaces the sentinel so the
	// scheduler can cool down + audit (the nil-error captcha path only
	// applies when the Msg actually carries the challenge).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"Code":1110205,"Msg":"verify"}`))
	}))
	defer srv.Close()
	_, err := SendLoginSmsCode(context.Background(), mockHTTP(srv.URL), "13800000000", "sess", nil)
	if !errors.Is(err, platform.ErrCaptchaRequired) {
		t.Fatalf("want ErrCaptchaRequired, got %v", err)
	}
}

func TestChangeLeasePricesPrechangeInitChecked(t *testing.T) {
	c, err := newMockUU(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch r.URL.Path {
		case "/api/user/Account/getUserInfo":
			okUserInfo(w)
		case "/api/youpin/bff/new/commodity/commodity/change/price/v3/init/info":
			_, _ = w.Write([]byte(`{"Code":84104,"Msg":"risk"}`))
		default:
			w.WriteHeader(404)
		}
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = c.ChangeLeasePrices(context.Background(),
		[]ChangeLeaseItem{{CommodityID: 1, LeaseDeposit: "10", LeaseMaxDays: 30, LeaseUnitPrice: 1}}, 7)
	if !errors.Is(err, platform.ErrPlatformBlocked) {
		t.Fatalf("pre-change risk code must abort, got %v", err)
	}
	if !strings.Contains(err.Error(), "prechange init") {
		t.Fatalf("error must name the pre-change step, got %v", err)
	}
}

func TestDeliverPendingRentalsReturnsLastErr(t *testing.T) {
	c, err := newMockUU(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch r.URL.Path {
		case "/api/user/Account/getUserInfo":
			okUserInfo(w)
		case "/api/youpin/bff/trade/todo/v1/orderTodo/list":
			_, _ = w.Write([]byte(`{"Code":0,"Data":[{"orderNo":"R1","commodityName":"K","message":"` + msgWaitSendOffer + `"}]}`))
		case "/api/youpin/bff/trade/v1/order/sell/delivery/send-offer":
			_, _ = w.Write([]byte(`{"Code":0,"Data":{}}`))
		case "/api/youpin/bff/trade/v1/order/sell/delivery/get-offer-status":
			_, _ = w.Write([]byte(`{"Code":84104,"Msg":"risk"}`))
		default:
			w.WriteHeader(404)
		}
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = c.DeliverPendingRentals(context.Background(), 2, nil)
	if !errors.Is(err, platform.ErrPlatformBlocked) {
		t.Fatalf("poll errors must surface (not last=-1), got %v", err)
	}
}

func TestRepriceLengthGate(t *testing.T) {
	c, err := newMockUU(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch r.URL.Path {
		case "/api/user/Account/getUserInfo":
			okUserInfo(w)
		case "/api/youpin/bff/new/commodity/commodity/change/price/v3/init/info":
			_, _ = w.Write([]byte(`{"Code":0,"Data":{}}`))
		case "/api/commodity/Commodity/PriceChangeWithLeaseV2":
			// Only 1 per-item entry for 2 requested items.
			_, _ = w.Write([]byte(`{"Code":0,"Data":{"SuccessCount":1,"FailCount":0,"Commoditys":[{"CommodityId":1,"IsSuccess":1}]}}`))
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
	if !errors.Is(err, platform.ErrPartialFailure) {
		t.Fatalf("want ErrPartialFailure, got %v", err)
	}
	if !rep[0].Success {
		t.Fatalf("explicitly-ok item must stay success: %+v", rep[0])
	}
	if rep[1].Success || !strings.Contains(rep[1].Error, "missing per-item") {
		t.Fatalf("unaccounted item must fail closed: %+v", rep[1])
	}
}

func TestParseUUTimeCSTFallback(t *testing.T) {
	got := parseUUTime("2026-08-01 10:00:00")
	want := time.Date(2026, 8, 1, 10, 0, 0, 0, uuWallClock)
	if !got.Equal(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if got.UTC().Hour() != 2 {
		t.Fatalf("CST wall clock must land at 02:00 UTC, got %v", got.UTC())
	}
	if !parseUUTime("not-a-time").IsZero() {
		t.Fatal("garbage must yield zero time")
	}
}
