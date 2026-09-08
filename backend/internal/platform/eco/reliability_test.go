package eco

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestMalformedResultCodeFailsClosed(t *testing.T) {
	for _, code := range []string{`"garbage"`, `null`, `true`, `{}`, `0.5`} {
		t.Run(code, func(t *testing.T) {
			env, err := decodeEnvelope([]byte(`{"ResultCode":` + code + `,"ResultData":[]}`))
			if err == nil && (&Client{}).checkEnv(env, "test") == nil {
				t.Fatalf("malformed ResultCode %s treated as success", code)
			}
		})
	}
}

func TestDelistRequiresEveryResult(t *testing.T) {
	for _, response := range []string{`null`, `[]`, `[{"GoodsNum":"other","IsSuccess":true}]`,
		`[{"GoodsNum":"expected","IsSuccess":true},{"GoodsNum":"expected","IsSuccess":true}]`} {
		t.Run(response, func(t *testing.T) {
			c, _ := newTestClient(t, func(_ *testing.T, _ *http.Request, _ map[string]any) string {
				return okEnv(response)
			})
			if err := NewAdapter(c, "sid").Delist(context.Background(), []string{"expected"}); err == nil {
				t.Fatalf("Delist claimed success: %s", response)
			}
		})
	}
}

func TestInventoryPaginationCompleteOrError(t *testing.T) {
	for _, scenario := range []string{"total", "missing_total", "page_error", "premature_empty"} {
		t.Run(scenario, func(t *testing.T) {
			calls := 0
			c, _ := newTestClient(t, func(t *testing.T, _ *http.Request, body map[string]any) string {
				calls++
				pageIndex := int(body["PageIndex"].(float64))
				if pageIndex == 2 && scenario == "page_error" {
					return `{"ResultCode":5005}`
				}
				items := []map[string]any{}
				for i := (pageIndex-1)*100 + 1; i <= pageIndex*100 && i <= 101; i++ {
					if pageIndex == 2 && scenario == "premature_empty" {
						break
					}
					items = append(items, map[string]any{"AssetId": fmt.Sprintf("A%d", i), "HashName": "template", "Price": 100, "Tradable": true, "Status": 1})
				}
				payload := map[string]any{"PageResult": items}
				if scenario != "missing_total" {
					payload["TotalRecord"] = 101
				}
				result, err := json.Marshal(payload)
				if err != nil {
					t.Fatal(err)
				}
				return okEnv(string(result))
			})
			items, err := NewAdapter(c, "sid").Inventory(context.Background())
			if scenario == "page_error" || scenario == "premature_empty" {
				if err == nil || items != nil {
					t.Fatalf("incomplete snapshot must fail, items=%d err=%v", len(items), err)
				}
				return
			}
			if err != nil || len(items) != 101 || calls != 2 {
				t.Fatalf("assets=%d requests=%d err=%v", len(items), calls, err)
			}
		})
	}
}
