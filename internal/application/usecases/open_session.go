package usecases

import (
	"context"
	"errors"

	"github.com/IQVO/checkout/internal/application/exchange"
	"github.com/IQVO/checkout/internal/application/views"
	"github.com/IQVO/checkout/internal/domain/checkoutsession"
)

// OpenSession is the command OpenSession (POST /checkout-sessions).
type OpenSession struct{ Deps }

// OpenCommand is the input of OpenSession. RequestKey is the Idempotency-Key
// of the HTTP call; it keys the pricing call so a retry gets the same quote.
type OpenCommand struct {
	RequestKey      string
	CartID          string
	ShopperID       string
	ShipToAddressID string
}

// Execute reads the cart, checks every selected offer is still QUOTED with
// margin (decision H3: a failure changes no state), issues the binding price
// quote and opens the session.
func (u OpenSession) Execute(ctx context.Context, cmd OpenCommand) (views.Session, error) {
	cart, err := u.Carts.GetCart(ctx, cmd.CartID)
	if err != nil {
		return views.Session{}, err
	}
	if !cart.Snapshot.Ready {
		return views.Session{}, checkoutsession.ErrCartNotCheckoutReady
	}
	offers, err := u.offerStatuses(ctx, cart.Snapshot.Groups)
	if err != nil {
		return views.Session{}, err
	}
	quote, err := u.Quoter.IssueQuote(ctx, cmd.RequestKey+":price-quote", quoteRequest(cart))
	if err != nil {
		return views.Session{}, err
	}
	in := checkoutsession.OpenInput{
		ShopperID: cmd.ShopperID, ShipToAddressID: cmd.ShipToAddressID,
		Cart: cart.Snapshot, Offers: offers, Quote: quote,
	}
	s, err := transact(ctx, u.UoW, func(ctx context.Context) (*checkoutsession.CheckoutSession, error) {
		in.ID = checkoutsession.ID(u.IDs.NewSessionID())
		s, err := checkoutsession.Open(in, u.now())
		if err != nil {
			return nil, err
		}
		return s, u.persist(ctx, s)
	})
	if errors.Is(err, checkoutsession.ErrContractViolation) {
		u.alert(ctx, "sibling-contract-violation", "", err.Error())
	}
	if err != nil {
		return views.Session{}, err
	}
	return views.Of(s), nil
}

func (u OpenSession) offerStatuses(ctx context.Context, groups []checkoutsession.CartGroup) ([]checkoutsession.OfferStatus, error) {
	out := make([]checkoutsession.OfferStatus, 0, len(groups))
	for _, g := range groups {
		o, err := u.Offers.GetOffer(ctx, g.OfferID)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, nil
}

func quoteRequest(cart exchange.Cart) exchange.QuoteRequest {
	lines := make([]checkoutsession.Line, len(cart.Snapshot.Lines))
	for i, l := range cart.Snapshot.Lines {
		lines[i] = checkoutsession.Line{LineNo: i + 1, CatalogItemID: l.CatalogItemID, Quantity: l.Quantity}
	}
	groups := make([]checkoutsession.Group, len(cart.Snapshot.Groups))
	for i, g := range cart.Snapshot.Groups {
		groups[i] = checkoutsession.Group{GroupNo: g.GroupNo, OfferID: g.OfferID, Tier: g.Tier, LineNos: g.LineNos}
	}
	return exchange.QuoteRequest{CartID: cart.Snapshot.CartID, DestinationPostalPrefix: cart.DestinationPostalPrefix, Lines: lines, Groups: groups}
}
