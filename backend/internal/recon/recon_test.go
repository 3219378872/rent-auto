package recon

import (
	"context"
	"testing"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/store"

	"github.com/3219378872/rent-auto/backend/internal/domain"
)

func TestDesiredChannelsMatrix(t *testing.T) {
	cases := []struct {
		route     string
		uuHealthy bool
		want      []domain.Channel
	}{
		{"uu_only", true, []domain.Channel{domain.ChannelUU}},
		{"uu_only", false, []domain.Channel{domain.ChannelUU}},
		{"eco_only", true, []domain.Channel{domain.ChannelECO}},
		{"both", true, []domain.Channel{domain.ChannelUU, domain.ChannelECO}},
		{"uu_primary_eco_fallback", true, []domain.Channel{domain.ChannelUU}},
		{"uu_primary_eco_fallback", false, []domain.Channel{domain.ChannelECO}},
		{"unknown_route", true, []domain.Channel{domain.ChannelUU, domain.ChannelECO}},
	}
	for _, c := range cases {
		got := desiredChannels(c.route, c.uuHealthy)
		if len(got) != len(c.want) {
			t.Fatalf("route=%s healthy=%v got=%v want=%v", c.route, c.uuHealthy, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("route=%s healthy=%v got=%v want=%v", c.route, c.uuHealthy, got, c.want)
			}
		}
	}
}

func TestPlanDecisionGatesPublish(t *testing.T) {
	p := &Planner{Now: time.Now().UTC()}
	d := p.decideFor(context.TODO(), domain.ChannelECO, routableNoAnchor())
	if d == nil || d.OK || d.SkipReason != "no_value_anchor" {
		t.Fatalf("missing anchor must gate publish: %+v", d)
	}
}

func routableNoAnchor() store.RoutableItem {
	return store.RoutableItem{AssetID: "a1", HashName: "H", Route: "both"}
}
