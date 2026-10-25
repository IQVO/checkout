package memory

import (
	"context"
	"sync"

	"github.com/IQVO/checkout/internal/application/exchange"
)

// Payments is a scriptable customer-payments double. Authorize is idempotent
// on its key (a repeated key returns the first answer), like the real service.
type Payments struct {
	mu sync.Mutex
	// Script holds the next answers of Authorize (oldest first); when empty an
	// authorization for the requested total is returned.
	Script []AuthStep
	// VoidErr, when set, is returned by Void.
	VoidErr error
	// AuthKeys and VoidKeys are the idempotency keys received, in order.
	AuthKeys []string
	VoidKeys []string
	// Voided lists the authorization ids voided successfully.
	Voided []string
	// Tokens are the PSP tokens received by Authorize, in order.
	Tokens []string
	first  map[string]exchange.Authorization
}

// AuthStep is one scripted answer of Authorize.
type AuthStep struct {
	Result exchange.Authorization
	Err    error
	// Mutate, when set, edits the default authorization before it is returned
	// (to script an amount mismatch); used when Result.ID is empty.
	Mutate func(*exchange.Authorization)
}

// Authorize implements ports.PaymentGateway.
func (p *Payments) Authorize(_ context.Context, key string, req exchange.AuthorizeRequest) (exchange.Authorization, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.AuthKeys = append(p.AuthKeys, key)
	p.Tokens = append(p.Tokens, req.Token)
	if a, ok := p.first[key]; ok {
		return a, nil
	}
	a := exchange.Authorization{Authorized: true, Currency: req.Currency, TotalMinor: req.TotalMinor}
	if len(p.Script) > 0 {
		step := p.Script[0]
		p.Script = p.Script[1:]
		if step.Err != nil {
			return exchange.Authorization{}, step.Err
		}
		a = p.apply(step, a)
	}
	if a.ID == "" {
		a.ID = "auth-" + key
	}
	if p.first == nil {
		p.first = map[string]exchange.Authorization{}
	}
	p.first[key] = a
	return a, nil
}

func (p *Payments) apply(step AuthStep, def exchange.Authorization) exchange.Authorization {
	if step.Mutate != nil {
		step.Mutate(&def)
		return def
	}
	if step.Result.ID != "" || !step.Result.Authorized {
		return step.Result
	}
	return def
}

// Void implements ports.PaymentGateway.
func (p *Payments) Void(_ context.Context, key, authorizationID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.VoidKeys = append(p.VoidKeys, key)
	if p.VoidErr != nil {
		return p.VoidErr
	}
	p.Voided = append(p.Voided, authorizationID)
	return nil
}
