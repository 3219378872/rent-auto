package uu

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/3219378872/rent-auto/backend/internal/platform"
)

// Behavioral spec: docs/knowledge/design/platform-steam-api-notes.md §4
// (orderTodo/list → send-offer → get-offer-status polling, gift skip).

func todoPage(items []string) string {
	return `{"Code":0,"Data":[` + strings.Join(items, ",") + `]}`
}

func todoJSON(orderNo, commodity, message string) string {
	b, _ := json.Marshal(map[string]string{
		"orderNo": orderNo, "commodityName": commodity, "message": message,
	})
	return string(b)
}

func TestGetWaitDeliverListPaginates(t *testing.T) {
	page := 0
	var seenPageIndex []float64
	c, err := newMockUU(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch {
		case strings.HasSuffix(r.URL.Path, "getUserInfo"):
			okUserInfo(w)
		case strings.HasSuffix(r.URL.Path, "/orderTodo/list"):
			var req map[string]any
			_ = json.NewDecoder(r.Body).Decode(&req)
			seenPageIndex = append(seenPageIndex, req["pageIndex"].(float64))
			if req["Sessionid"] == "" || req["userId"] == float64(7) == false {
				t.Errorf("todo list payload missing userId/Sessionid: %v", req)
			}
			page++
			if page == 1 {
				items := make([]string, 20)
				for i := range items {
					items[i] = todoJSON("o1", "Skin", "其他消息")
				}
				_, _ = w.Write([]byte(todoPage(items)))
				return true
			}
			_, _ = w.Write([]byte(todoPage([]string{todoJSON("o2", "Skin", "其他消息")})))
		default:
			w.WriteHeader(404)
		}
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	todos, err := c.GetWaitDeliverList(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(todos) != 21 {
		t.Fatalf("todos = %d, want 21", len(todos))
	}
	if len(seenPageIndex) != 2 || seenPageIndex[0] != 1 || seenPageIndex[1] != 2 {
		t.Fatalf("pagination broken: %v", seenPageIndex)
	}
}

func TestGetWaitDeliverListAuthExpired(t *testing.T) {
	c, err := newMockUU(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch {
		case strings.HasSuffix(r.URL.Path, "getUserInfo"):
			okUserInfo(w)
		case strings.HasSuffix(r.URL.Path, "/orderTodo/list"):
			_, _ = w.Write([]byte(`{"Code":84101,"Msg":"login expired"}`))
		default:
			w.WriteHeader(404)
		}
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetWaitDeliverList(context.Background()); !errors.Is(err, platform.ErrAuthExpired) {
		t.Fatalf("want ErrAuthExpired, got %v", err)
	}
}

func TestDeliverPendingRentalsHappyPath(t *testing.T) {
	var sentOrderNos []string
	var sendMethod string
	statusCalls := 0
	c, err := newMockUU(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch {
		case strings.HasSuffix(r.URL.Path, "getUserInfo"):
			okUserInfo(w)
		case strings.HasSuffix(r.URL.Path, "/orderTodo/list"):
			_, _ = w.Write([]byte(todoPage([]string{
				todoJSON("R100", "AK-47 | Redline", msgWaitSendOffer), // deliverable rental
				todoJSON("G200", "Gift Skin", "好友赠送饰品"),               // gift → skip
				todoJSON("X300", "Whatever", "系统通知"),                  // irrelevant → ignore
			})))
		case strings.HasSuffix(r.URL.Path, "/delivery/send-offer"):
			sendMethod = r.Method
			var req map[string]any
			_ = json.NewDecoder(r.Body).Decode(&req)
			sentOrderNos = append(sentOrderNos, req["orderNo"].(string))
			_, _ = w.Write([]byte(`{"Code":0,"Data":{}}`))
		case strings.HasSuffix(r.URL.Path, "/get-offer-status"):
			statusCalls++
			// first poll not ready, second poll confirms (status==3)
			st := 2
			if statusCalls >= 2 {
				st = 3
			}
			_, _ = w.Write([]byte(`{"Code":0,"Data":{"status":` + strconv.Itoa(st) + `}}`))
		default:
			w.WriteHeader(404)
		}
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	sent, gifts, err := c.DeliverPendingRentals(context.Background(), 5, func() {})
	if err != nil {
		t.Fatalf("delivery: %v", err)
	}
	if len(sent) != 1 || sent[0] != "R100" || gifts != 1 {
		t.Fatalf("sent=%v gifts=%d", sent, gifts)
	}
	if sendMethod != http.MethodPut {
		t.Fatalf("send-offer must be PUT, got %s", sendMethod)
	}
	if statusCalls != 2 {
		t.Fatalf("polls=%d, want 2 (retry until status==3)", statusCalls)
	}
}

func TestDeliverPendingRentalsSendOfferFails(t *testing.T) {
	c, err := newMockUU(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch {
		case strings.HasSuffix(r.URL.Path, "getUserInfo"):
			okUserInfo(w)
		case strings.HasSuffix(r.URL.Path, "/orderTodo/list"):
			_, _ = w.Write([]byte(todoPage([]string{todoJSON("R1", "S", msgWaitSendOffer)})))
		case strings.HasSuffix(r.URL.Path, "/delivery/send-offer"):
			_, _ = w.Write([]byte(`{"Code":500,"Msg":"boom"}`))
		default:
			w.WriteHeader(404)
		}
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	sent, gifts, err := c.DeliverPendingRentals(context.Background(), 3, func() {})
	if err == nil || !strings.Contains(err.Error(), "send offer R1") || len(sent) != 0 || gifts != 0 {
		t.Fatalf("send failure must surface: sent=%v gifts=%d err=%v", sent, gifts, err)
	}
}

func TestDeliverPendingRentalsPollTimeout(t *testing.T) {
	c, err := newMockUU(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch {
		case strings.HasSuffix(r.URL.Path, "getUserInfo"):
			okUserInfo(w)
		case strings.HasSuffix(r.URL.Path, "/orderTodo/list"):
			_, _ = w.Write([]byte(todoPage([]string{todoJSON("R9", "S", msgWaitSendOffer)})))
		case strings.HasSuffix(r.URL.Path, "/delivery/send-offer"):
			_, _ = w.Write([]byte(`{"Code":0,"Data":{}}`))
		case strings.HasSuffix(r.URL.Path, "/get-offer-status"):
			_, _ = w.Write([]byte(`{"Code":0,"Data":{"status":1}}`)) // never confirmed
		default:
			w.WriteHeader(404)
		}
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	polls := 0
	_, _, err = c.DeliverPendingRentals(context.Background(), 3, func() { polls++ })
	if err == nil || !strings.Contains(err.Error(), "not confirmed after 3 polls") {
		t.Fatalf("poll timeout must fail loudly: %v", err)
	}
	if polls != 3 {
		t.Fatalf("polls=%d, want 3", polls)
	}
}

func TestDeliverPendingRentalsEmptyTodo(t *testing.T) {
	c, err := newMockUU(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch {
		case strings.HasSuffix(r.URL.Path, "getUserInfo"):
			okUserInfo(w)
		case strings.HasSuffix(r.URL.Path, "/orderTodo/list"):
			_, _ = w.Write([]byte(todoPage(nil)))
		default:
			w.WriteHeader(404)
		}
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	sent, gifts, err := c.DeliverPendingRentals(context.Background(), 3, func() {})
	if err != nil || len(sent) != 0 || gifts != 0 {
		t.Fatalf("empty todo: sent=%v gifts=%d err=%v", sent, gifts, err)
	}
}
