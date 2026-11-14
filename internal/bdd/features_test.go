// Package bdd runs the Gherkin scenarios of ../../features against the real
// HTTP router and use cases wired to the in-memory doubles.
package bdd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cucumber/godog"

	httpadapter "github.com/IQVO/checkout/internal/adapters/inbound/http"
	"github.com/IQVO/checkout/internal/adapters/outbound/memory"
	"github.com/IQVO/checkout/internal/application/usecases"
)

var t0 = time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)

// TestFeatures runs the Gherkin scenarios of ../../features.
func TestFeatures(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: initializeScenario,
		Options:             &godog.Options{Format: "pretty", Paths: []string{"../../features"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}

// world is the state of one scenario.
type world struct {
	store     *memory.Store
	clock     *memory.Clock
	carts     *memory.Carts
	offers    *memory.Offers
	quoter    *memory.Quoter
	payments  *memory.Payments
	placer    *memory.Placer
	alerts    *memory.Alerts
	deps      usecases.Deps
	srv       *httptest.Server
	readiness *httpadapter.Readiness
	// spy and tweak sit between the use cases and payments / pricing: they
	// record what customer-payments was asked and let a scenario skew a quote.
	spy   *spyPayments
	tweak *tweakQuoter

	keys    int
	lastKey string

	status int
	body   string
	header http.Header
}

func newWorld() *world {
	clock := memory.NewClock(t0)
	s := memory.NewStore(clock)
	w := &world{
		store: s, clock: clock, carts: memory.NewCarts(), offers: memory.NewOffers(), quoter: memory.NewQuoter(clock),
		payments: &memory.Payments{}, placer: &memory.Placer{}, alerts: &memory.Alerts{},
	}
	w.spy = &spyPayments{inner: w.payments}
	w.tweak = &tweakQuoter{inner: w.quoter}
	w.deps = usecases.Deps{
		Sessions: s.Sessions(), Carts: w.carts, Offers: w.offers, Quoter: w.tweak, Payments: w.spy, Placer: w.placer,
		Outbox: s.Outbox(), UoW: s.UnitOfWork(), Clock: clock, IDs: &memory.SequentialIDs{}, Alerter: w.alerts,
	}
	return w
}

// api serves the real router of apis/openapi.yaml over the scenario's world.
func (w *world) api() *httptest.Server {
	if w.srv == nil {
		d := w.deps
		w.readiness = httpadapter.NewReadiness(0, 0)
		w.srv = httptest.NewServer(httpadapter.NewRouter(&httpadapter.Server{
			OpenSession: usecases.OpenSession{Deps: d}, ConfirmSession: usecases.ConfirmSession{Deps: d}, GetSession: usecases.GetSession{Deps: d},
			IdempotencyStore: w.store.Idempotency(), Clock: d.Clock, Readiness: w.readiness,
		}, nil, ""))
	}
	return w.srv
}

func initializeScenario(sc *godog.ScenarioContext) {
	w := newWorld()
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		if w.srv != nil {
			w.srv.Close()
		}
		*w = *newWorld()
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		if w.srv != nil {
			w.srv.Close()
			w.srv = nil
		}
		return ctx, err
	})
	registerGivens(sc, w)
	registerWhens(sc, w)
	registerPolicies(sc, w)
	registerAssertions(sc, w)
	registerWorldAssertions(sc, w)
	registerWave2(sc, w)
	registerRuleSteps(sc, w)
}
