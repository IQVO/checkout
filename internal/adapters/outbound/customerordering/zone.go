package customerordering

import (
	"log/slog"
	"sync"
	"time"
)

// maxZones bounds how many distinct zone names one gateway remembers. The
// network serves a handful of station zones; past the bound a name is still
// resolved, only no longer cached (nor warned about), so a misbehaving
// provider cannot grow the gateway's memory without limit.
const maxZones = 64

// zones resolves the IANA time-zone name a requoted offer carries
// (`requotedOffers[].timeZone`, the zone of the delivery station that serves
// the destination) into a *time.Location, once per name. A missing name, the
// process-local "Local" zone, or a name the zone database does not know falls
// back to UTC (the dates checkout sent before the offer named its zone) and is
// logged at WARN the first time the name is seen (ADR 0008).
type zones struct {
	mu     sync.Mutex
	known  map[string]*time.Location
	logger *slog.Logger
}

func newZones(logger *slog.Logger) *zones {
	return &zones{known: map[string]*time.Location{}, logger: logger}
}

// location returns the zone named name, or UTC when it cannot be used.
func (z *zones) location(name string) *time.Location {
	z.mu.Lock()
	defer z.mu.Unlock()
	if loc, ok := z.known[name]; ok {
		return loc
	}
	loc, reason := resolve(name)
	if len(z.known) >= maxZones {
		return loc
	}
	if reason != "" {
		z.logger.Warn("requoted offer time zone unusable; delivery dates fall back to UTC",
			"timeZone", name, "reason", reason)
	}
	z.known[name] = loc
	return loc
}

// resolve loads name; a non-empty reason says why UTC is used instead.
func resolve(name string) (*time.Location, string) {
	switch name {
	case "":
		return time.UTC, "the requoted offer names no timeZone"
	case "Local":
		return time.UTC, "Local is the process zone, not the destination's"
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.UTC, err.Error()
	}
	return loc, ""
}
