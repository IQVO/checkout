package http

// ProblemOf exposes the problem mapping to the external tests.
func ProblemOf(err error) (int, string) {
	p := problemFor(err)
	return p.status, p.slug
}
