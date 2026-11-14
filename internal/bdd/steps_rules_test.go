package bdd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cucumber/godog"

	"github.com/IQVO/checkout/internal/adapters/outbound/memory"
	"github.com/IQVO/checkout/internal/application/exchange"
	"github.com/IQVO/checkout/internal/application/ports"
	"github.com/IQVO/checkout/internal/application/usecases"
	cs "github.com/IQVO/checkout/internal/domain/checkoutsession"
	"github.com/IQVO/checkout/internal/domain/shared"
)

// steps_rules_test.go: the vocabulary of the documentation-derived features
// (session_*_rules.feature): what customer-payments was asked, quote faults
// of pricing, cart faults, event payloads, clock steps in seconds, the
// policies with a handled-count and the "does nothing" assertions.

// spyPayments records the AuthorizePayment requests (allocations included) and
// delegates to the scriptable double.
type spyPayments struct {
	inner    *memory.Payments
	mu       sync.Mutex
	requests []exchange.AuthorizeRequest
}

func (s *spyPayments) Authorize(ctx context.Context, key string, req exchange.AuthorizeRequest) (exchange.Authorization, error) {
	s.mu.Lock()
	s.requests = append(s.requests, req)
	s.mu.Unlock()
	return s.inner.Authorize(ctx, key, req)
}

func (s *spyPayments) Void(ctx context.Context, key, authorizationID string) error {
	return s.inner.Void(ctx, key, authorizationID)
}

func (s *spyPayments) last() (exchange.AuthorizeRequest, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.requests) == 0 {
		return exchange.AuthorizeRequest{}, false
	}
	return s.requests[len(s.requests)-1], true
}

// tweakQuoter lets a scenario make pricing answer something that does not add
// up, after the real double computed the honest quote.
type tweakQuoter struct {
	inner *memory.Quoter
	mu    sync.Mutex
	tweak func(*cs.PriceQuote)
}

func (t *tweakQuoter) set(f func(*cs.PriceQuote)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.tweak = f
}

func (t *tweakQuoter) IssueQuote(ctx context.Context, key string, req exchange.QuoteRequest) (cs.PriceQuote, error) {
	q, err := t.inner.IssueQuote(ctx, key, req)
	t.mu.Lock()
	f := t.tweak
	t.mu.Unlock()
	if err == nil && f != nil {
		f(&q)
	}
	return q, err
}

var (
	_ ports.PaymentGateway = (*spyPayments)(nil)
	_ ports.PriceQuoter    = (*tweakQuoter)(nil)
)

const quoteKeySuffix = ":price-quote"

func registerRuleSteps(sc *godog.ScenarioContext, w *world) {
	registerRuleGivens(sc, w)
	registerRuleWhens(sc, w)
	registerRuleEventSteps(sc, w)
	registerRulePaymentSteps(sc, w)
	registerRuleResponseSteps(sc, w)
}

func registerRuleGivens(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^a checkout-ready cart with one group per line for "([^"]*)"$`, w.oneGroupPerLine)
	sc.Step(`^a cart whose second group claims line 1 again$`, func() error { return w.groupLines(1) })
	sc.Step(`^a cart whose second group names line 3$`, func() error { return w.groupLines(3) })
	sc.Step(`^every offer stays quotable for 2 days$`, func() error {
		for _, id := range []string{"offer-1", "offer-2"} {
			w.offers.Put(cs.OfferStatus{OfferID: id, Quoted: true, ExpiresAt: t0.Add(48 * time.Hour)})
		}
		return nil
	})
	sc.Step(`^shopping-cart is unreachable$`, func() error { w.carts.Err = ports.ErrShoppingCartUnavailable; return nil })
	sc.Step(`^delivery-promise is unreachable$`, func() error { w.offers.Err = ports.ErrDeliveryPromiseUnavailable; return nil })
	sc.Step(`^pricing refuses the quote request$`, func() error { w.quoter.Err = ports.ErrQuoteRejected; return nil })
	sc.Step(`^pricing recovers$`, func() error { w.quoter.Err = nil; return nil })
	sc.Step(`^pricing adds one minor unit of tax$`, func() error {
		w.tweak.set(func(q *cs.PriceQuote) { q.TaxMinor++; q.TotalMinor++ })
		return nil
	})
	sc.Step(`^pricing answers a total of (\d+) minor units$`, func(total int) error {
		w.tweak.set(func(q *cs.PriceQuote) { q.TotalMinor = int64(total) })
		return nil
	})
	sc.Step(`^pricing leaves out the price of the last line$`, func() error {
		w.tweak.set(func(q *cs.PriceQuote) { q.Lines = q.Lines[:len(q.Lines)-1] })
		return nil
	})
	sc.Step(`^customer-payments authorizes one minor unit too little$`, func() error {
		w.payments.Script = append(w.payments.Script, memory.AuthStep{Mutate: func(a *exchange.Authorization) { a.TotalMinor-- }})
		return nil
	})
	sc.Step(`^customer-payments authorizes in "([^"]*)"$`, func(currency string) error {
		w.payments.Script = append(w.payments.Script, memory.AuthStep{Mutate: func(a *exchange.Authorization) { a.Currency = currency }})
		return nil
	})
}

// oneGroupPerLine stocks cart-1 with one line and one shipment group per entry
// of "SKU xQTY, SKU xQTY", the tiers cycling NEXT_DAY, STANDARD, SAME_DAY, TWO_DAY.
func (w *world) oneGroupPerLine(spec string) error {
	tiers := []cs.Tier{cs.TierNextDay, cs.TierStandard, cs.TierSameDay, cs.TierTwoDay}
	c := readyCart()
	c.Snapshot.Lines, c.Snapshot.Groups = nil, nil
	for i, part := range strings.Split(spec, ", ") {
		sku, qty, ok := strings.Cut(part, " x")
		n, err := strconv.Atoi(qty)
		if !ok || err != nil {
			return fmt.Errorf("line %q is not \"SKU xQTY\"", part)
		}
		c.Snapshot.Lines = append(c.Snapshot.Lines, cs.CartLine{CatalogItemID: sku, Quantity: n})
		c.Snapshot.Groups = append(c.Snapshot.Groups, cs.CartGroup{
			GroupNo: i + 1, OfferID: fmt.Sprintf("offer-%d", i+1), Tier: tiers[i%len(tiers)], LineNos: []int{i + 1},
		})
	}
	w.stockCart(c)
	return nil
}

// groupLines makes the second group of the ready cart name the given line.
func (w *world) groupLines(line int) error {
	c := readyCart()
	c.Snapshot.Groups[1].LineNos = []int{line}
	w.stockCart(c)
	return nil
}

func registerRuleWhens(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^(\d+) seconds? pass(?:es)?$`, func(n int) error { w.clock.Advance(time.Duration(n) * time.Second); return nil })
	sc.Step(`^(\d+) hours? and (\d+) minutes? pass(?:es)?$`, func(h, m int) error {
		w.clock.Advance(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute)
		return nil
	})
	sc.Step(`^I POST "([^"]*)" with a key of (\d+) characters and body (.*)$`, func(path string, n int, body string) error {
		w.lastKey = strings.Repeat("k", n)
		return w.send("POST", path, w.lastKey, body)
	})
	sc.Step(`^I open a checkout session with a body of (\d+) bytes$`, func(n int) error {
		const head, tail = `{"cartId":"`, `","shopperId":"shopper-1","shipToAddressId":"addr-1"}`
		pad := n - len(head) - len(tail)
		if pad < 1 {
			return fmt.Errorf("a body of %d bytes cannot hold the fixed fields", n)
		}
		return w.post("/checkout-sessions", head+strings.Repeat("c", pad)+tail)
	})
	sc.Step(`^I confirm the session with a token of (\d+) characters$`, func(n int) error {
		return w.post("/checkout-sessions/sess-1/confirmations", fmt.Sprintf(`{"paymentMethodToken":%q}`, strings.Repeat("t", n)))
	})
	sc.Step(`^(ExpireOnQuoteExpiry|ResolveInDoubtPlacements|ResolveStuckAuthorizations|RetryOwedVoids|PurgeStoredPlacements) handles (\d+) sessions?$`,
		func(policy string, want int) error {
			got, err := w.runPolicy(policy)
			if err != nil {
				return err
			}
			if got != want {
				return fmt.Errorf("%s handled %d sessions, want %d", policy, got, want)
			}
			return nil
		})
}

func (w *world) runPolicy(name string) (int, error) {
	ctx, d := context.Background(), w.deps
	switch name {
	case "ExpireOnQuoteExpiry":
		return usecases.ExpireOnQuoteExpiry{Deps: d}.Execute(ctx, 100)
	case "ResolveInDoubtPlacements":
		return usecases.ResolveInDoubtPlacements{Deps: d}.Execute(ctx, 100)
	case "ResolveStuckAuthorizations":
		return usecases.ResolveStuckAuthorizations{Deps: d}.Execute(ctx, 100)
	case "RetryOwedVoids":
		return usecases.RetryOwedVoids{Deps: d}.Execute(ctx, 100)
	case "PurgeStoredPlacements":
		return usecases.PurgeStoredPlacements{Deps: d}.Execute(ctx, 100)
	}
	return 0, fmt.Errorf("unknown policy %s", name)
}

// lastEvent returns the newest outbox event of the given name.
func (w *world) lastEvent(name string) (shared.Event, error) {
	evs := w.store.Outbox().Events()
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].EventName() == name {
			return evs[i], nil
		}
	}
	return nil, fmt.Errorf("no %s event was published", name)
}

func registerRuleEventSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the "([A-Za-z]+)" event was published (\d+) times?$`, w.eventsPublished)
	sc.Step(`^the CheckoutOpened event names cart "([^"]*)", shopper "([^"]*)" and total (\d+)$`, w.openedEventIs)
	sc.Step(`^the CheckoutDeclined event carries the reason "([^"]*)"$`, func(reason string) error {
		ev, err := w.lastEvent("CheckoutDeclined")
		return orErr(err, func() error { return wantString("decline reason", ev.(cs.CheckoutDeclined).Reason, reason) })
	})
	sc.Step(`^the CheckoutFailed event carries the code "([^"]*)"$`, func(code string) error {
		ev, err := w.lastEvent("CheckoutFailed")
		return orErr(err, func() error { return wantString("failure code", ev.(cs.CheckoutFailed).Code, code) })
	})
	sc.Step(`^the CheckoutPlacementInDoubt event carries placingSince "([^"]*)"$`, func(since string) error {
		ev, err := w.lastEvent("CheckoutPlacementInDoubt")
		return orErr(err, func() error {
			return wantString("placingSince", ev.(cs.CheckoutPlacementInDoubt).PlacingSince.UTC().Format(time.RFC3339), since)
		})
	})
	sc.Step(`^the CheckoutRequoteRequired event carries offer "([^"]*)" for group (\d+)$`, func(offer string, group int) error {
		ev, err := w.lastEvent("CheckoutRequoteRequired")
		return orErr(err, func() error {
			offers := ev.(cs.CheckoutRequoteRequired).RequotedOffers
			if len(offers) != 1 || offers[0].OfferID != offer || offers[0].GroupNo != group {
				return fmt.Errorf("requoted offers = %+v", offers)
			}
			return nil
		})
	})
	sc.Step(`^the CheckoutCompleted event names order "([^"]*)", shopper "([^"]*)", cart "([^"]*)" and authorization "([^"]*)" for (\d+) (\w+) allocated "([^"]*)"$`,
		w.completedEventIs)
	sc.Step(`^alert "([^"]*)" was not raised$`, func(code string) error { return w.alertRaised(code, 0) })
	sc.Step(`^alert "([^"]*)" was raised (\d+) times?$`, w.alertRaised)
}

func orErr(err error, then func() error) error {
	if err != nil {
		return err
	}
	return then()
}

func wantString(what, got, want string) error {
	if got != want {
		return fmt.Errorf("%s = %q, want %q", what, got, want)
	}
	return nil
}

func (w *world) eventsPublished(name string, want int) error {
	got := 0
	for _, ev := range w.store.Outbox().Events() {
		if ev.EventName() == name {
			got++
		}
	}
	if got != want {
		return fmt.Errorf("%s was published %d times, want %d", name, got, want)
	}
	return nil
}

func (w *world) openedEventIs(cart, shopper string, total int) error {
	ev, err := w.lastEvent("CheckoutOpened")
	if err != nil {
		return err
	}
	if e := ev.(cs.CheckoutOpened); e.CartID != cart || e.ShopperID != shopper || e.TotalMinor != int64(total) {
		return fmt.Errorf("CheckoutOpened = %+v", e)
	}
	return nil
}

func (w *world) completedEventIs(order, shopper, cart, auth string, total int, currency, alloc string) error {
	ev, err := w.lastEvent("CheckoutCompleted")
	if err != nil {
		return err
	}
	e := ev.(cs.CheckoutCompleted)
	if e.OrderID != order || e.ShopperID != shopper || e.CartID != cart || e.AuthorizationID != auth ||
		e.TotalMinor != int64(total) || e.Currency != currency {
		return fmt.Errorf("CheckoutCompleted = %+v", e)
	}
	parts := make([]string, len(e.Groups))
	for i, g := range e.Groups {
		parts[i] = fmt.Sprintf("%d:%d", g.GroupNo, g.AmountMinor)
	}
	return wantString("allocations", strings.Join(parts, ", "), alloc)
}

func (w *world) alertRaised(code string, want int) error {
	n := 0
	for _, c := range w.alerts.Codes() {
		if c == code {
			n++
		}
	}
	if n != want {
		return fmt.Errorf("alert %s raised %d times, want %d (%v)", code, n, want, w.alerts.Codes())
	}
	return nil
}

func registerRulePaymentSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^customer-payments received (\d+) authorization calls?$`, func(want int) error {
		if got := len(w.payments.AuthKeys); got != want {
			return fmt.Errorf("authorization calls = %d (%v), want %d", got, w.payments.AuthKeys, want)
		}
		return nil
	})
	sc.Step(`^customer-payments received the tokens "([^"]*)"$`, func(list string) error {
		return wantString("tokens", strings.Join(w.payments.Tokens, ", "), list)
	})
	sc.Step(`^the authorization keys were all different$`, w.authorizationKeysDiffer)
	sc.Step(`^customer-payments was asked to authorize (\d+) (\w+) allocated "([^"]*)"$`, w.authorizeRequestIs)
	sc.Step(`^(\d+) voids? (?:was|were) attempted$`, func(want int) error {
		if got := len(w.payments.VoidKeys); got != want {
			return fmt.Errorf("void attempts = %d (%v), want %d", got, w.payments.VoidKeys, want)
		}
		return nil
	})
	sc.Step(`^pricing received the keys "([^"]*)"$`, func(list string) error {
		return wantString("pricing keys", strings.Join(w.quoter.Keys, ", "), list)
	})
	sc.Step(`^the pricing key is the request key followed by ":price-quote"$`, func() error {
		return w.pricingKeyIs(w.lastKey + quoteKeySuffix)
	})
	sc.Step(`^the pricing key is the SHA-256 of the request key followed by ":price-quote"$`, func() error {
		sum := sha256.Sum256([]byte(w.lastKey))
		return w.pricingKeyIs(hex.EncodeToString(sum[:]) + quoteKeySuffix)
	})
	sc.Step(`^all (\d+) placement calls carried key "([^"]*)" and the same body$`, w.placementCallsAre)
	sc.Step(`^the session keeps the PSP token "([^"]*)"$`, func(want string) error { return w.pendingTokenIs(want) })
	sc.Step(`^the session keeps no PSP token$`, func() error { return w.pendingTokenIs("") })
	sc.Step(`^the stored placement of the session is kept$`, func() error {
		s, err := w.deps.Sessions.Get(context.Background(), "sess-1")
		return orErr(err, func() error {
			if s.Placement() == nil {
				return fmt.Errorf("the stored placement was purged")
			}
			return nil
		})
	})
}

func (w *world) authorizationKeysDiffer() error {
	seen := map[string]bool{}
	for _, k := range w.payments.AuthKeys {
		if seen[k] {
			return fmt.Errorf("authorization key %s was used twice: %v", k, w.payments.AuthKeys)
		}
		seen[k] = true
	}
	return nil
}

func (w *world) authorizeRequestIs(total int, currency, alloc string) error {
	req, ok := w.spy.last()
	if !ok {
		return fmt.Errorf("customer-payments was never asked to authorize")
	}
	parts := make([]string, len(req.Allocations))
	var sum int64
	for i, a := range req.Allocations {
		parts[i] = fmt.Sprintf("%d:%d", a.GroupNo, a.AmountMinor)
		sum += a.AmountMinor
	}
	if req.TotalMinor != int64(total) || req.Currency != currency || strings.Join(parts, ", ") != alloc || sum != req.TotalMinor {
		return fmt.Errorf("authorize request = %+v, want %d %s allocated %q", req, total, currency, alloc)
	}
	return nil
}

func (w *world) placementCallsAre(n int, key string) error {
	calls := w.placer.Calls
	if len(calls) != n {
		return fmt.Errorf("placement calls = %d, want %d", len(calls), n)
	}
	for i, c := range calls {
		if c.SessionID != key || string(c.Body) != string(calls[0].Body) {
			return fmt.Errorf("placement call %d: key %q, same body as the first %v", i+1, c.SessionID, string(c.Body) == string(calls[0].Body))
		}
	}
	return nil
}

func (w *world) pendingTokenIs(want string) error {
	s, err := w.deps.Sessions.Get(context.Background(), "sess-1")
	if err != nil {
		return err
	}
	if got := s.PendingToken(); got != want {
		return fmt.Errorf("the session keeps the token %q, want %q", got, want)
	}
	return nil
}

func (w *world) pricingKeyIs(want string) error {
	if len(w.quoter.Keys) != 1 || w.quoter.Keys[0] != want {
		return fmt.Errorf("pricing keys = %v, want [%s]", w.quoter.Keys, want)
	}
	if len(want) > 128 {
		return fmt.Errorf("the pricing key is %d characters, over the 128 bound of the siblings", len(want))
	}
	return nil
}

func registerRuleResponseSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the response has no field "([^"]*)"$`, func(name string) error {
		var m map[string]any
		if err := json.Unmarshal([]byte(w.body), &m); err != nil {
			return err
		}
		if _, present := m[name]; present {
			return fmt.Errorf("field %s is present: %s", name, w.body)
		}
		return nil
	})
	sc.Step(`^the response body does not contain "([^"]*)"$`, func(s string) error {
		if strings.Contains(w.body, s) {
			return fmt.Errorf("the body contains %q: %s", s, w.body)
		}
		return nil
	})
	sc.Step(`^the answer is not a replay$`, func() error {
		if w.header.Get("Idempotency-Replayed") != "" {
			return fmt.Errorf("the answer is a replay: %v", w.header)
		}
		return nil
	})
	sc.Step(`^the group amounts add up to the total$`, func() error {
		var s struct {
			TotalMinor int64 `json:"totalMinor"`
			Groups     []struct {
				AmountMinor int64 `json:"amountMinor"`
			} `json:"groups"`
		}
		if err := json.Unmarshal([]byte(w.body), &s); err != nil {
			return err
		}
		var sum int64
		for _, g := range s.Groups {
			sum += g.AmountMinor
		}
		if sum != s.TotalMinor {
			return fmt.Errorf("the groups add up to %d, the total is %d", sum, s.TotalMinor)
		}
		return nil
	})
}
