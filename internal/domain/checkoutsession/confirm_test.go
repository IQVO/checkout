package checkoutsession_test

import (
	"testing"
	"time"

	cs "github.com/IQVO/checkout/internal/domain/checkoutsession"
	"github.com/IQVO/checkout/internal/domain/shared"
)

func TestConfirmMovesOpenToAuthorizing(t *testing.T) {
	s := opened(t)
	wantNoErr(t, s.Confirm("tok_1", t0.Add(time.Minute)))
	if s.State() != cs.StateAuthorizing || s.PendingToken() != "tok_1" || s.AuthAttempt() != 0 || s.Version() != 2 {
		t.Fatalf("snapshot %+v", s.Snapshot())
	}
	if s.AuthorizingSince() != t0.Add(time.Minute) {
		t.Fatalf("since = %v", s.AuthorizingSince())
	}
	if len(s.PullEvents()) != 0 {
		t.Fatal("ConfirmSession is eventless")
	}
}

func TestConfirmAfterDeclineBumpsAttempt(t *testing.T) {
	s := declined(t)
	wantNoErr(t, s.Confirm("tok_2", t0.Add(3*time.Minute)))
	if s.State() != cs.StateAuthorizing || s.AuthAttempt() != 1 || s.DeclineReason() != "" {
		t.Fatalf("snapshot %+v", s.Snapshot())
	}
}

func TestConfirmGuards(t *testing.T) {
	wantErr(t, placing(t).Confirm("tok", t0.Add(3*time.Minute)), cs.ErrPlacementInDoubt)
	wantErr(t, authorizing(t).Confirm("tok", t0.Add(3*time.Minute)), shared.ErrIllegalTransition)
	s := opened(t)
	wantErr(t, s.Confirm("", t0.Add(time.Minute)), shared.ErrValidation)
	wantErr(t, s.Confirm(" tok", t0.Add(time.Minute)), shared.ErrValidation)
	wantErr(t, s.Confirm("tok", t0.Add(15*time.Minute)), cs.ErrPriceQuoteExpired)
	wantNoErr(t, s.Confirm("tok", t0.Add(15*time.Minute-time.Second)))
}

func TestConfirmClosedStates(t *testing.T) {
	for name, s := range closedSessions(t) {
		if err := s.Confirm("tok", t0.Add(time.Minute)); !isErr(err, cs.ErrSessionClosed) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestConfirmRefusedWhileVoidOwed(t *testing.T) {
	s := authorizing(t)
	err := s.RecordAuthorization("auth-9", 1, "USD", []byte("x"), t0.Add(2*time.Minute))
	wantErr(t, err, cs.ErrAuthorizationMismatch)
	if s.State() != cs.StateDeclined || s.VoidOwed() == nil || s.VoidOwed().AuthorizationID != "auth-9" {
		t.Fatalf("snapshot %+v", s.Snapshot())
	}
	wantErr(t, s.Confirm("tok", t0.Add(3*time.Minute)), cs.ErrVoidPending)
	wantNoErr(t, s.VoidCompleted(t0.Add(4*time.Minute)))
	wantNoErr(t, s.Confirm("tok", t0.Add(5*time.Minute)))
	wantErr(t, s.VoidCompleted(t0), shared.ErrIllegalTransition)
}

func TestMismatchOnCurrencyAlsoVoids(t *testing.T) {
	s := authorizing(t)
	err := s.RecordAuthorization("auth-9", 2100, "EUR", []byte("x"), t0.Add(2*time.Minute))
	wantErr(t, err, cs.ErrAuthorizationMismatch)
	ev := s.PullEvents()
	if len(ev) != 1 || ev[0].EventName() != "CheckoutDeclined" {
		t.Fatalf("events %+v", ev)
	}
}

func TestRecordAuthorizationPlaces(t *testing.T) {
	s := authorizing(t)
	wantNoErr(t, s.RecordAuthorization("auth-1", 2100, "USD", []byte(`{"a":1}`), t0.Add(2*time.Minute)))
	p := s.Placement()
	if s.State() != cs.StatePlacing || s.AuthorizationID() != "auth-1" || s.PendingToken() != "" || !s.AuthorizingSince().IsZero() {
		t.Fatalf("snapshot %+v", s.Snapshot())
	}
	if string(p.RequestBody) != `{"a":1}` || len(p.RequestHash) != 64 || p.PlacingSince != t0.Add(2*time.Minute) {
		t.Fatalf("placement %+v", p)
	}
	p.RequestBody[0] = 'X'
	if s.Placement().RequestBody[0] != '{' {
		t.Fatal("Placement must return a copy")
	}
	if len(s.PullEvents()) != 0 {
		t.Fatal("RecordAuthorization is eventless")
	}
}

func TestRecordAuthorizationGuards(t *testing.T) {
	s := authorizing(t)
	wantErr(t, s.RecordAuthorization("bad id", 2100, "USD", []byte("x"), t0), shared.ErrValidation)
	wantErr(t, s.RecordAuthorization("auth-1", 2100, "USD", nil, t0), shared.ErrValidation)
	wantErr(t, opened(t).RecordAuthorization("auth-1", 2100, "USD", []byte("x"), t0), shared.ErrIllegalTransition)
}

func TestRecordDecline(t *testing.T) {
	s := authorizing(t)
	wantErr(t, s.RecordDecline("", t0), shared.ErrValidation)
	wantNoErr(t, s.RecordDecline("do-not-honor", t0.Add(2*time.Minute)))
	if s.State() != cs.StateDeclined || s.DeclineReason() != "do-not-honor" || s.PendingToken() != "" {
		t.Fatalf("snapshot %+v", s.Snapshot())
	}
	ev := s.PullEvents()
	if len(ev) != 1 || ev[0].EventName() != "CheckoutDeclined" {
		t.Fatalf("events %+v", ev)
	}
	wantErr(t, s.RecordDecline("x", t0), shared.ErrIllegalTransition)
}

func closedSessions(t *testing.T) map[string]*cs.CheckoutSession {
	t.Helper()
	done := placing(t)
	wantNoErr(t, done.RecordPlaced("order-1", t0.Add(3*time.Minute)))
	rq := placing(t)
	wantNoErr(t, rq.RecordRequoteRequired(nil, t0.Add(3*time.Minute)))
	failed := placing(t)
	wantNoErr(t, failed.RecordFailure("contract-bug", t0.Add(3*time.Minute)))
	exp := opened(t)
	wantNoErr(t, exp.ExpireSession(t0.Add(time.Hour)))
	return map[string]*cs.CheckoutSession{"completed": done, "requote": rq, "failed": failed, "expired": exp}
}
