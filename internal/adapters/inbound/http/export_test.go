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

// ReplayCatalogue lists every problem type of the adapter (the registry
// newProblem fills), one entry each.
func ReplayCatalogue() []ReplayEntry {
	out := make([]ReplayEntry, 0, len(registry))
	for _, p := range registry {
		out = append(out, ReplayEntry{Slug: p.slug, Status: p.status, Class: string(p.class)})
	}
	return out
}

// RefuseForTest answers the problem type slug through the production writer:
// through writeError (the catalogue's own mapping) when an error maps to it,
// through writeProblem for the problems the adapter writes itself.
func RefuseForTest(w http.ResponseWriter, r *http.Request, slug string) {
	for _, e := range problemCatalogue {
		if e.p.slug == slug {
			writeError(w, r, e.err)
			return
		}
	}
	for _, p := range registry {
		if p.slug == slug {
			writeProblem(w, r, p, "test")
			return
		}
	}
	panic("unknown problem type " + slug)
}
