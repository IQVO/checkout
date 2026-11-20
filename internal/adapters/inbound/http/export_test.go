package http

import "net/http"

// ProblemOf exposes the problem mapping to the external tests.
func ProblemOf(err error) (int, string) {
	p := problemFor(err)
	return p.status, p.slug
}

// ReplayEntry is one problem type as the replay-class tests read it.
type ReplayEntry struct {
	Slug   string
	Status int
	Class  string
}

// ReplayCatalogue lists every problem type of the adapter, once per slug.
func ReplayCatalogue() []ReplayEntry {
	var out []ReplayEntry
	seen := map[string]bool{}
	add := func(p problem) {
		if !seen[p.slug] {
			seen[p.slug] = true
			out = append(out, ReplayEntry{Slug: p.slug, Status: p.status})
		}
	}
	for _, e := range problemCatalogue {
		add(e.p)
	}
	for _, p := range []problem{internalProblem, idempotencyKeyRequired, idempotencyKeyInvalid, idempotencyKeyReused, notFoundResource, dependencyUnavailable} {
		add(p)
	}
	return out
}

// RefuseForTest answers the problem type slug through the production writer.
func RefuseForTest(w http.ResponseWriter, r *http.Request, slug string) {
	for _, e := range problemCatalogue {
		if e.p.slug == slug {
			writeError(w, r, e.err)
			return
		}
	}
	for _, p := range []problem{internalProblem, idempotencyKeyRequired, idempotencyKeyInvalid, idempotencyKeyReused, notFoundResource, dependencyUnavailable} {
		if p.slug == slug {
			writeProblem(w, r, p, "test")
			return
		}
	}
	panic("unknown problem type " + slug)
}
