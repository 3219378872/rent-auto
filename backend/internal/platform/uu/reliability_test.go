package uu

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"

	"github.com/3219378872/rent-auto/backend/internal/platform"
)

func TestNullCodeFailsClosed(t *testing.T) {
	env, err := decodeEnvelope([]byte(`{"Code":null,"Data":null}`))
	if err == nil && checkEnv(env, "offshelf") == nil {
		t.Fatal("Code:null treated as success")
	}
}

func TestDeliveryStopsBatchOnFirstPollRisk(t *testing.T) {
	sends, polls := 0, 0
	c, err := newMockUU(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch r.URL.Path {
		case "/api/user/Account/getUserInfo":
			okUserInfo(w)
		case "/api/youpin/bff/trade/todo/v1/orderTodo/list":
			_, _ = w.Write([]byte(`{"Code":0,"Data":[{"orderNo":"1","message":"有买家下单，待您发送报价"},{"orderNo":"2","message":"有买家下单，待您发送报价"}]}`))
		case "/api/youpin/bff/trade/v1/order/sell/delivery/send-offer":
			sends++
			_, _ = w.Write([]byte(`{"Code":0}`))
		case "/api/youpin/bff/trade/v1/order/sell/delivery/get-offer-status":
			polls++
			_, _ = w.Write([]byte(`{"Code":84104}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = c.DeliverPendingRentals(context.Background(), 5, nil)
	if !errors.Is(err, platform.ErrPlatformBlocked) || sends != 1 || polls != 1 {
		t.Fatalf("sends=%d polls=%d err=%v", sends, polls, err)
	}
}

func TestHealthyConcurrentWithMarket(t *testing.T) {
	c, err := newMockUU(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch r.URL.Path {
		case "/api/user/Account/getUserInfo":
			okUserInfo(w)
		case "/api/homepage/v3/detail/commodity/list/lease":
			_, _ = w.Write([]byte(`{"Code":0,"Data":{"CommodityList":[]}}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			_ = NewAdapter(c).Healthy(context.Background())
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			_, _ = c.GetMarketLeasePrice(context.Background(), 1, 50, 100, 15)
			_ = c.Nickname()
		}
	}()
	wg.Wait()
}
