package checkoutsession_test

import (
	"testing"
	"time"

	cs "github.com/IQVO/checkout/internal/domain/checkoutsession"
	"github.com/IQVO/checkout/internal/domain/shared"
)

func TestInDoubtBackoffAndSingleEvent(t *testing.T) {
	s := placing(t)
	base := t0.Add(2 * time.Minute)
	want := []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 15 * time.Minute, 15 * time.Minute}
	for i, d := range want {
		now := base.Add(time.Duration(i) * time.Hour)
		wantNoErr(t, s.RecordPlacementInDoubt(now))
		if s.NextAttemptAt() != now.Add(d) || s.PlacementAttempts() != i+1 {
			t.Fatalf("attempt %d: next = %v, tries = %d", i+1, s.NextAttemptAt(), s.PlacementAttempts())
		}
		ev := s.PullEvents()
		if (i == 0) != (len(ev) == 1) {
			t.Fatalf("attempt %d: events = %+v", i+1, ev)
		}
		if i == 0 && ev[0].(cs.CheckoutPlacementInDoubt).PlacingSince != base {
			t.Fatalf("placingSince = %v", ev[0])
		}
	}
	if s.State() != cs.StatePlacing {
		t.Fatal("PLACING stays PLACING")
	}
}

func TestInDoubtOnlyFromPlacing(t *testing.T) {
	wantErr(t, opened(t).RecordPlacementInDoubt(t0), shared.ErrIllegalTransition)
}

func TestResolutionDue(t *testing.T) {
	s := placing(t)
	now := t0.Add(3 * time.Minute)
	if !s.ResolutionDue(now) {
		t.Fatal("fresh PLACING is due")
	}
	wantNoErr(t, s.RecordPlacementInDoubt(now))
	if s.ResolutionDue(now.Add(29 * time.Second)) {
		t.Fatal("not due inside the backoff")
	}
	if !s.ResolutionDue(now.Add(30 * time.Second)) {
		t.Fatal("due once the backoff elapsed")
	}
	if opened(t).ResolutionDue(now) {
		t.Fatal("only PLACING is resolvable")
	}
}

func TestAlertAndGiveUpTimeline(t *testing.T) {
	s := placing(t)
	since := t0.Add(2 * time.Minute)
	if s.AlertDue(since.Add(cs.InDoubtAlertAfter-time.Second)) || s.GiveUpDue(since.Add(cs.InDoubtGiveUpAfter-time.Second)) {
		t.Fatal("too early")
	}
	if !s.AlertDue(since.Add(cs.InDoubtAlertAfter)) || s.GiveUpDue(since.Add(cs.InDoubtAlertAfter)) {
		t.Fatal("alert at 20 h, not yet give-up")
	}
	if !s.GiveUpDue(since.Add(cs.InDoubtGiveUpAfter)) {
		t.Fatal("give up at 22 h")
	}
	wantNoErr(t, s.MarkInDoubtAlerted(since.Add(cs.InDoubtAlertAfter)))
	if s.AlertDue(since.Add(21*time.Hour)) || s.AlertedAt().IsZero() {
		t.Fatal("the alert fires once")
	}
	wantErr(t, opened(t).MarkInDoubtAlerted(t0), shared.ErrIllegalTransition)
	if opened(t).InDoubtFor(t0.Add(48*time.Hour)) != 0 || opened(t).AlertDue(t0.Add(48*time.Hour)) {
		t.Fatal("only PLACING is in doubt")
	}
}

func TestAuthorizationStuckAfterTwoMinutes(t *testing.T) {
	s := authorizing(t)
	since := s.AuthorizingSince()
	if s.AuthorizationStuck(since.Add(cs.StuckAuthorizationAfter - time.Second)) {
		t.Fatal("not stuck yet")
	}
	if !s.AuthorizationStuck(since.Add(cs.StuckAuthorizationAfter)) {
		t.Fatal("stuck at 2 min")
	}
	if opened(t).AuthorizationStuck(t0.Add(time.Hour)) {
		t.Fatal("only AUTHORIZING can be stuck")
	}
}

func TestQuoteExpiredBoundary(t *testing.T) {
	s := opened(t)
	if s.QuoteExpired(t0.Add(15*time.Minute-time.Second)) || !s.QuoteExpired(t0.Add(15*time.Minute)) {
		t.Fatal("expiry boundary")
	}
}
