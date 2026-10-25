package memory

import (
	"context"
	"sync"

	"github.com/IQVO/checkout/internal/application/exchange"
)

// Placer is a scriptable customer-ordering double. It renders the draft as
// plain JSON; the real wire names live in the customerordering adapter.
type Placer struct {
	mu sync.Mutex
	// Script holds the next answers of Place (oldest first); when empty the
	// order is placed as order-<sessionId>.
	Script []PlaceStep
	// Calls are the (sessionId, body) pairs received, in order.
	Calls []PlaceCall
}

// PlaceStep is one scripted answer of Place.
type PlaceStep struct {
	Outcome exchange.PlaceOutcome
	Err     error
}

// PlaceCall is one received Place call.
type PlaceCall struct {
	SessionID string
	Body      []byte
}

// BuildRequest implements ports.OrderPlacer.
func (p *Placer) BuildRequest(d exchange.PlacementDraft) ([]byte, error) { return draftJSON(d) }

// Place implements ports.OrderPlacer.
func (p *Placer) Place(_ context.Context, sessionID string, body []byte) (exchange.PlaceOutcome, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Calls = append(p.Calls, PlaceCall{SessionID: sessionID, Body: append([]byte(nil), body...)})
	if len(p.Script) == 0 {
		return exchange.PlaceOutcome{Kind: exchange.OutcomePlaced, OrderID: "order-" + sessionID}, nil
	}
	step := p.Script[0]
	p.Script = p.Script[1:]
	return step.Outcome, step.Err
}
