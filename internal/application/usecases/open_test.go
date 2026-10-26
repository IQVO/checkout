package usecases_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/IQVO/checkout/internal/application/ports"
	"github.com/IQVO/checkout/internal/application/usecases"
	cs "github.com/IQVO/checkout/internal/domain/checkoutsession"
)

func TestOpenSessionStoresBindingQuote(t *testing.T) {
	e := newEnv()
	v := e.open(t)
	if v.State != cs.StateOpen || v.SessionID != "sess-1" || v.CartVersion != 3 || v.Currency != "USD" || v.TotalMinor != 4400 {
		t.Fatalf("view %+v", v)
	}
	if e.quoter.Keys[0] != "key-1:price-quote" {
		t.Fatalf("quote key %v", e.quoter.Keys)
	}
	if !equalStrings(e.eventNames(), []string{"CheckoutOpened"}) {
		t.Fatalf("events %v", e.eventNames())
	}
	if e.get(t, "sess-1").TotalMinor != 4400 {
		t.Fatal("GetSession must read the stored session")
	}
}

func TestOpenSessionCartNotReadyCallsNobodyElse(t *testing.T) {
	e := newEnv()
	c := readyCart()
	c.Snapshot.Ready = false
	e.carts.Put(c)
	_, err := usecases.OpenSession{Deps: e.deps}.Execute(context.Background(), usecases.OpenCommand{RequestKey: "k", CartID: "cart-1", ShopperID: "s", ShipToAddressID: "a"})
	if !errors.Is(err, cs.ErrCartNotCheckoutReady) || len(e.quoter.Keys) != 0 || e.store.Counts().Sessions != 0 {
		t.Fatalf("err %v keys %v", err, e.quoter.Keys)
	}
}

func TestOpenSessionOfferNotQuotableChangesNothing(t *testing.T) {
	e := newEnv()
	e.offers.Put(cs.OfferStatus{OfferID: "offer-2", Quoted: true, ExpiresAt: t0.Add(time.Minute)})
	_, err := usecases.OpenSession{Deps: e.deps}.Execute(context.Background(), usecases.OpenCommand{RequestKey: "k", CartID: "cart-1", ShopperID: "s", ShipToAddressID: "a"})
	if !errors.Is(err, cs.ErrOfferNotQuotable) || e.store.Counts().Sessions != 0 || e.store.Counts().OutboxEvents != 0 {
		t.Fatalf("err %v counts %+v", err, e.store.Counts())
	}
}

func TestOpenSessionDependencyFailures(t *testing.T) {
	cases := map[string]struct {
		arm  func(*env)
		want error
	}{
		"cart":    {func(e *env) { e.carts.Err = ports.ErrShoppingCartUnavailable }, ports.ErrShoppingCartUnavailable},
		"offer":   {func(e *env) { e.offers.Err = ports.ErrDeliveryPromiseUnavailable }, ports.ErrDeliveryPromiseUnavailable},
		"pricing": {func(e *env) { e.quoter.Err = ports.ErrPricingUnavailable }, ports.ErrPricingUnavailable},
	}
	for name, c := range cases {
		e := newEnv()
		c.arm(e)
		_, err := usecases.OpenSession{Deps: e.deps}.Execute(context.Background(), usecases.OpenCommand{RequestKey: "k", CartID: "cart-1", ShopperID: "s", ShipToAddressID: "a"})
		if !errors.Is(err, c.want) || e.store.Counts().Sessions != 0 {
			t.Fatalf("%s: err %v", name, err)
		}
	}
}

func TestOpenSessionUnknownCartAndContractBug(t *testing.T) {
	e := newEnv()
	_, err := usecases.OpenSession{Deps: e.deps}.Execute(context.Background(), usecases.OpenCommand{RequestKey: "k", CartID: "nope", ShopperID: "s", ShipToAddressID: "a"})
	if !errors.Is(err, ports.ErrCartNotFound) {
		t.Fatalf("err %v", err)
	}
	c := readyCart()
	c.Snapshot.Groups = c.Snapshot.Groups[:1]
	e.carts.Put(c)
	_, err = usecases.OpenSession{Deps: e.deps}.Execute(context.Background(), usecases.OpenCommand{RequestKey: "k", CartID: "cart-1", ShopperID: "s", ShipToAddressID: "a"})
	if !errors.Is(err, cs.ErrContractViolation) || !equalStrings(e.alerts.Codes(), []string{"sibling-contract-violation"}) {
		t.Fatalf("err %v alerts %v", err, e.alerts.Codes())
	}
}
