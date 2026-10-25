package usecases_test

import (
	"context"
	"testing"
	"time"

	"github.com/IQVO/checkout/internal/adapters/outbound/memory"
	"github.com/IQVO/checkout/internal/application/exchange"
	"github.com/IQVO/checkout/internal/application/ports"
	cs "github.com/IQVO/checkout/internal/domain/checkoutsession"
)

func requoteOutcome() exchange.PlaceOutcome {
	return exchange.PlaceOutcome{Kind: exchange.OutcomeRequote, Offers: []cs.RequotedOffer{{
		OfferID: "offer-9", GroupNo: 1, Tier: cs.TierSameDay, DeliverByEarliest: "2026-10-26", DeliverByLatest: "2026-10-27", ExpiresAt: t0.Add(time.Hour),
	}}}
}

func TestPlaceDefinitive409VoidsAndAsksForRequote(t *testing.T) {
	e := newEnv()
	e.open(t)
	e.placer.Script = []memory.PlaceStep{{Outcome: requoteOutcome()}}
	v, err := e.confirm("sess-1", "tok_1")
	if err != nil || v.State != cs.StateRequoteRequired || len(v.RequotedOffers) != 1 || v.RequotedOffers[0].OfferID != "offer-9" {
		t.Fatalf("view %+v err %v", v, err)
	}
	if !equalStrings(e.payments.VoidKeys, []string{"sess-1:void"}) || !equalStrings(e.payments.Voided, []string{"auth-sess-1:auth:0"}) {
		t.Fatalf("void keys %v voided %v", e.payments.VoidKeys, e.payments.Voided)
	}
	if !equalStrings(e.eventNames(), []string{"CheckoutOpened", "CheckoutRequoteRequired"}) {
		t.Fatalf("events %v", e.eventNames())
	}
}

func TestPlace422IsAContractBugVoidedAndFailed(t *testing.T) {
	e := newEnv()
	e.open(t)
	e.placer.Script = []memory.PlaceStep{{Outcome: exchange.PlaceOutcome{Kind: exchange.OutcomeRejected, Detail: "422 line-without-promise"}}}
	v, err := e.confirm("sess-1", "tok_1")
	if err != nil || v.State != cs.StateFailed || v.FailureCode != "place-order-rejected" {
		t.Fatalf("view %+v err %v", v, err)
	}
	if len(e.payments.Voided) != 1 || !equalStrings(e.alerts.Codes(), []string{"place-order-rejected"}) {
		t.Fatalf("voided %v alerts %v", e.payments.Voided, e.alerts.Codes())
	}
}

func TestPlaceTimeoutKeepsPlacingAndNoVoid(t *testing.T) {
	e := newEnv()
	e.open(t)
	e.placer.Script = []memory.PlaceStep{{Err: ports.ErrOrderingUnavailable}}
	v, err := e.confirm("sess-1", "tok_1")
	if err != nil || v.State != cs.StatePlacing || !v.Unsettled() {
		t.Fatalf("view %+v err %v", v, err)
	}
	if len(e.payments.VoidKeys) != 0 {
		t.Fatal("PLACING is the semantic lock: no void")
	}
	if !equalStrings(e.eventNames(), []string{"CheckoutOpened", "CheckoutPlacementInDoubt"}) {
		t.Fatalf("events %v", e.eventNames())
	}
	if _, err := e.confirm("sess-1", "tok_2"); err == nil {
		t.Fatal("no new attempt while the outcome is unknown")
	}
}

func TestVoidFailureStaysOwedWithoutFailingTheAnswer(t *testing.T) {
	e := newEnv()
	e.open(t)
	e.payments.VoidErr = ports.ErrPaymentsUnavailable
	e.placer.Script = []memory.PlaceStep{{Outcome: requoteOutcome()}}
	v, err := e.confirm("sess-1", "tok_1")
	if err != nil || v.State != cs.StateRequoteRequired {
		t.Fatalf("view %+v err %v", v, err)
	}
	owed, _ := e.deps.Sessions.ListVoidOwed(context.Background(), 10)
	if len(owed) != 1 || owed[0].VoidOwed().AuthorizationID != "auth-sess-1:auth:0" {
		t.Fatalf("owed %v", owed)
	}
}
