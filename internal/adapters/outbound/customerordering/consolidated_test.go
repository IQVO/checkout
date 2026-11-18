package customerordering_test

import (
	"context"
	"testing"

	"github.com/IQVO/checkout/internal/adapters/outbound/customerordering"
	"github.com/IQVO/checkout/internal/application/exchange"
	cs "github.com/IQVO/checkout/internal/domain/checkoutsession"
)

// The pinned customer-ordering CheckoutGroup names groupNo, offerId and lineNos
// only: the tier and the consolidation choice ride in the offerId (its ADR 0013),
// so a CONSOLIDATED group is placed byte for byte like any other and nothing
// of the cart's optional offer fields is sent.
func TestAConsolidatedGroupIsPlacedWithTheOfferIdAlone(t *testing.T) {
	eligible := true
	consolidated := exchange.PlacementDraft{
		ShopperID: "shopper-1", ShipToAddressID: "addr-1", Lines: draft.Lines,
		Groups: []cs.Group{{GroupNo: 1, OfferID: "o1", Tier: cs.TierConsolidated, LineNos: []int{1, 2}, AmountMinor: 99,
			ConsolidationEligible: &eligible, CapacityBand: cs.BandScarce}},
	}
	got, err := customerordering.BuildRequest(consolidated)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != wantBody {
		t.Fatalf("body %s, want %s", got, wantBody)
	}
}

func TestARequotedConsolidatedOfferIsDecoded(t *testing.T) {
	body := `{"type":"/problems/offer-not-quotable","title":"x","status":409,"requotedOffers":[
	{"groupNo":1,"offerId":"o9","tier":"CONSOLIDATED","deliverByEarliest":"2026-10-26T08:00:00Z","deliverByLatest":"2026-10-27T20:00:00Z","expiresAt":"2026-10-25T12:00:00Z"}]}`
	g, _ := serve(t, 409, body)
	out, err := g.Place(context.Background(), "s", []byte("{}"))
	if err != nil || out.Kind != exchange.OutcomeRequote || len(out.Offers) != 1 || out.Offers[0].Tier != cs.TierConsolidated {
		t.Fatalf("out %+v err %v", out, err)
	}
}
