package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/IQVO/checkout/internal/application/exchange"
	"github.com/IQVO/checkout/internal/application/ports"
	"github.com/IQVO/checkout/internal/domain/checkoutsession"
)

var (
	_ ports.CartReader     = (*Carts)(nil)
	_ ports.OfferReader    = (*Offers)(nil)
	_ ports.PriceQuoter    = (*Quoter)(nil)
	_ ports.PaymentGateway = (*Payments)(nil)
	_ ports.OrderPlacer    = (*Placer)(nil)
	_ ports.Alerter        = (*Alerts)(nil)
)

// Carts is a scriptable shopping-cart double.
type Carts struct {
	mu    sync.Mutex
	carts map[string]exchange.Cart
	// Err, when set, is returned by every GetCart.
	Err error
}

// NewCarts returns an empty cart double.
func NewCarts() *Carts { return &Carts{carts: map[string]exchange.Cart{}} }

// Put makes the cart readable.
func (c *Carts) Put(cart exchange.Cart) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.carts[cart.Snapshot.CartID] = cart
}

// GetCart implements ports.CartReader.
func (c *Carts) GetCart(_ context.Context, cartID string) (exchange.Cart, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Err != nil {
		return exchange.Cart{}, c.Err
	}
	cart, ok := c.carts[cartID]
	if !ok {
		return exchange.Cart{}, ports.ErrCartNotFound
	}
	return cart, nil
}

// Offers is a scriptable delivery-promise double.
type Offers struct {
	mu     sync.Mutex
	offers map[string]checkoutsession.OfferStatus
	// Err, when set, is returned by every GetOffer.
	Err error
}

// NewOffers returns an empty offer double.
func NewOffers() *Offers { return &Offers{offers: map[string]checkoutsession.OfferStatus{}} }

// Put sets the state of an offer.
func (o *Offers) Put(s checkoutsession.OfferStatus) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.offers[s.OfferID] = s
}

// GetOffer implements ports.OfferReader; an unknown offer is not quoted.
func (o *Offers) GetOffer(_ context.Context, id string) (checkoutsession.OfferStatus, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.Err != nil {
		return checkoutsession.OfferStatus{}, o.Err
	}
	if s, ok := o.offers[id]; ok {
		return s, nil
	}
	return checkoutsession.OfferStatus{OfferID: id}, nil
}

// Quoter is a pricing double: 1000 minor units per item, 500 per group, 10 %
// tax, a quote that lives TTL after the clock reading.
type Quoter struct {
	mu    sync.Mutex
	clock ports.Clock
	// TTL is how long a quote lives (default 15 minutes).
	TTL time.Duration
	// Err, when set, is returned by every IssueQuote.
	Err error
	// Keys are the idempotency keys received, in order.
	Keys []string
}

// NewQuoter returns a quoter reading the clock.
func NewQuoter(clock ports.Clock) *Quoter { return &Quoter{clock: clock, TTL: 15 * time.Minute} }

// IssueQuote implements ports.PriceQuoter.
func (q *Quoter) IssueQuote(_ context.Context, key string, req exchange.QuoteRequest) (checkoutsession.PriceQuote, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.Keys = append(q.Keys, key)
	if q.Err != nil {
		return checkoutsession.PriceQuote{}, q.Err
	}
	out := checkoutsession.PriceQuote{QuoteID: "quote-" + key, Currency: "USD", ExpiresAt: q.clock.Now().Add(q.TTL)}
	var items int64
	for _, l := range req.Lines {
		total := int64(l.Quantity) * 1000
		items += total
		out.Lines = append(out.Lines, checkoutsession.QuoteLine{CatalogItemID: l.CatalogItemID, LineTotalMinor: total})
	}
	for _, g := range req.Groups {
		items += 500
		out.Charges = append(out.Charges, checkoutsession.QuoteCharge{GroupNo: g.GroupNo, ChargeMinor: 500})
	}
	out.TaxMinor = items / 10
	out.TotalMinor = items + out.TaxMinor
	return out, nil
}

// Alerts records the alerts raised.
type Alerts struct {
	mu    sync.Mutex
	codes []string
}

// Alert implements ports.Alerter.
func (a *Alerts) Alert(_ context.Context, code, _, _ string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.codes = append(a.codes, code)
}

// Codes returns the alert codes raised, in order.
func (a *Alerts) Codes() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.codes...)
}

func draftJSON(d exchange.PlacementDraft) ([]byte, error) {
	b, err := json.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("marshal draft: %w", err)
	}
	return b, nil
}
