// Package deliverypromise is the outbound HTTP client of delivery-promise: the
// pre-check checkout makes before authorizing (the model's sync_calls
// GetDeliveryOffer, REST GET /delivery-offers/{offerId} on the network's Open
// Host Service). It restates the one route it uses and nothing else.
package deliverypromise

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/IQVO/checkout/internal/adapters/outbound/httpx"
	"github.com/IQVO/checkout/internal/application/ports"
	cs "github.com/IQVO/checkout/internal/domain/checkoutsession"
)

// Gateway implements ports.OfferReader over HTTP.
type Gateway struct{ c *httpx.Client }

var _ ports.OfferReader = (*Gateway)(nil)

// New builds a Gateway for the delivery-promise base URL.
func New(baseURL string, client *http.Client) *Gateway {
	return &Gateway{c: httpx.New(baseURL, client)}
}

type offerDTO struct {
	OfferID   string    `json:"offerId"`
	State     string    `json:"state"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// GetOffer implements ports.OfferReader: GET /delivery-offers/{offerId}. A 404
// is an offer that is not quotable; a transport error, a timeout, an
// undecodable body and any other status are ports.ErrDeliveryPromiseUnavailable.
func (g *Gateway) GetOffer(ctx context.Context, offerID string) (cs.OfferStatus, error) {
	resp, err := g.c.Do(ctx, http.MethodGet, "/delivery-offers/"+url.PathEscape(offerID), "", nil)
	if err != nil {
		return cs.OfferStatus{}, fmt.Errorf("%w: %w", ports.ErrDeliveryPromiseUnavailable, err)
	}
	switch {
	case resp.Status == http.StatusNotFound:
		return cs.OfferStatus{OfferID: offerID}, nil
	case !resp.OK():
		return cs.OfferStatus{}, fmt.Errorf("%w: status %d", ports.ErrDeliveryPromiseUnavailable, resp.Status)
	}
	var dto offerDTO
	if err := json.Unmarshal(resp.Body, &dto); err != nil {
		return cs.OfferStatus{}, fmt.Errorf("%w: undecodable response: %w", ports.ErrDeliveryPromiseUnavailable, err)
	}
	return cs.OfferStatus{OfferID: offerID, Quoted: dto.State == "QUOTED", ExpiresAt: dto.ExpiresAt.UTC()}, nil
}
