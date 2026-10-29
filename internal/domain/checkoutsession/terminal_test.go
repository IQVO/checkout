package checkoutsession_test

import (
	"testing"
	"time"

	cs "github.com/IQVO/checkout/internal/domain/checkoutsession"
	"github.com/IQVO/checkout/internal/domain/shared"
)

func TestRecordPlacedCompletes(t *testing.T) {
	s := placing(t)
	wantErr(t, s.RecordPlaced("bad id", t0), shared.ErrValidation)
	at := t0.Add(3 * time.Minute)
	wantNoErr(t, s.RecordPlaced("order-1", at))
	if s.State() != cs.StateCompleted || s.OrderID() != "order-1" || s.TerminalAt() != at {
		t.Fatalf("snapshot %+v", s.Snapshot())
	}
	wantErr(t, s.RecordPlaced("order-2", at), cs.ErrSessionClosed)
	if s.VoidOwed() != nil {
		t.Fatal("no void owed on success")
	}
}

func TestRecordPlacedEvent(t *testing.T) {
	s := placing(t)
	at := t0.Add(3 * time.Minute)
	wantNoErr(t, s.RecordPlaced("order-1", at))
	ev := s.PullEvents()
	e, ok := ev[0].(cs.CheckoutCompleted)
	if !ok || len(ev) != 1 || e.OrderID != "order-1" || e.AuthorizationID != "auth-1" || e.TotalMinor != 2100 ||
		e.ShopperID != "shopper-1" || e.CartID != "cart-1" || e.Currency != "USD" || len(e.Groups) != 2 ||
		e.Groups[0].AmountMinor != 1365 || e.OccurredAt() != at || e.AggregateVersion() != s.Version() || e.Entity() != "checkoutsession" {
		t.Fatalf("event %+v", ev)
	}
}

func TestRecordPlacedFromWrongState(t *testing.T) {
	wantErr(t, opened(t).RecordPlaced("order-1", t0), shared.ErrIllegalTransition)
	wantErr(t, authorizing(t).RecordPlaced("order-1", t0), shared.ErrIllegalTransition)
}

func TestRecordRequoteRequired(t *testing.T) {
	s := placing(t)
	bad := cs.RequotedOffer{OfferID: "o9", GroupNo: 1, Tier: cs.TierSameDay, DeliverByEarliest: "nope", DeliverByLatest: "2026-10-27", ExpiresAt: t0}
	wantErr(t, s.RecordRequoteRequired([]cs.RequotedOffer{bad}, t0), shared.ErrValidation)
	offer := cs.RequotedOffer{OfferID: "o9", GroupNo: 1, Tier: cs.TierSameDay, DeliverByEarliest: "2026-10-26", DeliverByLatest: "2026-10-27", ExpiresAt: t0.Add(time.Hour)}
	wantNoErr(t, s.RecordRequoteRequired([]cs.RequotedOffer{offer}, t0.Add(3*time.Minute)))
	if s.State() != cs.StateRequoteRequired || len(s.RequotedOffers()) != 1 || s.VoidOwed().AuthorizationID != "auth-1" {
		t.Fatalf("snapshot %+v", s.Snapshot())
	}
	ev := s.PullEvents()
	e := ev[0].(cs.CheckoutRequoteRequired)
	if len(ev) != 1 || len(e.RequotedOffers) != 1 || e.EventName() != "CheckoutRequoteRequired" {
		t.Fatalf("events %+v", ev)
	}
}

func TestRequotedOfferValidation(t *testing.T) {
	good := cs.RequotedOffer{OfferID: "o9", GroupNo: 1, Tier: cs.TierSameDay, DeliverByEarliest: "2026-10-26", DeliverByLatest: "2026-10-27", ExpiresAt: t0}
	mut := map[string]func(*cs.RequotedOffer){
		"id":     func(o *cs.RequotedOffer) { o.OfferID = "" },
		"group":  func(o *cs.RequotedOffer) { o.GroupNo = 0 },
		"tier":   func(o *cs.RequotedOffer) { o.Tier = "X" },
		"expiry": func(o *cs.RequotedOffer) { o.ExpiresAt = time.Time{} },
		"late":   func(o *cs.RequotedOffer) { o.DeliverByLatest = "27/10" },
	}
	for name, m := range mut {
		o := good
		m(&o)
		if err := placing(t).RecordRequoteRequired([]cs.RequotedOffer{o}, t0); !isErr(err, shared.ErrValidation) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestRecordFailure(t *testing.T) {
	s := placing(t)
	wantErr(t, s.RecordFailure("", t0), shared.ErrValidation)
	wantNoErr(t, s.RecordFailure("contract-bug", t0.Add(3*time.Minute)))
	if s.State() != cs.StateFailed || s.FailureCode() != "contract-bug" || s.VoidOwed() == nil {
		t.Fatalf("snapshot %+v", s.Snapshot())
	}
	ev := s.PullEvents()
	if len(ev) != 1 || ev[0].(cs.CheckoutFailed).Code != "contract-bug" {
		t.Fatalf("events %+v", ev)
	}
	wantErr(t, s.RecordFailure("x", t0), cs.ErrSessionClosed)
}

func TestExpireSession(t *testing.T) {
	s := opened(t)
	wantErr(t, s.ExpireSession(t0.Add(14*time.Minute)), shared.ErrValidation)
	wantNoErr(t, s.ExpireSession(t0.Add(15*time.Minute)))
	if s.State() != cs.StateExpired || s.TerminalAt().IsZero() {
		t.Fatalf("snapshot %+v", s.Snapshot())
	}
	ev := s.PullEvents()
	if len(ev) != 1 || ev[0].EventName() != "CheckoutSessionExpired" {
		t.Fatalf("events %+v", ev)
	}
	wantErr(t, s.ExpireSession(t0.Add(time.Hour)), cs.ErrSessionClosed)
}

func TestExpireFromDeclinedAndWrongStates(t *testing.T) {
	d := declined(t)
	wantNoErr(t, d.ExpireSession(t0.Add(time.Hour)))
	wantErr(t, authorizing(t).ExpireSession(t0.Add(time.Hour)), shared.ErrIllegalTransition)
	wantErr(t, placing(t).ExpireSession(t0.Add(time.Hour)), shared.ErrIllegalTransition)
}

func TestPurgePlacement(t *testing.T) {
	s := placing(t)
	wantErr(t, s.PurgePlacement(t0.Add(48*time.Hour)), shared.ErrIllegalTransition)
	at := t0.Add(3 * time.Minute)
	wantNoErr(t, s.RecordPlaced("order-1", at))
	wantErr(t, s.PurgePlacement(at.Add(cs.PurgePlacementAfter-time.Second)), shared.ErrIllegalTransition)
	wantNoErr(t, s.PurgePlacement(at.Add(cs.PurgePlacementAfter)))
	if s.Placement() != nil {
		t.Fatal("placement must be purged")
	}
	wantErr(t, s.PurgePlacement(at.Add(48*time.Hour)), shared.ErrIllegalTransition)
}
