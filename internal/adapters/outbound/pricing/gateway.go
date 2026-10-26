// Package pricing is the outbound HTTP client of pricing: IssuePriceQuote (the
// model's sync_calls, REST POST /price-quotes) at session open.
package pricing

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/IQVO/checkout/internal/adapters/outbound/httpx"
	"github.com/IQVO/checkout/internal/application/exchange"
	"github.com/IQVO/checkout/internal/application/ports"
	cs "github.com/IQVO/checkout/internal/domain/checkoutsession"
)

// Gateway implements ports.PriceQuoter over HTTP.
type Gateway struct{ c *httpx.Client }

var _ ports.PriceQuoter = (*Gateway)(nil)

// New builds a Gateway for the pricing base URL.
func New(baseURL string, client *http.Client) *Gateway {
	return &Gateway{c: httpx.New(baseURL, client)}
}

type requestLine struct {
	CatalogItemID string `json:"catalogItemId"`
	Quantity      int    `json:"quantity"`
}

type requestGroup struct {
	GroupNo int    `json:"groupNo"`
	OfferID string `json:"offerId"`
	Tier    string `json:"tier"`
	LineNos []int  `json:"lineNos"`
}

type quoteRequest struct {
	CartID                  string         `json:"cartId"`
	DestinationPostalPrefix string         `json:"destinationPostalPrefix"`
	Lines                   []requestLine  `json:"lines"`
	Groups                  []requestGroup `json:"groups"`
}

type money struct {
	AmountMinor int64  `json:"amountMinor"`
	Currency    string `json:"currency"`
}

type quoteResponse struct {
	QuoteID  string `json:"quoteId"`
	Currency string `json:"currency"`
	Lines    []struct {
		CatalogItemID string `json:"catalogItemId"`
		LineTotal     money  `json:"lineTotal"`
	} `json:"lines"`
	Groups []struct {
		GroupNo        int   `json:"groupNo"`
		DeliveryCharge money `json:"deliveryCharge"`
	} `json:"groups"`
	TaxMinor   int64     `json:"taxMinor"`
	TotalMinor int64     `json:"totalMinor"`
	ExpiresAt  time.Time `json:"expiresAt"`
}

// IssueQuote implements ports.PriceQuoter: POST /price-quotes with the lines
// and groups the session froze, forwarding the Idempotency-Key. A 4xx is
// ports.ErrQuoteRejected (an unpriced item or a request we got wrong); a
// transport error, a timeout, an undecodable body and any 5xx are
// ports.ErrPricingUnavailable.
func (g *Gateway) IssueQuote(ctx context.Context, key string, req exchange.QuoteRequest) (cs.PriceQuote, error) {
	body := quoteRequest{CartID: req.CartID, DestinationPostalPrefix: req.DestinationPostalPrefix}
	for _, l := range req.Lines {
		body.Lines = append(body.Lines, requestLine{CatalogItemID: l.CatalogItemID, Quantity: l.Quantity})
	}
	for _, gr := range req.Groups {
		body.Groups = append(body.Groups, requestGroup{GroupNo: gr.GroupNo, OfferID: gr.OfferID, Tier: string(gr.Tier), LineNos: gr.LineNos})
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return cs.PriceQuote{}, fmt.Errorf("encode price quote request: %w", err)
	}
	resp, err := g.c.Do(ctx, http.MethodPost, "/price-quotes", key, raw)
	if err != nil {
		return cs.PriceQuote{}, fmt.Errorf("%w: %w", ports.ErrPricingUnavailable, err)
	}
	switch {
	case resp.OK():
		return decode(resp.Body)
	case resp.Status >= 400 && resp.Status < 500:
		return cs.PriceQuote{}, fmt.Errorf("%w: status %d: %s", ports.ErrQuoteRejected, resp.Status, resp.Detail())
	}
	return cs.PriceQuote{}, fmt.Errorf("%w: status %d", ports.ErrPricingUnavailable, resp.Status)
}

func decode(payload []byte) (cs.PriceQuote, error) {
	var r quoteResponse
	if err := json.Unmarshal(payload, &r); err != nil {
		return cs.PriceQuote{}, fmt.Errorf("%w: undecodable response: %w", ports.ErrPricingUnavailable, err)
	}
	q := cs.PriceQuote{QuoteID: r.QuoteID, Currency: r.Currency, TaxMinor: r.TaxMinor, TotalMinor: r.TotalMinor, ExpiresAt: r.ExpiresAt.UTC()}
	for _, l := range r.Lines {
		q.Lines = append(q.Lines, cs.QuoteLine{CatalogItemID: l.CatalogItemID, LineTotalMinor: l.LineTotal.AmountMinor})
	}
	for _, gr := range r.Groups {
		q.Charges = append(q.Charges, cs.QuoteCharge{GroupNo: gr.GroupNo, ChargeMinor: gr.DeliveryCharge.AmountMinor})
	}
	return q, nil
}
