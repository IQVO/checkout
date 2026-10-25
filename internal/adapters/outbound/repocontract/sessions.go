package repocontract

import (
	"reflect"
	"testing"

	"github.com/IQVO/checkout/internal/application/ports"
	cs "github.com/IQVO/checkout/internal/domain/checkoutsession"
)

func testSessions(t *testing.T, e Env) {
	_, err := e.Sessions.Get(bg(), "sess-nope")
	wantErr(t, err, ports.ErrNotFound)
	s := open(t, "sess-rt")
	save(t, e, s)
	if s.LoadedVersion() != s.Version() {
		t.Fatalf("Save must MarkPersisted: loaded = %d", s.LoadedVersion())
	}
	got, err := e.Sessions.Get(bg(), "sess-rt")
	must(t, err)
	if !reflect.DeepEqual(got.Snapshot(), s.Snapshot()) {
		t.Fatalf("round trip changed the session:\n got %+v\nwant %+v", got.Snapshot(), s.Snapshot())
	}
}

func testEveryState(t *testing.T, e Env) {
	declined := authorizing(t, "sess-declined")
	must(t, declined.RecordDecline("do-not-honor", at(3)))
	mismatch := authorizing(t, "sess-mismatch")
	wantErr(t, mismatch.RecordAuthorization("auth-m", 5, "USD", []byte("x"), at(3)), cs.ErrAuthorizationMismatch)
	inDoubt := placing(t, "sess-doubt")
	must(t, inDoubt.RecordPlacementInDoubt(at(3)))
	must(t, inDoubt.MarkInDoubtAlerted(at(4)))
	done := placing(t, "sess-done")
	must(t, done.RecordPlaced("order-1", at(3)))
	requote := placing(t, "sess-requote")
	must(t, requote.RecordRequoteRequired([]cs.RequotedOffer{{OfferID: "o9", GroupNo: 1, Tier: cs.TierSameDay,
		DeliverByEarliest: "2026-10-26", DeliverByLatest: "2026-10-27", ExpiresAt: at(90)}}, at(3)))
	failed := placing(t, "sess-failed")
	must(t, failed.RecordFailure("place-order-rejected", at(3)))
	expired := open(t, "sess-expired")
	must(t, expired.ExpireSession(at(20)))
	for _, s := range []*cs.CheckoutSession{open(t, "sess-open"), authorizing(t, "sess-auth"), declined, mismatch, inDoubt, done, requote, failed, expired} {
		save(t, e, s)
		got, err := e.Sessions.Get(bg(), s.ID())
		must(t, err)
		if !reflect.DeepEqual(got.Snapshot(), s.Snapshot()) {
			t.Fatalf("%s: round trip changed the session:\n got %+v\nwant %+v", s.ID(), got.Snapshot(), s.Snapshot())
		}
	}
}

func testConcurrency(t *testing.T, e Env) {
	save(t, e, open(t, "sess-cc"))
	wantErr(t, e.Sessions.Save(bg(), open(t, "sess-cc")), ports.ErrConcurrentUpdate) // a create expects a free id
	a, err := e.Sessions.Get(bg(), "sess-cc")
	must(t, err)
	b, err := e.Sessions.Get(bg(), "sess-cc")
	must(t, err)
	must(t, a.Confirm("tok_a", at(1)))
	save(t, e, a)
	must(t, b.ExpireSession(at(30)))
	wantErr(t, e.Sessions.Save(bg(), b), ports.ErrConcurrentUpdate) // b loaded a version that is gone
	must(t, a.RecordDecline("no", at(2)))
	save(t, e, a) // a refreshed LoadedVersion with MarkPersisted
	snap := open(t, "sess-ghost").Snapshot()
	snap.Version = 3
	ghost, err := cs.Restore(snap)
	must(t, err)
	wantErr(t, e.Sessions.Save(bg(), ghost), ports.ErrConcurrentUpdate) // loaded at a version that was never stored
}
