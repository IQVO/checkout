package checkoutsession

import (
	"time"

	"github.com/IQVO/checkout/internal/domain/shared"
)

// CartLine is a line of the cart snapshot.
type CartLine struct {
	CatalogItemID string
	Quantity      int
}

// CartGroup is a shipment group of the cart's delivery quote with the offer
// the shopper selected for it.
type CartGroup struct {
	GroupNo int
	OfferID string
	Tier    Tier
	LineNos []int
}

// CartSnapshot is what GetCart (shopping-cart) returned.
type CartSnapshot struct {
	CartID  string
	Version int64
	Ready   bool
	Lines   []CartLine
	Groups  []CartGroup
}

// OfferStatus is what GetDeliveryOffer (delivery-promise) returned for a selected offer.
type OfferStatus struct {
	OfferID   string
	Quoted    bool
	ExpiresAt time.Time
}

// QuoteLine is the priced total of one cart line, keyed by catalog item.
type QuoteLine struct {
	CatalogItemID  string
	LineTotalMinor int64
}

// QuoteCharge is the delivery charge of one group.
type QuoteCharge struct {
	GroupNo     int
	ChargeMinor int64
}

// PriceQuote is what IssuePriceQuote (pricing) returned.
type PriceQuote struct {
	QuoteID    string
	Currency   string
	Lines      []QuoteLine
	Charges    []QuoteCharge
	TaxMinor   int64
	TotalMinor int64
	ExpiresAt  time.Time
}

// OpenInput is everything OpenSession needs, fetched by the use case.
type OpenInput struct {
	ID              ID
	ShopperID       string
	ShipToAddressID string
	Cart            CartSnapshot
	Offers          []OfferStatus
	Quote           PriceQuote
}

// Open validates the input and opens an OPEN session (command OpenSession).
// Errors: ErrCartNotCheckoutReady, ErrOfferNotQuotable (no session, no state
// change), ErrContractViolation (a sibling answer cannot form a session) and
// shared.ErrValidation.
func Open(in OpenInput, now time.Time) (*CheckoutSession, error) {
	if err := checkIdentity(in, now); err != nil {
		return nil, err
	}
	if !in.Cart.Ready {
		return nil, ErrCartNotCheckoutReady
	}
	lines, groups, err := numberLinesAndGroups(in.Cart)
	if err != nil {
		return nil, err
	}
	if err := checkOffers(groups, in.Offers, now); err != nil {
		return nil, err
	}
	if err := allocate(lines, groups, in.Quote); err != nil {
		return nil, err
	}
	if !in.Quote.ExpiresAt.After(now) {
		return nil, shared.Invalid("the price quote is already expired")
	}
	at := shared.UTC(now)
	s := &CheckoutSession{
		id: in.ID, state: StateOpen, cartID: in.Cart.CartID, cartVersion: in.Cart.Version,
		shopperID: in.ShopperID, shipToAddressID: in.ShipToAddressID, lines: lines, groups: groups,
		priceQuoteID: in.Quote.QuoteID, currency: in.Quote.Currency, totalMinor: in.Quote.TotalMinor,
		quoteExpiresAt: shared.UTC(in.Quote.ExpiresAt), createdAt: at, updatedAt: at, version: 1,
	}
	s.raise(func(h shared.Header) shared.Event {
		return CheckoutOpened{Header: h, SessionID: s.id, CartID: s.cartID, ShopperID: s.shopperID, TotalMinor: s.totalMinor}
	}, at)
	return s, nil
}

func checkIdentity(in OpenInput, now time.Time) error {
	switch {
	case !shared.ValidID(string(in.ID)):
		return shared.Invalid("sessionId must be a valid id")
	case !shared.ValidID(in.Cart.CartID):
		return shared.Invalid("cartId must be a valid id")
	case !shared.ValidID(in.ShopperID):
		return shared.Invalid("shopperId must be a valid id")
	case !shared.ValidID(in.ShipToAddressID):
		return shared.Invalid("shipToAddressId must be a valid id")
	case now.IsZero():
		return shared.Invalid("an opening instant is required")
	case in.Cart.Version < 1:
		return shared.Invalid("the cart version must be at least 1")
	}
	return nil
}

// checkOffers is the H3 pre-check: every selected offer is QUOTED and does not
// expire within OfferMargin.
func checkOffers(groups []Group, offers []OfferStatus, now time.Time) error {
	byID := map[string]OfferStatus{}
	for _, o := range offers {
		byID[o.OfferID] = o
	}
	for _, g := range groups {
		o, ok := byID[g.OfferID]
		if !ok {
			return contract("no status for offer %q", g.OfferID)
		}
		if !o.Quoted || !o.ExpiresAt.After(now.Add(OfferMargin)) {
			return ErrOfferNotQuotable
		}
	}
	return nil
}
