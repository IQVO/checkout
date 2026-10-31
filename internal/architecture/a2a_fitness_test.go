package architecture

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"

	archgo "github.com/arch-go/arch-go/api"
	"github.com/arch-go/arch-go/api/configuration"
)

// TestA2AAdapterDependencyRule encodes ADR 0007: the A2A inbound adapter is
// additive, never load-bearing. It may depend only on the application and
// domain layers (never on outbound adapters, never on another inbound adapter,
// never on cmd), and nothing else in this codebase may depend on it; cmd/a2a
// wires it up and nothing else touches it.
func TestA2AAdapterDependencyRule(t *testing.T) {
	moduleInfo := configuration.Load(modulePath)

	t.Run("a2a adapter depends only on application and domain", func(t *testing.T) {
		result := archgo.CheckArchitecture(moduleInfo, configuration.Config{
			DependenciesRules: []*configuration.DependenciesRule{
				{
					Package: "**.internal.adapters.inbound.a2a.**",
					ShouldOnlyDependsOn: &configuration.Dependencies{
						Internal: []string{
							"**.internal.adapters.inbound.a2a.**",
							"**.internal.application.**",
							"**.internal.domain.**",
						},
					},
				},
			},
		})

		assertArchGoPasses(t, result)
	})

	t.Run("nothing else depends on the a2a adapter", func(t *testing.T) {
		result := archgo.CheckArchitecture(moduleInfo, configuration.Config{
			DependenciesRules: []*configuration.DependenciesRule{
				{
					Package: "**.internal.**",
					ShouldNotDependsOn: &configuration.Dependencies{
						Internal: []string{"**.internal.adapters.inbound.a2a.**"},
					},
				},
			},
		})

		assertArchGoPasses(t, result)
	})
}

// The A2A agent is READ-ONLY like the MCP charter (ADR 0005, ADR 0007): its one
// skill reads a checkout session. The sensor is an ALLOWLIST of what the
// non-test sources of the adapter and of cmd/a2a may reach, not a denylist of
// write names, so a new write use case, port or outbound adapter fails here
// until an ADR widens the charter on purpose.
var (
	// a2aUseCases are the only usecases.X identifiers allowed. Deps is the
	// bundle type NewDeps fills with the one read repository.
	a2aUseCases = map[string]bool{"GetSession": true, "Deps": true}
	// a2aPorts are the only ports.X identifiers allowed: the read repository
	// and the not-found sentinel. No UnitOfWork, EventPublisher or Outbox.
	a2aPorts = map[string]bool{"SessionRepository": true, "ErrNotFound": true}
	// a2aInternalImports are the only internal packages these sources may
	// import. No outbox, kafka or sibling-gateway adapter, no other inbound
	// adapter besides the request logger the router uses.
	a2aInternalImports = []string{
		"/internal/adapters/inbound/a2a",
		"/internal/adapters/inbound/requestlog",
		"/internal/adapters/outbound/postgres",
		"/internal/adapters/outbound/telemetry",
		"/internal/bootretry",
		"/internal/application/ports",
		"/internal/application/usecases",
		"/internal/application/views",
		"/internal/domain/",
	}
)

// a2aSourceViolations parses one non-test Go source (comments are dropped by
// the parser, so prose never trips the sensor) and returns every construct the
// read-only, no-outbound-client charter forbids.
func a2aSourceViolations(src string) []string {
	f, err := parser.ParseFile(token.NewFileSet(), "src.go", src, parser.SkipObjectResolution)
	if err != nil {
		return []string{"does not parse: " + err.Error()}
	}
	out := a2aImportViolations(f)
	out = append(out, a2aSelectorViolations(f, "usecases", a2aUseCases)...)
	out = append(out, a2aSelectorViolations(f, "ports", a2aPorts)...)
	return out
}

func a2aImportViolations(f *ast.File) []string {
	var out []string
	for _, spec := range f.Imports {
		path, _ := strconv.Unquote(spec.Path.Value)
		switch {
		case strings.Contains(path, "a2a-go/v2/a2aclient"):
			out = append(out, "imports "+path+": an outbound A2A client (ADR 0007)")
		case strings.Contains(path, "kafka"):
			out = append(out, "imports "+path+": a read-only agent never dials Kafka")
		case strings.Contains(path, "/internal/") && !a2aInternalImportAllowed(path):
			out = append(out, "imports "+path+": outside the read-only allowlist")
		}
	}
	return out
}

func a2aInternalImportAllowed(path string) bool {
	for _, allowed := range a2aInternalImports {
		if strings.HasSuffix(path, allowed) || (strings.HasSuffix(allowed, "/") && strings.Contains(path, allowed)) {
			return true
		}
	}
	return false
}

// a2aSelectorViolations flags every pkg.Name selector whose Name is not in the
// allowlist (usecases.GetSession, ports.SessionRepository, ...).
func a2aSelectorViolations(f *ast.File, pkg string, allowed map[string]bool) []string {
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); ok && id.Name == pkg && !allowed[sel.Sel.Name] {
			out = append(out, pkg+"."+sel.Sel.Name+": not on the read-only allowlist (the A2A charter mirrors the read-only MCP surface, ADR 0007)")
		}
		return true
	})
	return out
}

// TestA2AAdapterIsReadOnlyAndHasNoOutboundClient is the sensor behind the ADR
// 0007 boundaries: no write use case, no outbox access, no Kafka and no
// outbound A2A client in internal/adapters/inbound/a2a or cmd/a2a (test files
// excluded: the wire tests legitimately drive the agent with the SDK client).
func TestA2AAdapterIsReadOnlyAndHasNoOutboundClient(t *testing.T) {
	files := append(goFilesUnder(t, "../adapters/inbound/a2a", false), goFilesUnder(t, "../../cmd/a2a", false)...)
	if len(files) < 5 {
		t.Fatalf("found %d A2A sources; the sensor is looking in the wrong place", len(files))
	}
	for _, path := range files {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, v := range a2aSourceViolations(string(src)) {
			t.Errorf("%s %s", path, v)
		}
	}
}

// TestA2AReadOnlySensorBites proves the detector flags each forbidden shape in
// CODE (a comment mentioning it is ignored), so the sensor above cannot pass
// vacuously.
func TestA2AReadOnlySensorBites(t *testing.T) {
	const head = "package a2a\n"
	bad := map[string]string{
		"a write use case":       head + "var _ = usecases.OpenSession{}\n",
		"the confirm use case":   head + "var _ = usecases.ConfirmSession{}\n",
		"the unit of work port":  head + "var _ ports.UnitOfWork\n",
		"the publisher port":     head + "var _ ports.EventPublisher\n",
		"the outbox port":        head + "var _ ports.Outbox\n",
		"an outbound A2A client": head + "import \"github.com/a2aproject/a2a-go/v2/a2aclient\"\n",
		"a kafka adapter":        "package main\nimport _ \"github.com/IQVO/checkout/internal/adapters/outbound/kafka\"\n",
		"the outbox adapter":     "package main\nimport _ \"github.com/IQVO/checkout/internal/adapters/outbound/outbox\"\n",
		"a sibling gateway":      "package main\nimport _ \"github.com/IQVO/checkout/internal/adapters/outbound/pricing\"\n",
		"the REST adapter":       "package main\nimport _ \"github.com/IQVO/checkout/internal/adapters/inbound/http\"\n",
	}
	for name, src := range bad {
		if got := a2aSourceViolations(src); len(got) == 0 {
			t.Errorf("the sensor does not flag %s", name)
		}
	}
	good := head + "import _ \"github.com/IQVO/checkout/internal/adapters/outbound/postgres\"\n" +
		"// usecases.OpenSession and ports.UnitOfWork are only mentioned in prose\n" +
		"var _ = usecases.GetSession{Deps: usecases.Deps{}}\nvar _ ports.SessionRepository\nvar _ = ports.ErrNotFound\n"
	if got := a2aSourceViolations(good); len(got) != 0 {
		t.Errorf("false positive on the read path: %v", got)
	}
}
