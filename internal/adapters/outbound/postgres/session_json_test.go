package postgres

import (
	"strings"
	"testing"

	"github.com/IQVO/checkout/internal/domain/checkoutsession"
)

func TestGroupsJSONRoundTripsTheConsolidatedOfferFields(t *testing.T) {
	for _, eligible := range []bool{true, false} {
		in := []checkoutsession.Group{
			{GroupNo: 1, OfferID: "o1", Tier: checkoutsession.TierNextDay, LineNos: []int{1}, AmountMinor: 10},
			{GroupNo: 2, OfferID: "o2", Tier: checkoutsession.TierConsolidated, LineNos: []int{2}, AmountMinor: 20,
				ConsolidationEligible: &eligible, CapacityBand: checkoutsession.BandScarce},
		}
		raw, err := marshalGroups(in)
		if err != nil {
			t.Fatal(err)
		}
		back, err := unmarshalGroups(raw)
		if err != nil {
			t.Fatal(err)
		}
		g := back[1]
		if g.Tier != checkoutsession.TierConsolidated || g.ConsolidationEligible == nil || *g.ConsolidationEligible != eligible || g.CapacityBand != checkoutsession.BandScarce || g.AmountMinor != 20 {
			t.Fatalf("consolidated group = %+v", g)
		}
		if back[0].ConsolidationEligible != nil || back[0].CapacityBand != "" {
			t.Fatalf("classic group = %+v", back[0])
		}
	}
}

// A session stored before the CONSOLIDATED tier has neither key, and a group
// without them is written exactly as before.
func TestGroupsJSONOfAGroupWithoutTheOptionalFieldsIsUnchanged(t *testing.T) {
	raw, err := marshalGroups([]checkoutsession.Group{{GroupNo: 1, OfferID: "o1", Tier: checkoutsession.TierStandard, LineNos: []int{1}, AmountMinor: 5}})
	if err != nil {
		t.Fatal(err)
	}
	if s := string(raw); s != `[{"groupNo":1,"offerId":"o1","tier":"STANDARD","lineNos":[1],"amountMinor":5}]` || strings.Contains(s, "consolidationEligible") {
		t.Fatalf("stored group = %s", s)
	}
	back, err := unmarshalGroups(raw)
	if err != nil || back[0].ConsolidationEligible != nil || back[0].CapacityBand != "" {
		t.Fatalf("%+v %v", back, err)
	}
}
