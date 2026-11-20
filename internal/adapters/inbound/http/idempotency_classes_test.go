package http_test

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	httpadapter "github.com/IQVO/checkout/internal/adapters/inbound/http"
	"github.com/IQVO/checkout/internal/adapters/outbound/memory"
	"github.com/IQVO/checkout/internal/application/exchange"
	cs "github.com/IQVO/checkout/internal/domain/checkoutsession"
)

const adrPath = "../../../../docs/adr/0011-every-problem-type-has-a-replay-class.md"

// The ADR table is held equal to the catalogue: a problem type added, removed,
// reclassified or re-statused without the ADR fails here.
func TestTheADRDeclaresTheClassOfEveryProblemType(t *testing.T) {
	raw, err := os.ReadFile(adrPath)
	if err != nil {
		t.Fatalf("the ADR of the replay classes is missing: %v", err)
	}
	row := regexp.MustCompile("(?m)^\\| `([a-z-]+)` \\| (FINAL|OUTCOME|RETRYABLE) \\| (\\d{3}) \\|")
	got := map[string]string{}
	for _, m := range row.FindAllStringSubmatch(string(raw), -1) {
		got[m[1]] = m[2] + " " + m[3]
	}
	want := map[string]string{}
	for _, e := range httpadapter.ReplayCatalogue() {
		want[e.Slug] = e.Class + " " + strconv.Itoa(e.Status)
	}
	var diffs []string
	for slug, w := range want {
		if got[slug] != w {
			diffs = append(diffs, fmt.Sprintf("%s: the ADR says %q, the catalogue %q", slug, got[slug], w))
		}
	}
	for slug := range got {
		if _, ok := want[slug]; !ok {
			diffs = append(diffs, slug+": in the ADR, not in the catalogue")
		}
	}
	sort.Strings(diffs)
	if len(diffs) > 0 {
		t.Fatalf("ADR 0011 differs from the catalogue:\n%s", strings.Join(diffs, "\n"))
	}
}

// One slug is one class, whichever error reaches it.
func TestOneSlugHasOneClass(t *testing.T) {
	seen := map[string]string{}
	for _, e := range httpadapter.ReplayCatalogue() {
		if c, ok := seen[e.Slug]; ok {
			t.Errorf("%s is listed twice (%s and %s): a slug has one class", e.Slug, c, e.Class)
		}
		seen[e.Slug] = e.Class
	}
}

// OUTCOME is conservative: only a refusal the use case proves committed state.
func TestOnlyAuthorizationMismatchIsAnOutcome(t *testing.T) {
	var outcomes []string
	for _, e := range httpadapter.ReplayCatalogue() {
		if e.Class == "OUTCOME" {
			outcomes = append(outcomes, e.Slug)
		}
	}
	if len(outcomes) != 1 || outcomes[0] != "authorization-mismatch" {
		t.Fatalf("OUTCOME entries = %v, want exactly authorization-mismatch (usecases/confirm_session.go recordAuthorization saves it DECLINED before it answers)", outcomes)
	}
}

// problem literals live in the constructor only, so a problem cannot be built
// without a class (and cannot dodge the registry the tests above read).
func TestProblemsAreBuiltByTheConstructorOnly(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		tree, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range tree.Decls {
			fn, isFunc := decl.(*ast.FuncDecl)
			inConstructor := isFunc && fn.Name.Name == "newProblem"
			ast.Inspect(decl, func(n ast.Node) bool {
				lit, ok := n.(*ast.CompositeLit)
				if !ok {
					return true
				}
				if id, ok := lit.Type.(*ast.Ident); ok && id.Name == "problem" && !inConstructor {
					t.Errorf("%s: a problem literal outside newProblem: declare it with newProblem(status, slug, title, class)", fset.Position(lit.Pos()))
				}
				return true
			})
		}
	}
}

// A refusal that carries no class (a handler that wrote a 4xx by hand) is
// released like RETRYABLE: nothing stored, the retry runs.
func TestAnUnclassifiedRefusalIsReleased(t *testing.T) {
	a := newReplayAdapter(t)
	var calls int
	mw := a.Middleware(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"type":"/problems/made-up","status":409}`))
	}))
	first := post(t, mw, "k-unclassified", `{"n":1}`)
	second := post(t, mw, "k-unclassified", `{"n":1}`)
	if first.Code != http.StatusConflict || second.Code != http.StatusConflict || replayed(second) {
		t.Fatalf("answers = %d, %d replayed=%v", first.Code, second.Code, replayed(second))
	}
	if calls != 2 || a.KeyRows(t, "k-unclassified") != 0 {
		t.Fatalf("handler ran %d times, rows = %d, want 2 and 0: an unclassified refusal is released", calls, a.KeyRows(t, "k-unclassified"))
	}
}

// RETRYABLE through the real router, use cases and double store: the shopping
// cart is not checkout ready at first and is when the shopper retries.
func TestARetryableRefusalOfTheOpenRouteReleasesItsKey(t *testing.T) {
	e := newEnv(t)
	notReady := readyCart()
	notReady.Snapshot.Ready = false
	e.carts.Put(notReady)
	first := e.do(http.MethodPost, "/checkout-sessions", "open-k", openBody)
	e.wantProblem(first, http.StatusConflict, "cart-not-checkout-ready")
	if first.Header.Get("Idempotency-Replayed") != "" {
		t.Fatal("the first answer is never a replay")
	}
	e.carts.Put(readyCart())
	retry := e.do(http.MethodPost, "/checkout-sessions", "open-k", openBody)
	e.wantStatus(retry, http.StatusCreated)
	if retry.Header.Get("Idempotency-Replayed") != "" {
		t.Fatal("the retry of a released key executes: it is not a replay")
	}
	again := e.do(http.MethodPost, "/checkout-sessions", "open-k", openBody)
	if again.Header.Get("Idempotency-Replayed") != "true" || again.Body != retry.Body {
		t.Fatalf("the success is stored and replayed: %v %q", again.Header, again.Body)
	}
	if n := e.store.Counts().Sessions; n != 1 {
		t.Fatalf("%d sessions, want 1", n)
	}
}

// OUTCOME through the real router and use cases: the mismatching authorization
// is saved (DECLINED, void owed then voided) BEFORE the refusal is answered, the
// answer is stored, and a retry replays it without calling anyone again. The
// store is a separate write: "committed" here means the use case's own units of
// work, not the key row's transaction.
func TestTheAuthorizationMismatchOutcomeIsCommittedAndReplayed(t *testing.T) {
	e := newEnv(t)
	e.open()
	e.payments.Script = []memory.AuthStep{{Mutate: func(a *exchange.Authorization) { a.TotalMinor++ }}}
	const path = "/checkout-sessions/sess-1/confirmations"
	first := e.do(http.MethodPost, path, "c-mismatch", confirmBody)
	e.wantProblem(first, http.StatusConflict, "authorization-mismatch")

	s, err := e.store.Sessions().Get(context.Background(), "sess-1")
	if err != nil || s.State() != cs.StateDeclined {
		t.Fatalf("after the refusal the session is %v (%v), want DECLINED: the use case committed it", s.State(), err)
	}
	if len(e.payments.Voided) != 1 {
		t.Fatalf("voided = %v, want the mismatching authorization voided once", e.payments.Voided)
	}
	events, version := e.store.Counts().OutboxEvents, s.Version()

	replay := e.do(http.MethodPost, path, "c-mismatch", confirmBody)
	e.wantProblem(replay, http.StatusConflict, "authorization-mismatch")
	if replay.Header.Get("Idempotency-Replayed") != "true" || replay.Body != first.Body {
		t.Fatalf("replay = %v %q, want the stored answer", replay.Header, replay.Body)
	}
	if len(e.payments.AuthKeys) != 1 || len(e.payments.Voided) != 1 {
		t.Fatalf("a replay called customer-payments: authorize %v, voided %v", e.payments.AuthKeys, e.payments.Voided)
	}
	after, _ := e.store.Sessions().Get(context.Background(), "sess-1")
	if got := e.store.Counts().OutboxEvents; got != events || after.Version() != version {
		t.Fatalf("a replay recorded something new: events %d -> %d, version %d -> %d", events, got, version, after.Version())
	}
}

// A concurrent duplicate while the first ends RETRYABLE: the in-process key
// lock makes it wait; the first leaves nothing, so the duplicate runs the
// handler itself and the copies behind it replay ITS answer. The handler never
// runs twice at once. (Across replicas both execute and the first Put wins:
// that is the store's pre-existing behaviour, not covered here.)
func TestACopyWaitingOnARetryableFirstRunsItself(t *testing.T) {
	a := newReplayAdapter(t)
	refusal := firstOf(t, a, classRetryable)
	if refusal.Slug == "" {
		t.Fatal("the catalogue has no RETRYABLE entry below 500")
	}
	var calls, running, overlap atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if running.Add(1) > 1 {
			overlap.Add(1)
		}
		defer running.Add(-1)
		if calls.Add(1) == 1 {
			a.Refuse(w, r, refusal)
			return
		}
		succeeding(a, "ok")(w, r)
	})
	mw := a.Middleware(t, handler)
	const copies = 12
	codes := make([]int, copies)
	replays := make([]bool, copies)
	var wg sync.WaitGroup
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := post(t, mw, "k-burst", `{"n":1}`)
			codes[i], replays[i] = rec.Code, replayed(rec)
		}()
	}
	wg.Wait()
	if calls.Load() != 2 || overlap.Load() != 0 {
		t.Fatalf("handler ran %d times (%d overlapping), want 2 and 0: the refusal, then one execution", calls.Load(), overlap.Load())
	}
	var refused, created, replayedCount int
	for i, c := range codes {
		switch {
		case c == refusal.Status:
			refused++
		case c == http.StatusCreated && replays[i]:
			replayedCount++
		case c == http.StatusCreated:
			created++
		}
	}
	if refused != 1 || created != 1 || replayedCount != copies-2 {
		t.Fatalf("refused %d, created %d, replayed %d of %d, want 1, 1 and %d", refused, created, replayedCount, copies, copies-2)
	}
}
