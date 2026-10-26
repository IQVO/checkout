// Package shoppingcart is the outbound HTTP client of shopping-cart: the one
// read checkout makes of it (the model's sync_calls GetCart, REST GET
// /carts/{cartId}). It restates the route and the few fields it needs.
package shoppingcart

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/IQVO/checkout/internal/adapters/outbound/httpx"
	"github.com/IQVO/checkout/internal/application/exchange"
	"github.com/IQVO/checkout/internal/application/ports"
	cs "github.com/IQVO/checkout/internal/domain/checkoutsession"
)

// Gateway implements ports.CartReader over HTTP.
type Gateway struct{ c *httpx.Client }

var _ ports.CartReader = (*Gateway)(nil)

// New builds a Gateway for the shopping-cart base URL.
func New(baseURL string, client *http.Client) *Gateway {
	return &Gateway{c: httpx.New(baseURL, client)}
}

type offerDTO struct {
	OfferID string `json:"offerId"`
	Tier    string `json:"tier"`
}

type groupDTO struct {
	GroupNo int        `json:"groupNo"`
	LineNos []int      `json:"lineNos"`
	Offers  []offerDTO `json:"offers"`
}

type cartDTO struct {
	CartID                  string `json:"cartId"`
	State                   string `json:"state"`
	DestinationPostalPrefix string `json:"destinationPostalPrefix"`
	Lines                   []struct {
		CatalogItemID string `json:"catalogItemId"`
		Quantity      int    `json:"quantity"`
	} `json:"lines"`
	DeliveryQuote *struct {
		Groups []groupDTO `json:"groups"`
	} `json:"deliveryQuote"`
	Selections []struct {
		GroupNo int    `json:"groupNo"`
		OfferID string `json:"offerId"`
	} `json:"selections"`
	CheckoutReadiness struct {
		Ready bool `json:"ready"`
	} `json:"checkoutReadiness"`
	Version int64 `json:"version"`
}

// GetCart implements ports.CartReader: GET /carts/{cartId}. A 404 is
// ports.ErrCartNotFound; a transport error, a timeout, an undecodable body and
// any other status are ports.ErrShoppingCartUnavailable.
func (g *Gateway) GetCart(ctx context.Context, cartID string) (exchange.Cart, error) {
	resp, err := g.c.Do(ctx, http.MethodGet, "/carts/"+url.PathEscape(cartID), "", nil)
	if err != nil {
		return exchange.Cart{}, fmt.Errorf("%w: %w", ports.ErrShoppingCartUnavailable, err)
	}
	switch {
	case resp.Status == http.StatusNotFound:
		return exchange.Cart{}, fmt.Errorf("%w: %s", ports.ErrCartNotFound, cartID)
	case !resp.OK():
		return exchange.Cart{}, fmt.Errorf("%w: status %d", ports.ErrShoppingCartUnavailable, resp.Status)
	}
	var dto cartDTO
	if err := json.Unmarshal(resp.Body, &dto); err != nil {
		return exchange.Cart{}, fmt.Errorf("%w: undecodable response: %w", ports.ErrShoppingCartUnavailable, err)
	}
	return toCart(dto), nil
}

// toCart maps the answer. The cart is ready only when its own readiness says
// so AND it is still OPEN; a group is named by the cart's delivery quote and
// carries the offer the shopper selected for it.
func toCart(dto cartDTO) exchange.Cart {
	snap := cs.CartSnapshot{CartID: dto.CartID, Version: dto.Version, Ready: dto.CheckoutReadiness.Ready && dto.State == "OPEN"}
	for _, l := range dto.Lines {
		snap.Lines = append(snap.Lines, cs.CartLine{CatalogItemID: l.CatalogItemID, Quantity: l.Quantity})
	}
	selected := map[int]string{}
	for _, s := range dto.Selections {
		selected[s.GroupNo] = s.OfferID
	}
	if dto.DeliveryQuote != nil {
		for _, g := range dto.DeliveryQuote.Groups {
			snap.Groups = append(snap.Groups, cs.CartGroup{GroupNo: g.GroupNo, OfferID: selected[g.GroupNo], Tier: tierOf(g, selected[g.GroupNo]), LineNos: g.LineNos})
		}
	}
	return exchange.Cart{Snapshot: snap, DestinationPostalPrefix: dto.DestinationPostalPrefix}
}

func tierOf(g groupDTO, offerID string) cs.Tier {
	for _, o := range g.Offers {
		if o.OfferID == offerID {
			return cs.Tier(o.Tier)
		}
	}
	return ""
}
