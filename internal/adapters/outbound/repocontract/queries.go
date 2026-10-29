package repocontract

import (
	"fmt"
	"testing"

	cs "github.com/IQVO/checkout/internal/domain/checkoutsession"
)

func ids(list []*cs.CheckoutSession) []string {
	out := make([]string, len(list))
	for i, s := range list {
		out[i] = string(s.ID())
	}
	return out
}

func wantIDs(t *testing.T, got []*cs.CheckoutSession, want ...string) {
	t.Helper()
	g := ids(got)
	if fmt.Sprint(g) != fmt.Sprint(want) {
		t.Fatalf("ids = %v, want %v", g, want)
	}
}

func listStuck(t *testing.T, e Env) {
	old := authorizing(t, "sess-old") // authorizing since at(1)
	late := open(t, "sess-late")
	must(t, late.Confirm("tok", at(5)))
	save(t, e, old)
	save(t, e, late)
	save(t, e, open(t, "sess-open"))
	got, err := e.Sessions.ListStuckAuthorizing(bg(), at(1), 10)
	must(t, err)
	wantIDs(t, got, "sess-old")
	got, err = e.Sessions.ListStuckAuthorizing(bg(), at(5), 10)
	must(t, err)
	wantIDs(t, got, "sess-old", "sess-late")
}

func listPlacingDue(t *testing.T, e Env) {
	fresh := placing(t, "sess-fresh")
	waiting := placing(t, "sess-wait")
	must(t, waiting.RecordPlacementInDoubt(at(3))) // next attempt at(3)+30s
	save(t, e, fresh)
	save(t, e, waiting)
	save(t, e, authorizing(t, "sess-auth"))
	got, err := e.Sessions.ListPlacingDue(bg(), at(3), 10)
	must(t, err)
	wantIDs(t, got, "sess-fresh")
	got, err = e.Sessions.ListPlacingDue(bg(), at(4), 10)
	must(t, err)
	wantIDs(t, got, "sess-fresh", "sess-wait")
}

func listExpirable(t *testing.T, e Env) {
	save(t, e, open(t, "sess-open"))
	d := authorizing(t, "sess-declined")
	must(t, d.RecordDecline("no", at(2)))
	save(t, e, d)
	save(t, e, placing(t, "sess-placing"))
	got, err := e.Sessions.ListExpirable(bg(), at(14), 10)
	must(t, err)
	wantIDs(t, got)
	got, err = e.Sessions.ListExpirable(bg(), at(15), 10)
	must(t, err)
	wantIDs(t, got, "sess-declined", "sess-open")
}

func listVoidOwed(t *testing.T, e Env) {
	f := placing(t, "sess-failed")
	must(t, f.RecordFailure("code", at(3)))
	save(t, e, f)
	ok := placing(t, "sess-fine")
	must(t, ok.RecordPlaced("order-1", at(3)))
	save(t, e, ok)
	got, err := e.Sessions.ListVoidOwed(bg(), 10)
	must(t, err)
	wantIDs(t, got, "sess-failed")
	must(t, f.VoidCompleted(at(4)))
	save(t, e, f)
	got, err = e.Sessions.ListVoidOwed(bg(), 10)
	must(t, err)
	wantIDs(t, got)
}

func listPurgeable(t *testing.T, e Env) {
	done := placing(t, "sess-done")
	must(t, done.RecordPlaced("order-1", at(3)))
	save(t, e, done)
	save(t, e, placing(t, "sess-live"))
	got, err := e.Sessions.ListPurgeable(bg(), at(2), 10)
	must(t, err)
	wantIDs(t, got)
	got, err = e.Sessions.ListPurgeable(bg(), at(3), 10)
	must(t, err)
	wantIDs(t, got, "sess-done")
	must(t, got[0].PurgePlacement(at(3+24*60)))
	save(t, e, got[0])
	got, err = e.Sessions.ListPurgeable(bg(), at(3), 10)
	must(t, err)
	wantIDs(t, got)
}

func testLimitAndOrder(t *testing.T, e Env) {
	for i := 3; i >= 1; i-- {
		s := open(t, fmt.Sprintf("sess-%d", i))
		save(t, e, s)
	}
	got, err := e.Sessions.ListExpirable(bg(), at(60), 2)
	must(t, err)
	wantIDs(t, got, "sess-1", "sess-2")
}
