package checkoutsession_test

import (
	"testing"
	"time"

	cs "github.com/IQVO/checkout/internal/domain/checkoutsession"
)

func boolPtr(v bool) *bool { return &v }

// consolidatedInput is validInput with group 2 a CONSOLIDATED offer carrying
// the optional fields of the cart's selected offer.
func consolidatedInput(eligible bool) cs.OpenInput {
	in := validInput()
	in.Cart.Groups[1].Tier = cs.TierConsolidated
	in.Cart.Groups[1].ConsolidationEligible = boolPtr(eligible)
	in.Cart.Groups[1].CapacityBand = cs.BandTight
	in.Cart.Groups[0].CapacityBand = cs.BandAmple
	return in
}

func TestASessionOpensWithAConsolidatedOfferAndCarriesItsFields(t *testing.T) {
	for _, eligible := range []bool{true, false} {
		s, err := cs.Open(consolidatedInput(eligible), t0)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		g := s.Groups()
		if g[1].Tier != cs.TierConsolidated || g[1].ConsolidationEligible == nil || *g[1].ConsolidationEligible != eligible || g[1].CapacityBand != cs.BandTight {
			t.Fatalf("consolidated group = %+v", g[1])
		}
		if g[0].ConsolidationEligible != nil || g[0].CapacityBand != cs.BandAmple || g[0].Tier != cs.TierNextDay {
			t.Fatalf("classic group = %+v", g[0])
		}
		// the allocation is unchanged by the tier: the amounts are the quote's
		if g[0].AmountMinor+g[1].AmountMinor != 2100 {
			t.Fatalf("amounts = %d + %d, want the quote total 2100", g[0].AmountMinor, g[1].AmountMinor)
		}
	}
}

func TestAGroupWithoutTheOptionalFieldsIsUnchanged(t *testing.T) {
	g := opened(t).Groups()
	for _, grp := range g {
		if grp.ConsolidationEligible != nil || grp.CapacityBand != "" {
			t.Fatalf("%+v", grp)
		}
	}
}

func TestTheSessionOwnsItsEligibilityFlag(t *testing.T) {
	in := consolidatedInput(true)
	flag := in.Cart.Groups[1].ConsolidationEligible
	s, err := cs.Open(in, t0)
	if err != nil {
		t.Fatal(err)
	}
	*flag = false // the caller's cart snapshot changes afterwards
	if got := s.Groups()[1].ConsolidationEligible; got == nil || !*got {
		t.Fatalf("the session's flag followed the caller's: %v", got)
	}
	out := s.Groups()
	*out[1].ConsolidationEligible = false // a reader changes its copy
	if got := s.Groups()[1].ConsolidationEligible; got == nil || !*got {
		t.Fatalf("a returned group aliases the session: %v", got)
	}
	snap := s.Snapshot()
	*snap.Groups[1].ConsolidationEligible = false
	if got := s.Groups()[1].ConsolidationEligible; got == nil || !*got {
		t.Fatalf("the snapshot aliases the session: %v", got)
	}
}

func TestARestoredSessionKeepsTheGroupFields(t *testing.T) {
	s, err := cs.Open(consolidatedInput(true), t0)
	if err != nil {
		t.Fatal(err)
	}
	back, err := cs.Restore(s.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	g := back.Groups()[1]
	if g.Tier != cs.TierConsolidated || g.ConsolidationEligible == nil || !*g.ConsolidationEligible || g.CapacityBand != cs.BandTight {
		t.Fatalf("%+v", g)
	}
}

func TestARequotedConsolidatedOfferIsAccepted(t *testing.T) {
	s := placing(t)
	offer := cs.RequotedOffer{OfferID: "o9", GroupNo: 1, Tier: cs.TierConsolidated, DeliverByEarliest: "2026-10-26", DeliverByLatest: "2026-10-27", ExpiresAt: t0.Add(time.Hour)}
	if err := s.RecordRequoteRequired([]cs.RequotedOffer{offer}, t0.Add(3*time.Minute)); err != nil {
		t.Fatalf("RecordRequoteRequired: %v", err)
	}
}
