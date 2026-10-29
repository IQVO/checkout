package http_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/IQVO/checkout/internal/adapters/outbound/memory"
	"github.com/IQVO/checkout/internal/application/exchange"
	"github.com/IQVO/checkout/internal/application/ports"
	cs "github.com/IQVO/checkout/internal/domain/checkoutsession"
)

const openBody = `{"cartId":"cart-1","shopperId":"shopper-1","shipToAddressId":"addr-1"}`
const confirmBody = `{"paymentMethodToken":"tok_1"}`

func (e *env) open() map[string]any {
	e.t.Helper()
	r := e.post("/checkout-sessions", openBody)
	e.wantStatus(r, http.StatusCreated)
	return r.json(e.t)
}

func TestOpenReturnsTheBindingSession(t *testing.T) {
	e := newEnv(t)
	m := e.open()
	if m["sessionId"] != "sess-1" || m["state"] != "OPEN" || m["totalMinor"] != float64(4400) || m["currency"] != "USD" {
		t.Fatalf("body %v", m)
	}
	if m["authorizationId"] != nil || m["orderId"] != nil || m["failureCode"] != nil {
		t.Fatalf("nullable fields must be null: %v", m)
	}
	if _, has := m["requotedOffers"]; has {
		t.Fatal("requotedOffers only in REQUOTE_REQUIRED")
	}
	groups := m["groups"].([]any)
	if len(groups) != 2 || groups[0].(map[string]any)["amountMinor"] != float64(2750) {
		t.Fatalf("groups %v", groups)
	}
	got := e.get("/checkout-sessions/sess-1")
	e.wantStatus(got, http.StatusOK)
	e.wantProblem(e.get("/checkout-sessions/nope"), http.StatusNotFound, "not-found")
}

func TestOpenValidationAndConflicts(t *testing.T) {
	e := newEnv(t)
	e.wantProblem(e.post("/checkout-sessions", `{"cartId":"cart-1"}`), http.StatusUnprocessableEntity, "validation-failed")
	e.wantProblem(e.post("/checkout-sessions", `{"cartId":"cart-1","bogus":1}`), http.StatusBadRequest, "malformed-request")
	e.wantProblem(e.post("/checkout-sessions", `{"cartId":"missing","shopperId":"s","shipToAddressId":"a"}`), http.StatusUnprocessableEntity, "validation-failed")
	c := readyCart()
	c.Snapshot.Ready = false
	e.carts.Put(c)
	e.wantProblem(e.post("/checkout-sessions", openBody), http.StatusConflict, "cart-not-checkout-ready")
}

func TestOpenOfferNotQuotableAndDependencies(t *testing.T) {
	e := newEnv(t)
	e.offers.Put(cs.OfferStatus{OfferID: "offer-1", Quoted: false})
	e.wantProblem(e.post("/checkout-sessions", openBody), http.StatusUnprocessableEntity, "offer-not-quotable")
	e.offers.Put(cs.OfferStatus{OfferID: "offer-1", Quoted: true, ExpiresAt: t0.Add(time.Hour)})
	e.carts.Err = ports.ErrShoppingCartUnavailable
	e.wantProblem(e.post("/checkout-sessions", openBody), http.StatusServiceUnavailable, "shopping-cart-unavailable")
	e.carts.Err = nil
	e.offers.Err = ports.ErrDeliveryPromiseUnavailable
	e.wantProblem(e.post("/checkout-sessions", openBody), http.StatusServiceUnavailable, "delivery-promise-unavailable")
}

func TestConfirmCompletesWith200(t *testing.T) {
	e := newEnv(t)
	e.open()
	r := e.post("/checkout-sessions/sess-1:confirm", confirmBody)
	e.wantStatus(r, http.StatusOK)
	m := r.json(t)
	if m["state"] != "COMPLETED" || m["orderId"] != "order-sess-1" || m["authorizationId"] == nil {
		t.Fatalf("body %v", m)
	}
}

func TestConfirmAnswers202WhileTheOutcomeIsUnknown(t *testing.T) {
	e := newEnv(t)
	e.open()
	e.placer.Script = []memory.PlaceStep{{Err: ports.ErrOrderingUnavailable}}
	r := e.post("/checkout-sessions/sess-1:confirm", confirmBody)
	e.wantStatus(r, http.StatusAccepted)
	if r.json(t)["state"] != "PLACING" {
		t.Fatalf("body %s", r.Body)
	}
	e.wantProblem(e.post("/checkout-sessions/sess-1:confirm", `{"paymentMethodToken":"tok_2"}`), http.StatusConflict, "placement-in-doubt")
	e.wantStatus(e.get("/checkout-sessions/sess-1"), http.StatusOK)
}

func TestConfirmDeclineAndRequoteBodies(t *testing.T) {
	e := newEnv(t)
	e.open()
	e.payments.Script = []memory.AuthStep{{Result: exchange.Authorization{DeclineReason: "no"}}}
	d := e.post("/checkout-sessions/sess-1:confirm", confirmBody)
	e.wantStatus(d, http.StatusOK)
	if d.json(t)["state"] != "DECLINED" {
		t.Fatalf("body %s", d.Body)
	}
	e.placer.Script = []memory.PlaceStep{{Outcome: exchange.PlaceOutcome{Kind: exchange.OutcomeRequote, Offers: []cs.RequotedOffer{{
		OfferID: "offer-9", GroupNo: 1, Tier: cs.TierSameDay, DeliverByEarliest: "2026-10-26", DeliverByLatest: "2026-10-27", ExpiresAt: t0.Add(time.Hour)}}}}}
	r := e.post("/checkout-sessions/sess-1:confirm", confirmBody)
	e.wantStatus(r, http.StatusOK)
	m := r.json(t)
	offers, _ := m["requotedOffers"].([]any)
	if m["state"] != "REQUOTE_REQUIRED" || len(offers) != 1 || offers[0].(map[string]any)["offerId"] != "offer-9" {
		t.Fatalf("body %s", r.Body)
	}
}

func TestConfirmErrors(t *testing.T) {
	e := newEnv(t)
	e.wantProblem(e.post("/checkout-sessions/nope:confirm", confirmBody), http.StatusNotFound, "not-found")
	e.open()
	e.wantProblem(e.post("/checkout-sessions/sess-1:confirm", `{}`), http.StatusUnprocessableEntity, "validation-failed")
	e.clock.Advance(16 * time.Minute)
	e.wantProblem(e.post("/checkout-sessions/sess-1:confirm", confirmBody), http.StatusUnprocessableEntity, "price-quote-expired")
}

func TestConfirmClosedAndMismatch(t *testing.T) {
	e := newEnv(t)
	e.open()
	e.wantStatus(e.post("/checkout-sessions/sess-1:confirm", confirmBody), http.StatusOK)
	e.wantProblem(e.post("/checkout-sessions/sess-1:confirm", confirmBody), http.StatusConflict, "session-closed")
	e2 := newEnv(t)
	e2.open()
	e2.payments.Script = []memory.AuthStep{{Mutate: func(a *exchange.Authorization) { a.TotalMinor++ }}}
	e2.wantProblem(e2.post("/checkout-sessions/sess-1:confirm", confirmBody), http.StatusConflict, "authorization-mismatch")
}
