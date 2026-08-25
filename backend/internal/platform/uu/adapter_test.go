package uu

import (
	"context"
	"net/http"
	"testing"

	"errors"
	"github.com/3219378872/rent-auto/backend/internal/domain"
	"github.com/3219378872/rent-auto/backend/internal/platform"
)

func TestAdapterSurface(t *testing.T) {
	c, err := newMockUU(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch r.URL.Path {
		case "/api/user/Account/getUserInfo":
			okUserInfo(w)
		case "/api/commodity/Inventory/GetUserInventoryDataListV3":
			_, _ = w.Write([]byte(`{"Code":0,"Data":{"ItemsInfos":[
				{"SteamAssetId":"a1","AssetStatus":0,"Tradable":true,"ShotName":"AK红线的短名","TemplateInfo":{"Id":11,"MarkPrice":123.45},"MarketHashName":"AK-47 | Redline (Field-Tested)"},
				{"SteamAssetId":"a2","AssetStatus":2,"Tradable":false,"ShotName":"locked","TemplateInfo":{"Id":12,"MarkPrice":10},"MarketHashName":"Item (FN)"}
			]}}`))
		case "/api/youpin/bff/new/commodity/v1/commodity/list/lease":
			_, _ = w.Write([]byte(`{"code":0,"data":{"commodityInfoList":[
				{"id":77,"steamAssetId":"a1","templateId":11,"name":"N","depositAmount":"50","shortLeaseAmount":"1.5","longLeaseAmount":"","leaseMaxDays":30,"commodityCanSell":false,"commodityCanLease":true}]}}`))
		case "/api/youpin/bff/new/commodity/v1/commodity/list/zeroCDLease":
			_, _ = w.Write([]byte(`{"code":9004001}`))
		default:
			w.WriteHeader(404)
		}
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	ad := NewAdapter(c)
	ctx := context.Background()

	if ad.Channel() != domain.ChannelUU {
		t.Fatal("channel")
	}
	caps := ad.Caps()
	if !caps.DepositDirect || caps.MaxBatchPublish <= 0 {
		t.Fatalf("caps: %+v", caps)
	}

	if err := ad.Healthy(ctx); err != nil {
		t.Fatal(err)
	}

	inv, err := ad.Inventory(ctx)
	if err != nil || len(inv) != 2 {
		t.Fatalf("inventory: %v %d", err, len(inv))
	}
	if inv[0].Status != "in_stock" || inv[1].Status != "locked" {
		t.Fatalf("status mapping: %+v", inv)
	}

	shelf, err := ad.LeaseShelf(ctx)
	if err != nil || len(shelf) != 1 {
		t.Fatalf("shelf: %v %d", err, len(shelf))
	}
	if shelf[0].GoodsRef != "77" || shelf[0].RentPrice != 1.5 || shelf[0].Deposit != 50 {
		t.Fatalf("shelf item: %+v", shelf[0])
	}

	if _, err := ad.Wallet(ctx); err != platform.ErrUnsupported {
		t.Fatalf("wallet should be unsupported on uu, got %v", err)
	}
}

func TestNewClientInvalidToken(t *testing.T) {
	_, err := newMockUU(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/api/user/Account/getUserInfo" {
			_, _ = w.Write([]byte(`{"Code":84101,"Msg":"expired"}`))
			return true
		}
		return false
	})
	if err == nil {
		t.Fatal("invalid token must fail construction")
	}
}

// PublishLease 部分失败必须以 ErrPartialFailure 哨兵暴露，同时结果数组
// 保持逐项权威（round7 契约统一，与 eco 对齐）。
func TestPublishLeasePartialFailureSentinel(t *testing.T) {
	c, err := newMockUU(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch r.URL.Path {
		case "/api/user/Account/getUserInfo":
			okUserInfo(w)
		case "/api/commodity/Inventory/SellInventoryWithLeaseV2":
			_, _ = w.Write([]byte(`{"Code":0,"Data":[
				{"AssetId":111,"Status":1,"CommodityId":999,"Remark":""},
				{"AssetId":222,"Status":2,"CommodityId":0,"Remark":"不可租"}
			]}`))
		default:
			w.WriteHeader(404)
		}
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := NewAdapter(c).PublishLease(context.Background(), []platform.PublishLeaseRequest{
		{AssetRef: "111", RentPrice: 1.2, MaxDays: 30, Deposit: 88.5},
		{AssetRef: "222", RentPrice: 2.0, MaxDays: 8, Deposit: 60},
	})
	if !errors.Is(err, platform.ErrPartialFailure) {
		t.Fatalf("want ErrPartialFailure, got %v", err)
	}
	if len(out) != 2 || !out[0].Success || out[0].GoodsRef != "999" || out[1].Success || out[1].Error != "不可租" {
		t.Fatalf("results must stay authoritative: %+v (%v)", out, err)
	}
}
