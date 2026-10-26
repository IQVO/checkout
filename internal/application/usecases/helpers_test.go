package usecases_test

import (
	"context"
	"testing"
	"time"

	"github.com/IQVO/checkout/internal/adapters/outbound/memory"
	"github.com/IQVO/checkout/internal/application/exchange"
	"github.com/IQVO/checkout/internal/application/usecases"
	"github.com/IQVO/checkout/internal/application/views"
	cs "github.com/IQVO/checkout/internal/domain/checkoutsession"
)

var t0 = time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)

type env struct {
	clock    *memory.Clock
	store    *memory.Store
	carts    *memory.Carts
	offers   *memory.Offers
	quoter   *memory.Quoter
	payments *memory.Payments
	placer   *memory.Placer
	alerts   *memory.Alerts
	deps     usecases.Deps
}

func newEnv() *env {
	clock := memory.NewClock(t0)
	store := memory.NewStore(clock)
	e := &env{
		clock: clock, store: store, carts: memory.NewCarts(), offers: memory.NewOffers(), quoter: memory.NewQuoter(clock),
		payments: &memory.Payments{}, placer: &memory.Placer{}, alerts: &memory.Alerts{},
	}
	e.deps = usecases.Deps{
		Sessions: store.Sessions(), Carts: e.carts, Offers: e.offers, Quoter: e.quoter, Payments: e.payments,
		Placer: e.placer, Outbox: store.Outbox(), UoW: store.UnitOfWork(), Clock: clock, IDs: &memory.SequentialIDs{}, Alerter: e.alerts,
	}
	e.carts.Put(readyCart())
	for _, id := range []string{"offer-1", "offer-2"} {
		e.offers.Put(cs.OfferStatus{OfferID: id, Quoted: true, ExpiresAt: t0.Add(time.Hour)})
	}
	return e
}

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

func (e *env) open(t *testing.T) views.Session {
	t.Helper()
	v, err := usecases.OpenSession{Deps: e.deps}.Execute(context.Background(), usecases.OpenCommand{
		RequestKey: "key-1", CartID: "cart-1", ShopperID: "shopper-1", ShipToAddressID: "addr-1",
	})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	return v
}

func (e *env) confirm(id, token string) (views.Session, error) {
	return usecases.ConfirmSession{Deps: e.deps}.Execute(context.Background(), id, token)
}

func (e *env) get(t *testing.T, id string) views.Session {
	t.Helper()
	v, err := usecases.GetSession{Deps: e.deps}.Execute(context.Background(), id)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	return v
}

func (e *env) eventNames() []string {
	var out []string
	for _, ev := range e.store.Outbox().Events() {
		out = append(out, ev.EventName())
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
