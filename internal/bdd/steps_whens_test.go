package bdd

import (
	"context"
	"fmt"
	"time"

	"github.com/cucumber/godog"

	"github.com/IQVO/checkout/internal/application/usecases"
)

func registerWhens(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^I open a checkout session$`, func() error { return w.openFor("cart-1") })
	sc.Step(`^I open a checkout session for cart "([^"]*)"$`, w.openFor)
	sc.Step(`^I confirm the session with token "([^"]*)"$`, func(token string) error {
		return w.post("/checkout-sessions/sess-1/confirmations", fmt.Sprintf(`{"paymentMethodToken":%q}`, token))
	})
	sc.Step(`^I read the session$`, func() error { return w.get("/checkout-sessions/sess-1") })
	sc.Step(`^I read the session "([^"]*)"$`, func(id string) error { return w.get("/checkout-sessions/" + id) })
	sc.Step(`^I POST "([^"]*)" without a key and body (.*)$`, func(path, body string) error { return w.send("POST", path, "", body) })
	sc.Step(`^I POST "([^"]*)" with key "([^"]*)" and body (.*)$`, func(path, key, body string) error { return w.send("POST", path, key, body) })
	sc.Step(`^(\d+) (minutes|hours) pass$`, func(n int, unit string) error {
		d := time.Minute
		if unit == "hours" {
			d = time.Hour
		}
		w.clock.Advance(time.Duration(n) * d)
		return nil
	})
}

func (w *world) openFor(cartID string) error {
	return w.post("/checkout-sessions", fmt.Sprintf(`{"cartId":%q,"shopperId":"shopper-1","shipToAddressId":"addr-1"}`, cartID))
}

func registerPolicies(sc *godog.ScenarioContext, w *world) {
	d := func() usecases.Deps { return w.deps }
	sc.Step(`^ResolveInDoubtPlacements runs$`, func() error {
		_, err := usecases.ResolveInDoubtPlacements{Deps: d()}.Execute(context.Background(), 100)
		return err
	})
	sc.Step(`^ResolveStuckAuthorizations runs$`, func() error {
		_, err := usecases.ResolveStuckAuthorizations{Deps: d()}.Execute(context.Background(), 100)
		return err
	})
	sc.Step(`^ExpireOnQuoteExpiry runs$`, func() error {
		_, err := usecases.ExpireOnQuoteExpiry{Deps: d()}.Execute(context.Background(), 100)
		return err
	})
	sc.Step(`^RetryOwedVoids runs$`, func() error {
		_, err := usecases.RetryOwedVoids{Deps: d()}.Execute(context.Background(), 100)
		return err
	})
	sc.Step(`^PurgeStoredPlacements runs$`, func() error {
		_, err := usecases.PurgeStoredPlacements{Deps: d()}.Execute(context.Background(), 100)
		return err
	})
}
