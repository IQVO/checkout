package bdd

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/cucumber/godog"
)

func registerWorldAssertions(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the events published are "([^"]*)"$`, func(list string) error {
		var got []string
		for _, ev := range w.store.Outbox().Events() {
			got = append(got, ev.EventName())
		}
		if strings.Join(got, ", ") != list {
			return fmt.Errorf("events = %v, want %s", got, list)
		}
		return nil
	})
	sc.Step(`^alert "([^"]*)" was raised$`, func(code string) error {
		for _, c := range w.alerts.Codes() {
			if c == code {
				return nil
			}
		}
		return fmt.Errorf("alerts = %v, want %s", w.alerts.Codes(), code)
	})
	sc.Step(`^pricing was not called$`, func() error {
		if len(w.quoter.Keys) != 0 {
			return fmt.Errorf("pricing was called %d times", len(w.quoter.Keys))
		}
		return nil
	})
	sc.Step(`^no session was stored$`, func() error { return w.sessionsStored(0) })
	sc.Step(`^(\d+) sessions are stored$`, w.sessionsStored)
	registerPaymentAssertions(sc, w)
	registerOrderingAssertions(sc, w)
}

func (w *world) sessionsStored(n int) error {
	if c := w.store.Counts(); c.Sessions != n || (n == 0 && c.OutboxEvents != 0) {
		return fmt.Errorf("counts = %+v, want %d sessions", c, n)
	}
	return nil
}

func registerPaymentAssertions(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the authorization keys were "([^"]*)"$`, func(list string) error {
		if got := strings.Join(w.payments.AuthKeys, ", "); got != list {
			return fmt.Errorf("authorization keys = %q, want %q", got, list)
		}
		return nil
	})
	sc.Step(`^no void was sent$`, func() error {
		if len(w.payments.VoidKeys) != 0 {
			return fmt.Errorf("void keys = %v, want none", w.payments.VoidKeys)
		}
		return nil
	})
	sc.Step(`^no authorization was voided$`, func() error {
		if len(w.payments.Voided) != 0 {
			return fmt.Errorf("voided = %v, want none", w.payments.Voided)
		}
		return nil
	})
	sc.Step(`^the authorization "([^"]*)" was voided with key "([^"]*)"$`, func(id, key string) error {
		if len(w.payments.Voided) == 0 || w.payments.Voided[len(w.payments.Voided)-1] != id {
			return fmt.Errorf("voided = %v, want %s", w.payments.Voided, id)
		}
		if w.payments.VoidKeys[len(w.payments.VoidKeys)-1] != key {
			return fmt.Errorf("void keys = %v, want %s", w.payments.VoidKeys, key)
		}
		return nil
	})
}

func registerOrderingAssertions(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^customer-ordering received (\d+) calls? with key "([^"]*)"$`, func(n int, key string) error {
		count := 0
		for _, c := range w.placer.Calls {
			if c.SessionID == key {
				count++
			}
		}
		if count != n {
			return fmt.Errorf("customer-ordering got %d calls with key %s, want %d", count, key, n)
		}
		return nil
	})
	sc.Step(`^customer-ordering received (\d+) calls$`, func(n int) error {
		if len(w.placer.Calls) != n {
			return fmt.Errorf("customer-ordering got %d calls, want %d", len(w.placer.Calls), n)
		}
		return nil
	})
	sc.Step(`^both placement calls carried the same body$`, func() error {
		c := w.placer.Calls
		if len(c) < 2 || !bytes.Equal(c[0].Body, c[1].Body) {
			return fmt.Errorf("placement bodies differ: %d calls", len(c))
		}
		return nil
	})
	sc.Step(`^the stored placement of the session is gone$`, func() error {
		s, err := w.deps.Sessions.Get(context.Background(), "sess-1")
		if err != nil {
			return err
		}
		if s.Placement() != nil {
			return fmt.Errorf("the placement is still stored")
		}
		return nil
	})
}
