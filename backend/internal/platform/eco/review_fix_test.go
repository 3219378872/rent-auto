package eco

// Mock regression tests for the review-fix round: ECO code→sentinel mapping,
// market-dump fail-closed decode, delivery per-item ErrorCode judgment.

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/3219378872/rent-auto/backend/internal/platform"
)

func TestCheckEnvCodeSentinels(t *testing.T) {
	c := &Client{}
	cases := []struct {
		name string
		code int
		want error // nil = generic error, must NOT match any sentinel
	}{
		{"missing-steamid-stays-generic", 2001, nil},
		{"bad-timestamp-is-auth-env", 5003, platform.ErrAuthExpired},
		{"window-limit-backs-off-as-ratelimit", 7002, platform.ErrRateLimited},
		{"rate-limited", 6001, platform.ErrRateLimited},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := c.checkEnv(&envelope{Code: tc.code, Msg: "m"}, "p")
			if err == nil {
				t.Fatal("want error, got nil")
			}
			if tc.want != nil {
				if !errors.Is(err, tc.want) {
					t.Fatalf("want %v, got %v", tc.want, err)
				}
				return
			}
			for _, s := range []error{platform.ErrAuthExpired, platform.ErrRateLimited, platform.ErrPlatformBlocked, platform.ErrPartialFailure} {
				if errors.Is(err, s) {
					t.Fatalf("deterministic caller bug must not map to %v: %v", s, err)
				}
			}
		})
	}
}

func TestMarketDumpDecodeFailClosed(t *testing.T) {
	// Garbage payloads must error (never nil,nil starvation).
	for _, resp := range []string{okEnv(`[broken`), okEnv(`"oops"`), okEnv(`123`)} {
		c, _ := newTestClient(t, func(t *testing.T, r *http.Request, b map[string]any) string { return resp })
		if rows, err := c.GetMarketPriceDump(context.Background()); err == nil || rows != nil {
			t.Fatalf("garbage must fail closed, got rows=%v err=%v (resp=%s)", rows, err, resp)
		}
	}
	// Empty-but-wellformed shapes stay valid empties.
	for _, resp := range []string{okEnv(`{"List":[]}`), okEnv(`[]`), okEnv(`null`)} {
		c, _ := newTestClient(t, func(t *testing.T, r *http.Request, b map[string]any) string { return resp })
		rows, err := c.GetMarketPriceDump(context.Background())
		if err != nil {
			t.Fatalf("empty shape must not error (resp=%s): %v", resp, err)
		}
		if len(rows) != 0 {
			t.Fatalf("want empty, got %+v (resp=%s)", rows, resp)
		}
	}
}

func TestDeliveryPerItemFailed(t *testing.T) {
	sends := []SendOfferResult{
		{OrderNum: "ok-code", ErrorCode: 1, OfferID: "1"},
		{OrderNum: "ok-legacy", OfferID: "2"},
		{OrderNum: "bad-code", ErrorCode: 108, Error: "TooManyPending"},
		{OrderNum: "bad-text", Error: "限流"},
		{OrderNum: "confirm-pending", OfferID: "3", NeedsMobileConfirmation: true},
	}
	want := []bool{false, false, true, true, false}
	for i, s := range sends {
		if s.Failed() != want[i] {
			t.Fatalf("send %s Failed=%v, want %v", s.OrderNum, s.Failed(), want[i])
		}
	}
	accepts := []AcceptOfferResult{
		{OrderNum: "a-ok", ErrorCode: 1},
		{OrderNum: "a-bad", ErrorCode: 108, Error: "TooManyPending"},
		{OrderNum: "a-unknown", Error: ""},
	}
	wantA := []bool{false, true, true}
	for i, a := range accepts {
		if a.Failed() != wantA[i] {
			t.Fatalf("accept %s Failed=%v, want %v", a.OrderNum, a.Failed(), wantA[i])
		}
	}
}

func TestSellerSendOfferFailsOnItemError(t *testing.T) {
	c, _ := newTestClient(t, func(t *testing.T, r *http.Request, body map[string]any) string {
		return okEnv(`{"OrderNum":"ZH9","ErrorCode":108,"Error":"TooManyPending"}`)
	})
	res, err := c.SellerSendOffer(context.Background(), "ZH9")
	if err == nil {
		t.Fatalf("item-level 108 must error, got %+v", res)
	}
	if res == nil || res.ErrorCode != 108 {
		t.Fatalf("result body must stay available: %+v", res)
	}
}

func TestOneClickFailedHelpers(t *testing.T) {
	c, _ := newTestClient(t, func(t *testing.T, r *http.Request, body map[string]any) string {
		return okEnv(`{"sendOfferResults":[
			{"OrderNum":"ZH1","OfferId":"100001"},
			{"OrderNum":"ZH2","ErrorCode":108,"Error":"TooManyPending"}],
			"acceptOfferResults":[
			{"OrderNum":"DB3","OfferId":"100002","ErrorCode":1},
			{"OrderNum":"DB4","ErrorCode":100,"Error":"NotSettled"}]}`)
	})
	out, err := c.OneClickResolveOffer(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := out.FailedSends(); len(got) != 1 || got[0] != "ZH2" {
		t.Fatalf("FailedSends=%v", got)
	}
	if got := out.FailedAccepts(); len(got) != 1 || got[0] != "DB4" {
		t.Fatalf("FailedAccepts=%v", got)
	}
}
