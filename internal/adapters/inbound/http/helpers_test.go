package http_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	httpadapter "github.com/IQVO/checkout/internal/adapters/inbound/http"
	"github.com/IQVO/checkout/internal/adapters/outbound/memory"
	"github.com/IQVO/checkout/internal/application/exchange"
	"github.com/IQVO/checkout/internal/application/usecases"
	cs "github.com/IQVO/checkout/internal/domain/checkoutsession"
)

var t0 = time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)

type env struct {
	t        *testing.T
	store    *memory.Store
	clock    *memory.Clock
	carts    *memory.Carts
	offers   *memory.Offers
	payments *memory.Payments
	placer   *memory.Placer
	deps     usecases.Deps
	ready    *httpadapter.Readiness
	srv      *httptest.Server
	keys     int
}

func newEnv(t *testing.T) *env {
	t.Helper()
	return newEnvLogged(t, nil)
}

// newEnvLogged is newEnv with the request logger the router is built with.
func newEnvLogged(t *testing.T, logger *slog.Logger) *env {
	t.Helper()
	clock := memory.NewClock(t0)
	s := memory.NewStore(clock)
	e := &env{
		t: t, store: s, clock: clock, carts: memory.NewCarts(), offers: memory.NewOffers(),
		payments: &memory.Payments{}, placer: &memory.Placer{},
	}
	e.deps = usecases.Deps{
		Sessions: s.Sessions(), Carts: e.carts, Offers: e.offers, Quoter: memory.NewQuoter(clock), Payments: e.payments,
		Placer: e.placer, Outbox: s.Outbox(), UoW: s.UnitOfWork(), Clock: clock, IDs: &memory.SequentialIDs{}, Alerter: &memory.Alerts{},
	}
	e.ready = httpadapter.NewReadiness(0, 0)
	api := &httpadapter.Server{
		OpenSession: usecases.OpenSession{Deps: e.deps}, ConfirmSession: usecases.ConfirmSession{Deps: e.deps},
		GetSession: usecases.GetSession{Deps: e.deps}, IdempotencyStore: s.Idempotency(), Clock: clock, Readiness: e.ready,
	}
	e.srv = httptest.NewServer(httpadapter.NewRouter(api, logger, ""))
	t.Cleanup(e.srv.Close)
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

type reply struct {
	Status int
	Header http.Header
	Body   string
}

func (r reply) json(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(r.Body), &m); err != nil {
		t.Fatalf("body is not JSON: %q (%v)", r.Body, err)
	}
	return m
}

// do sends one request; key "" sends no Idempotency-Key.
func (e *env) do(method, path, key, body string) reply {
	e.t.Helper()
	req, err := http.NewRequest(method, e.srv.URL+path, strings.NewReader(body))
	if err != nil {
		e.t.Fatal(err)
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return reply{Status: resp.StatusCode, Header: resp.Header, Body: string(b)}
}

// post sends a POST with a fresh Idempotency-Key.
func (e *env) post(path, body string) reply {
	e.t.Helper()
	e.keys++
	return e.do(http.MethodPost, path, "key-"+strconv.Itoa(e.keys), body)
}

func (e *env) get(path string) reply { e.t.Helper(); return e.do(http.MethodGet, path, "", "") }

func (e *env) wantStatus(r reply, status int) {
	e.t.Helper()
	if r.Status != status {
		e.t.Fatalf("status = %d, want %d: %s", r.Status, status, r.Body)
	}
}

func (e *env) wantProblem(r reply, status int, slug string) {
	e.t.Helper()
	e.wantStatus(r, status)
	if ct := r.Header.Get("Content-Type"); ct != "application/problem+json" {
		e.t.Fatalf("content-type = %q", ct)
	}
	if got := r.json(e.t)["type"]; got != "/problems/"+slug {
		e.t.Fatalf("problem type = %v, want /problems/%s (%s)", got, slug, r.Body)
	}
}
