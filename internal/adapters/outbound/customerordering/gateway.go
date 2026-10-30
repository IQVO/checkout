package customerordering

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/IQVO/checkout/internal/adapters/outbound/httpx"
	"github.com/IQVO/checkout/internal/application/exchange"
	"github.com/IQVO/checkout/internal/application/ports"
	cs "github.com/IQVO/checkout/internal/domain/checkoutsession"
)

// Gateway implements ports.OrderPlacer over HTTP.
type Gateway struct {
	c      *httpx.Client
	logger *slog.Logger
	zones  *zones
}

var _ ports.OrderPlacer = (*Gateway)(nil)

// Option configures a Gateway.
type Option func(*Gateway)

// WithLogger sets the logger of the gateway's warnings (an unusable requoted
// offer time zone); the default is slog.Default().
func WithLogger(logger *slog.Logger) Option {
	return func(g *Gateway) {
		if logger != nil {
			g.logger = logger
		}
	}
}

// New builds a Gateway for the customer-ordering base URL.
func New(baseURL string, client *http.Client, opts ...Option) *Gateway {
	g := &Gateway{c: httpx.New(baseURL, client), logger: slog.Default()}
	for _, opt := range opts {
		opt(g)
	}
	g.zones = newZones(g.logger)
	return g
}

// BuildRequest implements ports.OrderPlacer.
func (*Gateway) BuildRequest(d exchange.PlacementDraft) ([]byte, error) { return BuildRequest(d) }

// definitiveConflicts are the 409 problem types that mean "this will never
// succeed as sent": the network re-quoted (or could not) and nothing was
// placed. Any other 409 (concurrent-update) is retriable, hence in doubt.
var definitiveConflicts = map[string]bool{
	"offer-not-quotable":         true,
	"promise-capacity-exhausted": true,
	"book-closed":                true,
}

type orderDTO struct {
	OrderID string `json:"orderId"`
}

type offerDTO struct {
	GroupNo           int       `json:"groupNo"`
	OfferID           string    `json:"offerId"`
	Tier              string    `json:"tier"`
	DeliverByEarliest time.Time `json:"deliverByEarliest"`
	DeliverByLatest   time.Time `json:"deliverByLatest"`
	ExpiresAt         time.Time `json:"expiresAt"`
	// TimeZone is the IANA zone of the destination (optional; absent reads
	// as UTC). The window's instants become calendar days in it (ADR 0008).
	TimeZone string `json:"timeZone,omitempty"`
}

type conflictDTO struct {
	RequotedOffers []offerDTO `json:"requotedOffers"`
}

// Place implements ports.OrderPlacer: POST /checkout with Idempotency-Key =
// sessionId. 2xx is the order (a replay answers the first response); a
// definitive 409 is a requote; a 400 or 422 is a rejection (a contract bug on
// our side). Everything else - a transport error, a timeout, a 5xx, a retriable
// 409, an unreadable answer - leaves the outcome unknown and is
// ports.ErrOrderingUnavailable.
func (g *Gateway) Place(ctx context.Context, sessionID string, body []byte) (exchange.PlaceOutcome, error) {
	resp, err := g.c.Do(ctx, http.MethodPost, "/checkout", sessionID, body)
	if err != nil {
		return exchange.PlaceOutcome{}, fmt.Errorf("%w: %w", ports.ErrOrderingUnavailable, err)
	}
	switch {
	case resp.OK():
		return placed(resp.Body)
	case resp.Status == http.StatusConflict && definitiveConflicts[resp.Slug()]:
		return g.requote(resp.Body)
	case resp.Status == http.StatusBadRequest || resp.Status == http.StatusUnprocessableEntity:
		return exchange.PlaceOutcome{Kind: exchange.OutcomeRejected, Detail: fmt.Sprintf("%d %s: %s", resp.Status, resp.Slug(), resp.Detail())}, nil
	}
	return exchange.PlaceOutcome{}, fmt.Errorf("%w: status %d", ports.ErrOrderingUnavailable, resp.Status)
}

func placed(payload []byte) (exchange.PlaceOutcome, error) {
	var o orderDTO
	if err := json.Unmarshal(payload, &o); err != nil || o.OrderID == "" {
		return exchange.PlaceOutcome{}, fmt.Errorf("%w: the order answer has no orderId", ports.ErrOrderingUnavailable)
	}
	return exchange.PlaceOutcome{Kind: exchange.OutcomePlaced, OrderID: o.OrderID}, nil
}

// requote turns the 409's requoted offers into the cart's offers. The window
// is a span of instants; the shopper reads it as days of the DESTINATION's
// calendar, so each bound is formatted in the offer's timeZone (UTC when it
// is absent or unusable, ADR 0008).
func (g *Gateway) requote(payload []byte) (exchange.PlaceOutcome, error) {
	var c conflictDTO
	if err := json.Unmarshal(payload, &c); err != nil {
		return exchange.PlaceOutcome{}, fmt.Errorf("%w: undecodable conflict: %w", ports.ErrOrderingUnavailable, err)
	}
	out := exchange.PlaceOutcome{Kind: exchange.OutcomeRequote}
	for _, o := range c.RequotedOffers {
		loc := g.zones.location(o.TimeZone)
		out.Offers = append(out.Offers, cs.RequotedOffer{
			OfferID: o.OfferID, GroupNo: o.GroupNo, Tier: cs.Tier(o.Tier),
			DeliverByEarliest: o.DeliverByEarliest.In(loc).Format(time.DateOnly), DeliverByLatest: o.DeliverByLatest.In(loc).Format(time.DateOnly),
			ExpiresAt: o.ExpiresAt.UTC(),
		})
	}
	return out, nil
}
