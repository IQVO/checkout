// Package repocontract is the repository contract suite: the behaviours every
// persistence adapter (the in-memory doubles and Postgres) must show. It is
// test support (it imports "testing") kept out of _test.go files so both
// adapters can run it; the shipped binary never links it (enforced by
// internal/architecture).
package repocontract

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/IQVO/checkout/internal/application/ports"
	cs "github.com/IQVO/checkout/internal/domain/checkoutsession"
)

// Env is what an adapter hands to the suite. Every test gets a fresh Env.
type Env struct {
	Sessions    ports.SessionRepository
	Outbox      ports.EventOutbox
	UoW         ports.UnitOfWork
	Idempotency ports.IdempotencyStore
	// Clock is the store's time authority: testIdempotency stamps records
	// with it, so the expired-record case expires relative to the SAME clock
	// the implementation under test judges TTL with (the memory double is
	// driven by a frozen clock; the Postgres store by the database's now()).
	Clock ports.Clock
	// OutboxCount returns how many events the outbox holds.
	OutboxCount func(t *testing.T) int
}

// Run executes the whole suite; newEnv must return an empty Env each time.
func Run(t *testing.T, newEnv func(t *testing.T) Env) {
	t.Helper()
	suites := map[string]func(*testing.T, Env){
		"sessions":        testSessions,
		"stuck":           listStuck,
		"placing-due":     listPlacingDue,
		"expirable":       listExpirable,
		"void-owed":       listVoidOwed,
		"purgeable":       listPurgeable,
		"unitofwork":      testUnitOfWork,
		"idempotency":     testIdempotency,
		"concurrency":     testConcurrency,
		"every-state":     testEveryState,
		"limit-and-order": testLimitAndOrder,
	}
	for name, fn := range suites {
		t.Run(name, func(t *testing.T) { fn(t, newEnv(t)) })
	}
}

var t0 = time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)

// at returns t0 plus m minutes.
func at(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }

var bg = context.Background

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func wantErr(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("want %v, got %v", want, got)
	}
}

// open returns an OPEN session (quote expires 15 minutes after t0).
func open(t *testing.T, id string) *cs.CheckoutSession {
	t.Helper()
	s, err := cs.Open(openInput(id), t0)
	must(t, err)
	s.PullEvents()
	return s
}

func openInput(id string) cs.OpenInput {
	return cs.OpenInput{
		ID: cs.ID(id), ShopperID: "shopper-1", ShipToAddressID: "addr-1",
		Cart: cs.CartSnapshot{CartID: "cart-" + id, Version: 4, Ready: true,
			Lines: []cs.CartLine{{CatalogItemID: "item-a", Quantity: 2}, {CatalogItemID: "item-b", Quantity: 1}},
			Groups: []cs.CartGroup{
				{GroupNo: 1, OfferID: "o1", Tier: cs.TierNextDay, LineNos: []int{1}},
				{GroupNo: 2, OfferID: "o2", Tier: cs.TierStandard, LineNos: []int{2}},
			}},
		Offers: []cs.OfferStatus{{OfferID: "o1", Quoted: true, ExpiresAt: at(120)}, {OfferID: "o2", Quoted: true, ExpiresAt: at(120)}},
		Quote: cs.PriceQuote{QuoteID: "q-" + id, Currency: "USD",
			Lines:    []cs.QuoteLine{{CatalogItemID: "item-a", LineTotalMinor: 1000}, {CatalogItemID: "item-b", LineTotalMinor: 500}},
			Charges:  []cs.QuoteCharge{{GroupNo: 1, ChargeMinor: 300}, {GroupNo: 2, ChargeMinor: 200}},
			TaxMinor: 100, TotalMinor: 2100, ExpiresAt: at(15)},
	}
}

func authorizing(t *testing.T, id string) *cs.CheckoutSession {
	t.Helper()
	s := open(t, id)
	must(t, s.Confirm("tok_1", at(1)))
	return s
}

func placing(t *testing.T, id string) *cs.CheckoutSession {
	t.Helper()
	s := authorizing(t, id)
	must(t, s.RecordAuthorization("auth-"+id, 2100, "USD", []byte(`{"shipToAddressId":"addr-1"}`), at(2)))
	s.PullEvents()
	return s
}

func save(t *testing.T, e Env, s *cs.CheckoutSession) {
	t.Helper()
	must(t, e.Sessions.Save(bg(), s))
}
