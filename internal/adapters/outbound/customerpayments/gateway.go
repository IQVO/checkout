// Package customerpayments is the outbound HTTP client of customer-payments:
// AuthorizePayment (REST POST /payment-authorizations) and VoidAuthorization
// (REST POST /payment-authorizations/{id}:void).
package customerpayments

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/IQVO/checkout/internal/adapters/outbound/httpx"
	"github.com/IQVO/checkout/internal/application/exchange"
	"github.com/IQVO/checkout/internal/application/ports"
)

// Gateway implements ports.PaymentGateway over HTTP.
type Gateway struct{ c *httpx.Client }

var _ ports.PaymentGateway = (*Gateway)(nil)

// New builds a Gateway for the customer-payments base URL.
func New(baseURL string, client *http.Client) *Gateway {
	return &Gateway{c: httpx.New(baseURL, client)}
}

type allocation struct {
	GroupNo     int   `json:"groupNo"`
	AmountMinor int64 `json:"amountMinor"`
}

type authorizeRequest struct {
	CheckoutSessionID  string       `json:"checkoutSessionId"`
	PaymentMethodToken string       `json:"paymentMethodToken"`
	Currency           string       `json:"currency"`
	TotalMinor         int64        `json:"totalMinor"`
	Allocations        []allocation `json:"allocations"`
}

type authorization struct {
	AuthorizationID string `json:"authorizationId"`
	State           string `json:"state"`
	Currency        string `json:"currency"`
	TotalMinor      int64  `json:"totalMinor"`
}

// Authorize implements ports.PaymentGateway: POST /payment-authorizations. A
// 201 AUTHORIZED is an authorization, a 201 DECLINED (or a 422 we caused) a
// decline; a transport error, a timeout, a 409 (duplicate-authorization while a
// void is pending, or concurrent-update) and any 5xx are
// ports.ErrPaymentsUnavailable, so the session stays AUTHORIZING and
// ResolveStuckAuthorizations re-calls with the same key.
func (g *Gateway) Authorize(ctx context.Context, key string, req exchange.AuthorizeRequest) (exchange.Authorization, error) {
	body := authorizeRequest{CheckoutSessionID: req.SessionID, PaymentMethodToken: req.Token, Currency: req.Currency, TotalMinor: req.TotalMinor}
	for _, a := range req.Allocations {
		body.Allocations = append(body.Allocations, allocation(a))
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return exchange.Authorization{}, fmt.Errorf("encode authorize request: %w", err)
	}
	resp, err := g.c.Do(ctx, http.MethodPost, "/payment-authorizations", key, raw)
	if err != nil {
		return exchange.Authorization{}, fmt.Errorf("%w: %w", ports.ErrPaymentsUnavailable, err)
	}
	switch {
	case resp.OK():
		return decode(resp.Body)
	case resp.Status == http.StatusUnprocessableEntity:
		return exchange.Authorization{DeclineReason: "authorization-rejected: " + resp.Slug()}, nil
	}
	return exchange.Authorization{}, fmt.Errorf("%w: status %d", ports.ErrPaymentsUnavailable, resp.Status)
}

func decode(payload []byte) (exchange.Authorization, error) {
	var a authorization
	if err := json.Unmarshal(payload, &a); err != nil {
		return exchange.Authorization{}, fmt.Errorf("%w: undecodable response: %w", ports.ErrPaymentsUnavailable, err)
	}
	if a.State != "AUTHORIZED" {
		return exchange.Authorization{ID: a.AuthorizationID, DeclineReason: "payment-" + strings.ToLower(a.State)}, nil
	}
	return exchange.Authorization{ID: a.AuthorizationID, Authorized: true, Currency: a.Currency, TotalMinor: a.TotalMinor}, nil
}

// Void implements ports.PaymentGateway: POST /payment-authorizations/{id}:void.
// A 200 is done; a 404 (nothing to void) and a 409 illegal-transition (already
// voided, captured or expired) mean there is nothing left to do. A transport
// error, a timeout, a 409 concurrent-update and any other status are
// ports.ErrPaymentsUnavailable, so the debt stays and RetryOwedVoids tries again.
func (g *Gateway) Void(ctx context.Context, key, authorizationID string) error {
	resp, err := g.c.Do(ctx, http.MethodPost, "/payment-authorizations/"+url.PathEscape(authorizationID)+":void", key, nil)
	if err != nil {
		return fmt.Errorf("%w: %w", ports.ErrPaymentsUnavailable, err)
	}
	switch {
	case resp.OK(), resp.Status == http.StatusNotFound:
		return nil
	case resp.Status == http.StatusConflict && resp.Slug() == "illegal-transition":
		return nil
	}
	return fmt.Errorf("%w: status %d", ports.ErrPaymentsUnavailable, resp.Status)
}
