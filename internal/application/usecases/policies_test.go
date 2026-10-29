package usecases_test

import (
	"context"
	"testing"
	"time"

	"github.com/IQVO/checkout/internal/adapters/outbound/memory"
	"github.com/IQVO/checkout/internal/application/exchange"
	"github.com/IQVO/checkout/internal/application/ports"
	"github.com/IQVO/checkout/internal/application/usecases"
	cs "github.com/IQVO/checkout/internal/domain/checkoutsession"
)

var bg = context.Background()

func inDoubt(t *testing.T, e *env) {
	t.Helper()
	e.open(t)
	e.placer.Script = []memory.PlaceStep{{Err: ports.ErrOrderingUnavailable}}
	if v, err := e.confirm("sess-1", "tok_1"); err != nil || v.State != cs.StatePlacing {
		t.Fatalf("setup: %+v %v", v, err)
	}
}

func TestResolveInDoubtReplaysSameBodyAndKeyWithBackoff(t *testing.T) {
	e := newEnv()
	inDoubt(t, e)
	r := usecases.ResolveInDoubtPlacements{Deps: e.deps}
	if n, _ := r.Execute(bg, 10); n != 0 {
		t.Fatalf("not due inside the backoff, handled %d", n)
	}
	e.clock.Advance(31 * time.Second)
	e.placer.Script = []memory.PlaceStep{{Err: ports.ErrOrderingUnavailable}}
	if n, err := r.Execute(bg, 10); err != nil || n != 1 {
		t.Fatalf("n %d err %v", n, err)
	}
	e.clock.Advance(2 * time.Minute)
	if n, err := r.Execute(bg, 10); err != nil || n != 1 {
		t.Fatalf("n %d err %v", n, err)
	}
	if v := e.get(t, "sess-1"); v.State != cs.StateCompleted {
		t.Fatalf("state %s", v.State)
	}
	first := e.placer.Calls[0]
	for _, c := range e.placer.Calls {
		if c.SessionID != "sess-1" || string(c.Body) != string(first.Body) {
			t.Fatalf("every pass replays the same key and body: %+v", e.placer.Calls)
		}
	}
	if len(e.placer.Calls) != 3 {
		t.Fatalf("calls %d", len(e.placer.Calls))
	}
}

func TestResolveInDoubtAlertsOnceAtTwentyHours(t *testing.T) {
	e := newEnv()
	inDoubt(t, e)
	r := usecases.ResolveInDoubtPlacements{Deps: e.deps}
	for i := 0; i < 3; i++ {
		e.clock.Advance(20*time.Hour + time.Duration(i)*time.Hour/2)
		e.placer.Script = []memory.PlaceStep{{Err: ports.ErrOrderingUnavailable}}
		if _, err := r.Execute(bg, 10); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			e.clock.Advance(-20 * time.Hour)
		}
	}
	codes := e.alerts.Codes()
	if len(codes) == 0 || codes[0] != "placement-in-doubt-20h" {
		t.Fatalf("alerts %v", codes)
	}
	count := 0
	for _, c := range codes {
		if c == "placement-in-doubt-20h" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("the 20 h alert fires once, got %v", codes)
	}
}

func TestResolveInDoubtGivesUpAndVoidsAtTwentyTwoHours(t *testing.T) {
	e := newEnv()
	inDoubt(t, e)
	e.clock.Advance(22*time.Hour + 5*time.Minute)
	e.placer.Script = []memory.PlaceStep{{Err: ports.ErrOrderingUnavailable}}
	n, err := usecases.ResolveInDoubtPlacements{Deps: e.deps}.Execute(bg, 10)
	if err != nil || n != 1 {
		t.Fatalf("n %d err %v", n, err)
	}
	v := e.get(t, "sess-1")
	if v.State != cs.StateFailed || v.FailureCode != "placement-unresolved" {
		t.Fatalf("view %+v", v)
	}
	if len(e.payments.Voided) != 1 {
		t.Fatalf("voided %v", e.payments.Voided)
	}
	if !equalStrings(e.alerts.Codes(), []string{"placement-in-doubt-20h", "placement-given-up-22h"}) {
		t.Fatalf("alerts %v", e.alerts.Codes())
	}
}

func TestResolveInDoubtLateDefinitiveAnswerBeatsGiveUp(t *testing.T) {
	e := newEnv()
	inDoubt(t, e)
	e.clock.Advance(23 * time.Hour)
	if _, err := (usecases.ResolveInDoubtPlacements{Deps: e.deps}).Execute(bg, 10); err != nil {
		t.Fatal(err)
	}
	if v := e.get(t, "sess-1"); v.State != cs.StateCompleted || len(e.payments.Voided) != 0 {
		t.Fatalf("view %+v voided %v", v, e.payments.Voided)
	}
}

func TestResolveStuckAuthorizationReCallsWithTheSameKey(t *testing.T) {
	e := newEnv()
	e.open(t)
	e.payments.Script = []memory.AuthStep{{Err: ports.ErrPaymentsUnavailable}}
	if v, _ := e.confirm("sess-1", "tok_1"); v.State != cs.StateAuthorizing {
		t.Fatalf("setup %+v", v)
	}
	r := usecases.ResolveStuckAuthorizations{Deps: e.deps}
	if n, _ := r.Execute(bg, 10); n != 0 {
		t.Fatalf("not stuck yet, handled %d", n)
	}
	e.clock.Advance(2 * time.Minute)
	if n, err := r.Execute(bg, 10); err != nil || n != 1 {
		t.Fatalf("n %d err %v", n, err)
	}
	if v := e.get(t, "sess-1"); v.State != cs.StateCompleted {
		t.Fatalf("state %s", v.State)
	}
	if !equalStrings(e.payments.AuthKeys, []string{"sess-1:auth:0", "sess-1:auth:0"}) || !equalStrings(e.payments.Tokens, []string{"tok_1", "tok_1"}) {
		t.Fatalf("keys %v tokens %v", e.payments.AuthKeys, e.payments.Tokens)
	}
}

func TestExpireOnQuoteExpiryCoversOpenAndDeclined(t *testing.T) {
	e := newEnv()
	e.open(t)
	e.deps.IDs = &memory.SequentialIDs{}
	declinedID := func() {
		e.payments.Script = []memory.AuthStep{{Result: exchange.Authorization{DeclineReason: "no"}}}
		if _, err := e.confirm("sess-2", "tok"); err != nil {
			t.Fatal(err)
		}
	}
	usecases.OpenSession{Deps: e.deps}.Execute(bg, usecases.OpenCommand{RequestKey: "k2", CartID: "cart-1", ShopperID: "s", ShipToAddressID: "a"})
	declinedID()
	x := usecases.ExpireOnQuoteExpiry{Deps: e.deps}
	if n, _ := x.Execute(bg, 10); n != 0 {
		t.Fatalf("quotes still valid, expired %d", n)
	}
	e.clock.Advance(15 * time.Minute)
	if n, err := x.Execute(bg, 10); err != nil || n != 2 {
		t.Fatalf("n %d err %v", n, err)
	}
	if e.get(t, "sess-1").State != cs.StateExpired || e.get(t, "sess-2").State != cs.StateExpired {
		t.Fatal("both expire")
	}
	if n, _ := x.Execute(bg, 10); n != 0 {
		t.Fatal("a second sweep is harmless")
	}
}

func TestRetryOwedVoidsUntilSuccess(t *testing.T) {
	e := newEnv()
	e.open(t)
	e.payments.VoidErr = ports.ErrPaymentsUnavailable
	e.placer.Script = []memory.PlaceStep{{Outcome: requoteOutcome()}}
	e.confirm("sess-1", "tok_1")
	r := usecases.RetryOwedVoids{Deps: e.deps}
	if n, _ := r.Execute(bg, 10); n != 0 {
		t.Fatalf("still failing, done %d", n)
	}
	e.payments.VoidErr = nil
	if n, err := r.Execute(bg, 10); err != nil || n != 1 {
		t.Fatalf("n %d err %v", n, err)
	}
	if n, _ := r.Execute(bg, 10); n != 0 {
		t.Fatal("nothing owed any more")
	}
	if !equalStrings(e.payments.VoidKeys, []string{"sess-1:void", "sess-1:void", "sess-1:void"}) {
		t.Fatalf("keys %v", e.payments.VoidKeys)
	}
}

func TestPurgeStoredPlacementsAfterTwentyFourHours(t *testing.T) {
	e := newEnv()
	e.open(t)
	e.confirm("sess-1", "tok_1")
	p := usecases.PurgeStoredPlacements{Deps: e.deps}
	e.clock.Advance(23 * time.Hour)
	if n, _ := p.Execute(bg, 10); n != 0 {
		t.Fatalf("too early, purged %d", n)
	}
	e.clock.Advance(time.Hour)
	if n, err := p.Execute(bg, 10); err != nil || n != 1 {
		t.Fatalf("n %d err %v", n, err)
	}
	s, _ := e.deps.Sessions.Get(bg, "sess-1")
	if s.Placement() != nil {
		t.Fatal("placement must be gone")
	}
}
