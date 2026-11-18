package pricing_test

import (
	"context"
	"slices"
	"sort"
	"testing"

	"github.com/IQVO/checkout/internal/application/exchange"
	cs "github.com/IQVO/checkout/internal/domain/checkoutsession"
)

// The price quote request names the group's tier as it stands: CONSOLIDATED is
// forwarded verbatim (pricing decides what it charges; this service invents no
// mapping to another tier) and the optional offer fields of the cart are not
// part of the pricing contract, so they are never sent.
func TestIssueQuoteForwardsAConsolidatedTierAndNothingElseOfTheOffer(t *testing.T) {
	eligible := true
	req := exchange.QuoteRequest{
		CartID: "cart-1", DestinationPostalPrefix: "941", Lines: request.Lines,
		Groups: []cs.Group{{GroupNo: 1, OfferID: "o1", Tier: cs.TierConsolidated, LineNos: []int{1},
			ConsolidationEligible: &eligible, CapacityBand: cs.BandTight}},
	}
	g, got := serve(t, 201, quoteJSON)
	if _, err := g.IssueQuote(context.Background(), "key-1", req); err != nil {
		t.Fatal(err)
	}
	grp := got.body["groups"].([]any)[0].(map[string]any)
	keys := make([]string, 0, len(grp))
	for k := range grp {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if grp["tier"] != "CONSOLIDATED" || !slices.Equal(keys, []string{"groupNo", "lineNos", "offerId", "tier"}) {
		t.Fatalf("group = %v", grp)
	}
}
