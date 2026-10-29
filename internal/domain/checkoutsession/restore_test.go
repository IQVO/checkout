package checkoutsession_test

import (
	"reflect"
	"testing"
	"time"

	cs "github.com/IQVO/checkout/internal/domain/checkoutsession"
	"github.com/IQVO/checkout/internal/domain/shared"
)

func TestSnapshotRestoreRoundTripsEveryState(t *testing.T) {
	sessions := closedSessions(t)
	sessions["open"] = opened(t)
	sessions["authorizing"] = authorizing(t)
	sessions["declined"] = declined(t)
	sessions["placing"] = placing(t)
	for name, s := range sessions {
		sn := s.Snapshot()
		r, err := cs.Restore(sn)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !reflect.DeepEqual(sn, r.Snapshot()) || r.LoadedVersion() != sn.Version {
			t.Fatalf("%s: round trip lost data\n%+v\n%+v", name, sn, r.Snapshot())
		}
	}
}

func TestMarkPersistedSetsLoadedVersion(t *testing.T) {
	s := opened(t)
	if s.LoadedVersion() != 0 {
		t.Fatal("new aggregates are unloaded")
	}
	s.MarkPersisted()
	if s.LoadedVersion() != 1 {
		t.Fatal("MarkPersisted")
	}
}

func TestRestoreRejectsCorruptSnapshots(t *testing.T) {
	cases := map[string]func(*cs.Snapshot){
		"id":          func(s *cs.Snapshot) { s.ID = "" },
		"cart":        func(s *cs.Snapshot) { s.CartID = "" },
		"shopper":     func(s *cs.Snapshot) { s.ShopperID = "" },
		"address":     func(s *cs.Snapshot) { s.ShipToAddressID = "" },
		"state":       func(s *cs.Snapshot) { s.State = "NOPE" },
		"version":     func(s *cs.Snapshot) { s.Version = 0 },
		"cart ver":    func(s *cs.Snapshot) { s.CartVersion = 0 },
		"created":     func(s *cs.Snapshot) { s.CreatedAt = time.Time{} },
		"updated":     func(s *cs.Snapshot) { s.UpdatedAt = time.Time{} },
		"expiry":      func(s *cs.Snapshot) { s.QuoteExpiresAt = time.Time{} },
		"lines":       func(s *cs.Snapshot) { s.Lines = nil },
		"groups":      func(s *cs.Snapshot) { s.Groups = nil },
		"total":       func(s *cs.Snapshot) { s.TotalMinor = -1 },
		"currency":    func(s *cs.Snapshot) { s.Currency = "US" },
		"attempt":     func(s *cs.Snapshot) { s.AuthAttempt = -1 },
		"auth token":  func(s *cs.Snapshot) { s.State = cs.StateAuthorizing; s.PendingToken = "" },
		"auth since":  func(s *cs.Snapshot) { s.State = cs.StateAuthorizing; s.PendingToken = "t" },
		"placing id":  func(s *cs.Snapshot) { s.State = cs.StatePlacing; s.AuthorizationID = "" },
		"placing req": func(s *cs.Snapshot) { s.State = cs.StatePlacing; s.AuthorizationID = "a" },
		"completed":   func(s *cs.Snapshot) { s.State = cs.StateCompleted; s.AuthorizationID = "a"; s.TerminalAt = t0 },
		"terminal":    func(s *cs.Snapshot) { s.State = cs.StateExpired },
	}
	for name, mut := range cases {
		sn := opened(t).Snapshot()
		mut(&sn)
		if _, err := cs.Restore(sn); !isErr(err, shared.ErrInvalidSnapshot) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
}

func TestStateAndTierPredicates(t *testing.T) {
	all := []cs.State{cs.StateOpen, cs.StateAuthorizing, cs.StateDeclined, cs.StatePlacing, cs.StateRequoteRequired, cs.StateCompleted, cs.StateExpired, cs.StateFailed}
	closed := 0
	for _, s := range all {
		if !s.Valid() {
			t.Fatalf("%s must be valid", s)
		}
		if s.Closed() {
			closed++
		}
	}
	if closed != 4 || cs.State("X").Valid() {
		t.Fatalf("closed = %d", closed)
	}
	for _, tier := range []cs.Tier{cs.TierSameDay, cs.TierNextDay, cs.TierTwoDay, cs.TierStandard} {
		if !tier.Valid() {
			t.Fatalf("%s", tier)
		}
	}
	if cs.Tier("WEEKLY").Valid() {
		t.Fatal("unknown tier")
	}
}
