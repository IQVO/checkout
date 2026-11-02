package bdd

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/cucumber/godog"
)

// jsonField reads a top-level string field of a JSON object.
func jsonField(body, name string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		return ""
	}
	s, _ := m[name].(string)
	return s
}

// lookup follows a dotted path ("groups.0.amountMinor") through the last answer.
func (w *world) lookup(path string) (any, bool) {
	var cur any
	if err := json.Unmarshal([]byte(w.body), &cur); err != nil {
		return nil, false
	}
	for _, part := range strings.Split(path, ".") {
		switch node := cur.(type) {
		case map[string]any:
			v, ok := node[part]
			if !ok {
				return nil, false
			}
			cur = v
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(node) {
				return nil, false
			}
			cur = node[i]
		default:
			return nil, false
		}
	}
	return cur, cur != nil
}

func show(v any) string {
	if f, ok := v.(float64); ok {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return fmt.Sprint(v)
}

func (w *world) fieldIs(path, want string) error {
	v, ok := w.lookup(path)
	if !ok || show(v) != want {
		return fmt.Errorf("field %s = %v (present %v), want %s; body %s", path, v, ok, want, w.body)
	}
	return nil
}

func registerAssertions(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the API answers (\d+)$`, func(code int) error {
		if w.status != code {
			return fmt.Errorf("status = %d, want %d: %s", w.status, code, w.body)
		}
		return nil
	})
	sc.Step(`^the problem type is "([^"]*)"$`, func(slug string) error {
		if got := jsonField(w.body, "type"); got != "/problems/"+slug {
			return fmt.Errorf("problem type = %q, want /problems/%s (%d %s)", got, slug, w.status, w.body)
		}
		return nil
	})
	sc.Step(`^the response field "([^"]*)" holds null$`, func(path string) error {
		var m map[string]any
		if err := json.Unmarshal([]byte(w.body), &m); err != nil {
			return err
		}
		if v, present := m[path]; !present || v != nil {
			return fmt.Errorf("field %s = %v (present %v), want null", path, v, present)
		}
		return nil
	})
	sc.Step(`^the response field "([^"]*)" is (\S+)$`, w.fieldIs)
	sc.Step(`^the answer is a replay$`, func() error {
		if w.header.Get("Idempotency-Replayed") != "true" {
			return fmt.Errorf("the answer is not a replay: %v", w.header)
		}
		return nil
	})
}
