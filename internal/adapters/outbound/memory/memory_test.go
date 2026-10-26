package memory_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/IQVO/checkout/internal/adapters/outbound/memory"
	"github.com/IQVO/checkout/internal/adapters/outbound/repocontract"
	"github.com/IQVO/checkout/internal/application/exchange"
	cs "github.com/IQVO/checkout/internal/domain/checkoutsession"
)

// t0 is an arbitrary FIXED instant: the store judges TTL on the injected
// clock, never on the wall, so this date never goes stale.
var t0 = time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)

func TestMemoryDoublesHonourTheRepositoryContract(t *testing.T) {
	repocontract.Run(t, func(t *testing.T) repocontract.Env {
		s := memory.NewStore(memory.NewClock(t0))
		return repocontract.Env{
			Sessions: s.Sessions(), Outbox: s.Outbox(), UoW: s.UnitOfWork(), Idempotency: s.Idempotency(),
			Clock:       memory.NewClock(t0),
			OutboxCount: func(*testing.T) int { return len(s.Outbox().Events()) },
		}
	})
}

func TestClockAndIDs(t *testing.T) {
	c := memory.NewClock(t0)
	c.Advance(time.Hour)
	if !c.Now().Equal(t0.Add(time.Hour)) {
		t.Fatal("advance")
	}
	c.Set(t0)
	if !c.Now().Equal(t0) {
		t.Fatal("set")
	}
	g := &memory.SequentialIDs{}
	if g.NewSessionID() != "sess-1" || g.NewSessionID() != "sess-2" {
		t.Fatal("ids")
	}
}

func TestPaymentsAuthorizeIsIdempotentOnItsKey(t *testing.T) {
	p := &memory.Payments{}
	p.Script = []memory.AuthStep{{Result: exchange.Authorization{DeclineReason: "no"}}}
	req := exchange.AuthorizeRequest{SessionID: "s", Token: "t", Currency: "USD", TotalMinor: 5}
	first, _ := p.Authorize(context.Background(), "k", req)
	again, _ := p.Authorize(context.Background(), "k", req)
	if first.Authorized || again.Authorized || len(p.Script) != 0 {
		t.Fatalf("first %+v again %+v", first, again)
	}
	boom := errors.New("boom")
	p.Script = []memory.AuthStep{{Err: boom}}
	if _, err := p.Authorize(context.Background(), "k2", req); !errors.Is(err, boom) {
		t.Fatalf("err %v", err)
	}
}

func TestOffersAndCartsDoubles(t *testing.T) {
	o := memory.NewOffers()
	got, _ := o.GetOffer(context.Background(), "unknown")
	if got.Quoted {
		t.Fatal("an unknown offer is not quotable")
	}
	o.Put(cs.OfferStatus{OfferID: "o", Quoted: true})
	if got, _ = o.GetOffer(context.Background(), "o"); !got.Quoted {
		t.Fatal("put")
	}
	if _, err := memory.NewCarts().GetCart(context.Background(), "x"); err == nil {
		t.Fatal("a missing cart is an error")
	}
}
