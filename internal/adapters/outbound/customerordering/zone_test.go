package customerordering_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/IQVO/checkout/internal/adapters/outbound/customerordering"
	"github.com/IQVO/checkout/internal/application/exchange"
	cs "github.com/IQVO/checkout/internal/domain/checkoutsession"
)

// requoted is one requoted offer of a definitive 409; zone "" omits timeZone.
func requoted(offerID, earliest, latest, zone string) string {
	tz := ""
	if zone != "" {
		tz = fmt.Sprintf(`,"timeZone":%q`, zone)
	}
	return fmt.Sprintf(`{"groupNo":1,"offerId":%q,"tier":"STANDARD","deliverByEarliest":%q,"deliverByLatest":%q,"expiresAt":"2026-10-30T13:30:00Z"%s}`,
		offerID, earliest, latest, tz)
}

// requoteGateway answers every placement with a definitive 409 carrying offers
// and logs into the returned buffer.
func requoteGateway(t *testing.T, offers ...string) (*customerordering.Gateway, *bytes.Buffer) {
	t.Helper()
	body := `{"type":"/problems/offer-not-quotable","title":"x","status":409,"requotedOffers":[` + strings.Join(offers, ",") + `]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return customerordering.New(srv.URL, nil, customerordering.WithLogger(logger)), logs
}

func placeRequote(t *testing.T, g *customerordering.Gateway) []cs.RequotedOffer {
	t.Helper()
	out, err := g.Place(context.Background(), "s", []byte("{}"))
	if err != nil || out.Kind != exchange.OutcomeRequote {
		t.Fatalf("out %+v err %v", out, err)
	}
	return out.Offers
}

func TestRequotedDatesAreDaysOfTheDestinationsCalendar(t *testing.T) {
	for name, tc := range map[string]struct {
		earliest, latest, zone   string
		wantEarliest, wantLatest string
	}{
		// 09:00-21:00 on Saturday in Chicago (CDT, UTC-5): both bounds are Oct 31
		// there, while the UTC latest (02:00Z) is already Nov 1.
		"Chicago evening window is one local day": {"2026-10-31T14:00:00Z", "2026-11-01T02:00:00Z", "America/Chicago", "2026-10-31", "2026-10-31"},
		"missing zone reads as UTC":               {"2026-10-31T14:00:00Z", "2026-11-01T02:00:00Z", "", "2026-10-31", "2026-11-01"},
		"explicit UTC":                            {"2026-10-31T14:00:00Z", "2026-11-01T02:00:00Z", "UTC", "2026-10-31", "2026-11-01"},
		"east of UTC moves forward":               {"2026-10-30T16:00:00Z", "2026-10-31T02:00:00Z", "Asia/Tokyo", "2026-10-31", "2026-10-31"},
		"unknown zone falls back to UTC":          {"2026-10-31T14:00:00Z", "2026-11-01T02:00:00Z", "Mars/Olympus_Mons", "2026-10-31", "2026-11-01"},
		"Local is not the destination's zone":     {"2026-10-31T14:00:00Z", "2026-11-01T02:00:00Z", "Local", "2026-10-31", "2026-11-01"},
	} {
		t.Run(name, func(t *testing.T) {
			g, _ := requoteGateway(t, requoted("o1", tc.earliest, tc.latest, tc.zone))
			offers := placeRequote(t, g)
			if len(offers) != 1 {
				t.Fatalf("offers = %+v", offers)
			}
			if o := offers[0]; o.DeliverByEarliest != tc.wantEarliest || o.DeliverByLatest != tc.wantLatest {
				t.Fatalf("dates = %s..%s, want %s..%s", o.DeliverByEarliest, o.DeliverByLatest, tc.wantEarliest, tc.wantLatest)
			}
		})
	}
}

func TestEachRequotedOfferUsesItsOwnZone(t *testing.T) {
	g, _ := requoteGateway(t,
		requoted("chi", "2026-10-31T14:00:00Z", "2026-11-01T02:00:00Z", "America/Chicago"),
		requoted("utc", "2026-10-31T14:00:00Z", "2026-11-01T02:00:00Z", ""))
	offers := placeRequote(t, g)
	if len(offers) != 2 || offers[0].DeliverByLatest != "2026-10-31" || offers[1].DeliverByLatest != "2026-11-01" {
		t.Fatalf("offers = %+v", offers)
	}
}

func TestAnUnusableZoneIsWarnedOncePerName(t *testing.T) {
	g, logs := requoteGateway(t,
		requoted("a", "2026-10-31T14:00:00Z", "2026-11-01T02:00:00Z", "Mars/Olympus_Mons"),
		requoted("b", "2026-10-31T14:00:00Z", "2026-11-01T02:00:00Z", "Mars/Olympus_Mons"),
		requoted("c", "2026-10-31T14:00:00Z", "2026-11-01T02:00:00Z", ""),
		requoted("d", "2026-10-31T14:00:00Z", "2026-11-01T02:00:00Z", "America/Chicago"))
	placeRequote(t, g)
	placeRequote(t, g)
	out := logs.String()
	if n := strings.Count(out, "level=WARN"); n != 2 {
		t.Fatalf("want one WARN per unusable name (2), got %d:\n%s", n, out)
	}
	if !strings.Contains(out, "timeZone=Mars/Olympus_Mons") || strings.Contains(out, "America/Chicago") {
		t.Fatalf("logs:\n%s", out)
	}
}

func TestZoneCacheIsBounded(t *testing.T) {
	var offers []string
	for i := range 70 {
		offers = append(offers, requoted(fmt.Sprintf("o%d", i), "2026-10-31T14:00:00Z", "2026-11-01T02:00:00Z", fmt.Sprintf("Nowhere/Zone_%d", i)))
	}
	g, logs := requoteGateway(t, offers...)
	got := placeRequote(t, g)
	if len(got) != 70 || got[69].DeliverByLatest != "2026-11-01" {
		t.Fatalf("every offer still resolves (to UTC): %+v", got[69])
	}
	if n := strings.Count(logs.String(), "level=WARN"); n != 64 {
		t.Fatalf("warnings stop at the cache bound: %d", n)
	}
}
