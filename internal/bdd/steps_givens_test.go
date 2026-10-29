package bdd

import (
	"time"

	"github.com/cucumber/godog"

	"github.com/IQVO/checkout/internal/adapters/outbound/memory"
	"github.com/IQVO/checkout/internal/application/exchange"
	"github.com/IQVO/checkout/internal/application/ports"
	cs "github.com/IQVO/checkout/internal/domain/checkoutsession"
)

// readyCart is a two-line cart in two groups, every group selected.
func readyCart() exchange.Cart {
	return exchange.Cart{DestinationPostalPrefix: "941", Snapshot: cs.CartSnapshot{
		CartID: "cart-1", Version: 3, Ready: true,
		Lines: []cs.CartLine{{CatalogItemID: "ITEM-A", Quantity: 2}, {CatalogItemID: "ITEM-B", Quantity: 1}},
		Groups: []cs.CartGroup{
			{GroupNo: 1, OfferID: "offer-1", Tier: cs.TierNextDay, LineNos: []int{1}},
			{GroupNo: 2, OfferID: "offer-2", Tier: cs.TierStandard, LineNos: []int{2}},
		},
	}}
}

func (w *world) stockCart(c exchange.Cart) {
	w.carts.Put(c)
	for _, g := range c.Snapshot.Groups {
		w.offers.Put(cs.OfferStatus{OfferID: g.OfferID, Quoted: true, ExpiresAt: t0.Add(time.Hour)})
	}
}

func registerGivens(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^a checkout-ready cart with 2 lines in 2 groups$`, func() error { w.stockCart(readyCart()); return nil })
	sc.Step(`^a cart that is not checkout ready$`, func() error {
		c := readyCart()
		c.Snapshot.Ready = false
		w.stockCart(c)
		return nil
	})
	sc.Step(`^a cart whose groups leave a line unshipped$`, func() error {
		c := readyCart()
		c.Snapshot.Groups = c.Snapshot.Groups[:1]
		w.stockCart(c)
		return nil
	})
	sc.Step(`^offer "([^"]*)" expires in (\d+) seconds$`, func(id string, secs int) error {
		w.offers.Put(cs.OfferStatus{OfferID: id, Quoted: true, ExpiresAt: t0.Add(time.Duration(secs) * time.Second)})
		return nil
	})
	sc.Step(`^offer "([^"]*)" is no longer quoted$`, func(id string) error {
		w.offers.Put(cs.OfferStatus{OfferID: id, Quoted: false, ExpiresAt: t0.Add(time.Hour)})
		return nil
	})
	sc.Step(`^pricing is unreachable$`, func() error { w.quoter.Err = ports.ErrPricingUnavailable; return nil })
	registerPaymentGivens(sc, w)
	registerOrderingGivens(sc, w)
}

func registerPaymentGivens(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^customer-payments declines with "([^"]*)"$`, func(reason string) error {
		w.payments.Script = append(w.payments.Script, memory.AuthStep{Result: exchange.Authorization{DeclineReason: reason}})
		return nil
	})
	sc.Step(`^customer-payments authorizes one minor unit too much$`, func() error {
		w.payments.Script = append(w.payments.Script, memory.AuthStep{Mutate: func(a *exchange.Authorization) { a.TotalMinor++ }})
		return nil
	})
	sc.Step(`^customer-payments is unreachable for authorizations$`, func() error { w.payments.AuthErr = ports.ErrPaymentsUnavailable; return nil })
	sc.Step(`^customer-payments recovers$`, func() error { w.payments.AuthErr = nil; return nil })
	sc.Step(`^customer-payments cannot void$`, func() error { w.payments.VoidErr = ports.ErrPaymentsUnavailable; return nil })
	sc.Step(`^customer-payments can void again$`, func() error { w.payments.VoidErr = nil; return nil })
}

func registerOrderingGivens(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^customer-ordering times out$`, func() error { w.placer.Fail = ports.ErrOrderingUnavailable; return nil })
	sc.Step(`^customer-ordering recovers$`, func() error { w.placer.Fail = nil; return nil })
	sc.Step(`^customer-ordering answers a definitive 409 requoting offer "([^"]*)"$`, func(offer string) error {
		w.placer.Script = append(w.placer.Script, memory.PlaceStep{Outcome: exchange.PlaceOutcome{Kind: exchange.OutcomeRequote, Offers: []cs.RequotedOffer{{
			OfferID: offer, GroupNo: 1, Tier: cs.TierSameDay, DeliverByEarliest: "2026-10-26", DeliverByLatest: "2026-10-27", ExpiresAt: t0.Add(time.Hour),
		}}}})
		return nil
	})
	sc.Step(`^customer-ordering rejects the request with 422$`, func() error {
		w.placer.Script = append(w.placer.Script, memory.PlaceStep{Outcome: exchange.PlaceOutcome{Kind: exchange.OutcomeRejected, Detail: "422 line-without-promise"}})
		return nil
	})
}
