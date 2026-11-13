package bdd

import (
	"github.com/cucumber/godog"
)

// steps_wave2_test.go — the wave-2 additions: the raw GET step for the
// platform routes the domain journeys never touch, and the drain gate of
// /readyz.
func registerWave2(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^I GET "([^"]*)"$`, func(path string) error { return w.get(path) })
	sc.Step(`^the drain gate flips to not-ready$`, func() error {
		w.api() // ensure the router (and its readiness) exist
		w.readiness.SetNotReady()
		return nil
	})
}
