package bdd

import (
	"fmt"
	"strconv"

	"github.com/cucumber/godog"

	cs "github.com/IQVO/checkout/internal/domain/checkoutsession"
)

// registerConsolidated registers the steps of features/consolidated_tier.feature:
// a cart whose selected offer is of delivery-promise's opt-in CONSOLIDATED tier
// and carries the optional offer fields.
func registerConsolidated(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^a checkout-ready cart whose second group selected the (\w+) offer "([^"]*)" eligible (true|false) in band (\w+)$`,
		func(tier, offer, eligible, band string) error {
			v, err := strconv.ParseBool(eligible)
			if err != nil {
				return err
			}
			c := readyCart()
			g := &c.Snapshot.Groups[1]
			g.OfferID, g.Tier, g.ConsolidationEligible, g.CapacityBand = offer, cs.Tier(tier), &v, cs.CapacityBand(band)
			c.Snapshot.Groups[0].CapacityBand = cs.BandAmple
			w.stockCart(c)
			return nil
		})
	sc.Step(`^a checkout-ready cart whose second group selected the (\w+) offer "([^"]*)" in band (\w+)$`,
		func(tier, offer, band string) error {
			c := readyCart()
			g := &c.Snapshot.Groups[1]
			g.OfferID, g.Tier, g.CapacityBand = offer, cs.Tier(tier), cs.CapacityBand(band)
			w.stockCart(c)
			return nil
		})
	sc.Step(`^the response field "([^"]*)" is not present$`, func(path string) error {
		if v, ok := w.lookup(path); ok {
			return fmt.Errorf("field %s = %v, want it absent", path, v)
		}
		return nil
	})
}
