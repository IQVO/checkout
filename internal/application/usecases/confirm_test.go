package usecases_test

import (
	"errors"
	"testing"
	"time"

	"github.com/IQVO/checkout/internal/adapters/outbound/memory"
	"github.com/IQVO/checkout/internal/application/exchange"
	"github.com/IQVO/checkout/internal/application/ports"
	cs "github.com/IQVO/checkout/internal/domain/checkoutsession"
)

func TestConfirmHappyPathCompletes(t *testing.T) {
	e := newEnv()
	e.open(t)
	v, err := e.confirm("sess-1", "tok_1")
	if err != nil {
		t.Fatal(err)
	}
	if v.State != cs.StateCompleted || v.OrderID != "order-sess-1" || v.AuthorizationID != "auth-sess-1:auth:0" {
		t.Fatalf("view %+v", v)
	}
	if !equalStrings(e.payments.AuthKeys, []string{"sess-1:auth:0"}) || e.placer.Calls[0].SessionID != "sess-1" {
		t.Fatalf("keys %v calls %v", e.payments.AuthKeys, e.placer.Calls)
	}
	if !equalStrings(e.eventNames(), []string{"CheckoutOpened", "CheckoutCompleted"}) {
		t.Fatalf("events %v", e.eventNames())
	}
	if len(e.payments.VoidKeys) != 0 {
		t.Fatal("no void on success")
	}
}

func TestConfirmDeclineKeepsSessionOpenForAnotherToken(t *testing.T) {
	e := newEnv()
	e.open(t)
	e.payments.Script = []memory.AuthStep{{Result: exchange.Authorization{DeclineReason: "insufficient-funds"}}}
	v, err := e.confirm("sess-1", "tok_1")
	if err != nil || v.State != cs.StateDeclined {
		t.Fatalf("view %+v err %v", v, err)
	}
	v, err = e.confirm("sess-1", "tok_2")
	if err != nil || v.State != cs.StateCompleted {
		t.Fatalf("view %+v err %v", v, err)
	}
	if !equalStrings(e.payments.AuthKeys, []string{"sess-1:auth:0", "sess-1:auth:1"}) || !equalStrings(e.payments.Tokens, []string{"tok_1", "tok_2"}) {
		t.Fatalf("keys %v tokens %v", e.payments.AuthKeys, e.payments.Tokens)
	}
	if !equalStrings(e.eventNames(), []string{"CheckoutOpened", "CheckoutDeclined", "CheckoutCompleted"}) {
		t.Fatalf("events %v", e.eventNames())
	}
}

func TestConfirmDeclineWithoutReasonStillDeclines(t *testing.T) {
	e := newEnv()
	e.open(t)
	e.payments.Script = []memory.AuthStep{{Result: exchange.Authorization{}}}
	v, err := e.confirm("sess-1", "tok_1")
	if err != nil || v.State != cs.StateDeclined {
		t.Fatalf("view %+v err %v", v, err)
	}
}

func TestConfirmAuthorizationUnavailableLeavesAuthorizing(t *testing.T) {
	e := newEnv()
	e.open(t)
	e.payments.Script = []memory.AuthStep{{Err: ports.ErrPaymentsUnavailable}}
	v, err := e.confirm("sess-1", "tok_1")
	if err != nil || v.State != cs.StateAuthorizing || !v.Unsettled() {
		t.Fatalf("view %+v err %v", v, err)
	}
	if len(e.placer.Calls) != 0 {
		t.Fatal("never place without an authorization")
	}
}

func TestConfirmGuardsPassThrough(t *testing.T) {
	e := newEnv()
	e.open(t)
	e.clock.Advance(16 * time.Minute)
	if _, err := e.confirm("sess-1", "tok"); !errors.Is(err, cs.ErrPriceQuoteExpired) {
		t.Fatalf("err %v", err)
	}
	if _, err := e.confirm("missing", "tok"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("err %v", err)
	}
}

func TestConfirmAuthorizationMismatchVoidsAndReportsIt(t *testing.T) {
	e := newEnv()
	e.open(t)
	e.payments.Script = []memory.AuthStep{{Mutate: func(a *exchange.Authorization) { a.TotalMinor-- }}}
	v, err := e.confirm("sess-1", "tok_1")
	if !errors.Is(err, cs.ErrAuthorizationMismatch) || v.State != cs.StateDeclined {
		t.Fatalf("view %+v err %v", v, err)
	}
	if !equalStrings(e.payments.Voided, []string{"auth-sess-1:auth:0"}) || !equalStrings(e.alerts.Codes(), []string{"authorization-mismatch"}) {
		t.Fatalf("voided %v alerts %v", e.payments.Voided, e.alerts.Codes())
	}
	if len(e.placer.Calls) != 0 {
		t.Fatal("never place on a mismatch")
	}
	// the void succeeded, so the shopper can try again
	if _, err := e.confirm("sess-1", "tok_2"); err != nil {
		t.Fatalf("retry: %v", err)
	}
}
