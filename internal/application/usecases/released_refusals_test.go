package usecases_test

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/IQVO/checkout/internal/adapters/outbound/memory"
	"github.com/IQVO/checkout/internal/application/exchange"
	"github.com/IQVO/checkout/internal/application/usecases"
	cs "github.com/IQVO/checkout/internal/domain/checkoutsession"
)

// A refusal customer-ordering releases (replay class RETRYABLE, ADR 0011) is
// not an answer: the session stays PLACING under the semantic lock and the
// resolver sends the same stored body with the same key (the session id) until
// it passes or the in-doubt budget (20 h alert, 22 h give up) is spent.

func released(kind exchange.OutcomeKind, slug, detail string) exchange.PlaceOutcome {
	return exchange.PlaceOutcome{Kind: kind, Slug: slug, Detail: detail, Released: true}
}

func envWithLog() (*env, *bytes.Buffer) {
	e := newEnv()
	var buf bytes.Buffer
	e.deps.Log = slog.New(slog.NewTextHandler(&buf, nil))
	return e, &buf
}

func TestAReleasedRefusalKeepsTheSessionPlacingAndALaterSuccessCompletesUnderTheSameKey(t *testing.T) {
	for name, refusal := range map[string]exchange.PlaceOutcome{
		"rejection (422 validation-failed)": released(exchange.OutcomeRejected, "validation-failed", "422 validation-failed: shopper is not registered"),
		"requote (409 book-closed)":         released(exchange.OutcomeRequote, "book-closed", "409 book-closed: the promise book is closed"),
	} {
		e, logs := envWithLog()
		e.open(t)
		e.placer.Script = []memory.PlaceStep{{Outcome: refusal}}
		v, err := e.confirm("sess-1", "tok_1")
		if err != nil || v.State != cs.StatePlacing || !v.Unsettled() {
			t.Fatalf("%s: view %+v err %v", name, v, err)
		}
		if len(e.payments.VoidKeys) != 0 || len(e.alerts.Codes()) != 0 {
			t.Fatalf("%s: the semantic lock holds, no void %v and no rejection alert %v", name, e.payments.VoidKeys, e.alerts.Codes())
		}
		if !strings.Contains(logs.String(), refusal.Slug) || !strings.Contains(logs.String(), "sess-1") {
			t.Fatalf("%s: the last refusal is logged with the session: %s", name, logs.String())
		}
		e.clock.Advance(31 * time.Second)
		if n, err := resolver(e).Execute(bg, 10); err != nil || n != 1 {
			t.Fatalf("%s: n %d err %v", name, n, err)
		}
		assertCompletedUnderTheSameKey(t, name, e)
	}
}

func assertCompletedUnderTheSameKey(t *testing.T, name string, e *env) {
	t.Helper()
	if v := e.get(t, "sess-1"); v.State != cs.StateCompleted {
		t.Fatalf("%s: a later success completes the session, got %+v", name, v)
	}
	calls := e.placer.Calls
	if len(calls) != 2 || calls[0].SessionID != "sess-1" || calls[1].SessionID != "sess-1" || string(calls[0].Body) != string(calls[1].Body) {
		t.Fatalf("%s: the same key and the same stored body both times: %+v", name, calls)
	}
	if len(e.payments.VoidKeys) != 0 || !equalStrings(e.eventNames(), []string{"CheckoutOpened", "CheckoutPlacementInDoubt", "CheckoutCompleted"}) {
		t.Fatalf("%s: void %v events %v", name, e.payments.VoidKeys, e.eventNames())
	}
}

func TestEveryReleasedRefusalCountsAnAttemptAndTheLastOneIsLogged(t *testing.T) {
	e, logs := envWithLog()
	e.open(t)
	e.placer.Script = []memory.PlaceStep{
		{Outcome: released(exchange.OutcomeRejected, "validation-failed", "422 validation-failed: first")},
		{Outcome: released(exchange.OutcomeRequote, "promise-capacity-exhausted", "409 promise-capacity-exhausted: second")},
	}
	if v, _ := e.confirm("sess-1", "tok_1"); v.State != cs.StatePlacing {
		t.Fatalf("setup %+v", v)
	}
	e.clock.Advance(31 * time.Second)
	if _, err := resolver(e).Execute(bg, 10); err != nil {
		t.Fatal(err)
	}
	s, err := e.deps.Sessions.Get(bg, "sess-1")
	if err != nil || s.State() != cs.StatePlacing || s.PlacementAttempts() != 2 {
		t.Fatalf("still PLACING after two refusals, attempts counted: %v %v", s, err)
	}
	if !strings.Contains(logs.String(), "second") {
		t.Fatalf("the newest refusal is logged: %s", logs.String())
	}
}

// At the existing bound (22 h) a refusal customer-ordering keeps releasing ends
// the session in the terminal state it always had: FAILED place-order-rejected
// for a rejection, REQUOTE_REQUIRED for a requote, the void owed in both.
func TestAReleasedRefusalAtTheGiveUpBoundEndsInTheTerminalStateItHadBefore(t *testing.T) {
	e := newEnv()
	e.open(t)
	e.placer.Script = []memory.PlaceStep{{Outcome: released(exchange.OutcomeRejected, "group-spans-nodes", "422 group-spans-nodes")}}
	if v, _ := e.confirm("sess-1", "tok_1"); v.State != cs.StatePlacing {
		t.Fatalf("setup %+v", v)
	}
	e.clock.Advance(22*time.Hour + 5*time.Minute)
	e.placer.Script = []memory.PlaceStep{{Outcome: released(exchange.OutcomeRejected, "group-spans-nodes", "422 group-spans-nodes")}}
	if _, err := resolver(e).Execute(bg, 10); err != nil {
		t.Fatal(err)
	}
	v := e.get(t, "sess-1")
	if v.State != cs.StateFailed || v.FailureCode != "place-order-rejected" || len(e.payments.Voided) != 1 {
		t.Fatalf("view %+v voided %v", v, e.payments.Voided)
	}
	if !equalStrings(e.alerts.Codes(), []string{"placement-in-doubt-20h", "place-order-rejected"}) {
		t.Fatalf("alerts %v", e.alerts.Codes())
	}

	e = newEnv()
	e.open(t)
	e.placer.Script = []memory.PlaceStep{{Outcome: released(exchange.OutcomeRequote, "book-closed", "409 book-closed")}}
	if v, _ := e.confirm("sess-1", "tok_1"); v.State != cs.StatePlacing {
		t.Fatalf("setup %+v", v)
	}
	e.clock.Advance(22*time.Hour + 5*time.Minute)
	e.placer.Script = []memory.PlaceStep{{Outcome: released(exchange.OutcomeRequote, "book-closed", "409 book-closed")}}
	if _, err := resolver(e).Execute(bg, 10); err != nil {
		t.Fatal(err)
	}
	if v := e.get(t, "sess-1"); v.State != cs.StateRequoteRequired || len(e.payments.Voided) != 1 {
		t.Fatalf("view %+v voided %v", v, e.payments.Voided)
	}
}

// A refusal that is not released (FINAL or OUTCOME at customer-ordering, an
// unknown slug) ends the session at once, as before the rule.
func TestAFinalRefusalStillEndsTheSessionAtOnce(t *testing.T) {
	e := newEnv()
	e.open(t)
	e.placer.Script = []memory.PlaceStep{{Outcome: exchange.PlaceOutcome{Kind: exchange.OutcomeRejected, Slug: "line-without-promise", Detail: "422 line-without-promise"}}}
	v, err := e.confirm("sess-1", "tok_1")
	if err != nil || v.State != cs.StateFailed || v.FailureCode != "place-order-rejected" || len(e.placer.Calls) != 1 {
		t.Fatalf("view %+v err %v calls %d", v, err, len(e.placer.Calls))
	}
	if !equalStrings(e.alerts.Codes(), []string{"place-order-rejected"}) {
		t.Fatalf("alerts %v", e.alerts.Codes())
	}
}

// The shopper cannot re-submit a session whose refusal was released: it is
// PLACING, so ConfirmSession is refused (ErrPlacementInDoubt) and sends nothing,
// which means no request with a changed body ever reuses the freed key; only
// the resolver talks to customer-ordering and it replays the stored body byte
// for byte.
func TestTheShopperCannotRetryAReleasedRefusalWithAnotherBody(t *testing.T) {
	e := newEnv()
	e.open(t)
	e.placer.Script = []memory.PlaceStep{{Outcome: released(exchange.OutcomeRejected, "validation-failed", "422 validation-failed")}}
	if v, _ := e.confirm("sess-1", "tok_1"); v.State != cs.StatePlacing {
		t.Fatalf("setup %+v", v)
	}
	if _, err := e.confirm("sess-1", "tok_2"); !errors.Is(err, cs.ErrPlacementInDoubt) {
		t.Fatalf("err %v", err)
	}
	if len(e.placer.Calls) != 1 || len(e.payments.VoidKeys) != 0 {
		t.Fatalf("the refused confirm sent nothing: calls %d voids %v", len(e.placer.Calls), e.payments.VoidKeys)
	}
}

func resolver(e *env) usecases.ResolveInDoubtPlacements {
	return usecases.ResolveInDoubtPlacements{Deps: e.deps}
}
