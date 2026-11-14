// Consumer contract tests against the byte-pinned producer spec.
// testdata/customer-payments.openapi.yaml is a byte copy of IQVO/customer-payments
// develop's apis/openapi.yaml; contracts/pinned.json and the pinned-contracts
// CI job keep it equal to the producer. R1 the routes the gateway calls are
// declared, R2 the example fixtures satisfy the producer's schemas, R3 the
// gateway's request satisfies the request schema, it decodes the answers into
// the domain and maps the problem+json answers it branches on, R4 every field
// the DTOs read is specified with a compatible type. The customer-payments
// spec carries no examples: the fixtures are derived from its schemas
// (producer example backfill pending).
package customerpayments

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/IQVO/checkout/internal/application/exchange"
	"github.com/IQVO/checkout/internal/application/ports"
)

func loadPinned(t *testing.T) *openapi3.T {
	t.Helper()
	doc, err := openapi3.NewLoader().LoadFromFile("testdata/customer-payments.openapi.yaml")
	if err != nil {
		t.Fatalf("load pinned spec: %v", err)
	}
	return doc
}

func readExample(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func schema(t *testing.T, doc *openapi3.T, name string) *openapi3.Schema {
	t.Helper()
	ref := doc.Components.Schemas[name]
	if ref == nil || ref.Value == nil {
		t.Fatalf("schema %s is not in the pinned spec copy", name)
	}
	return ref.Value
}

// R1: the pinned spec declares every (method, path) the gateway calls.
func TestPinnedSpecCoversEveryCallSite(t *testing.T) {
	doc := loadPinned(t)
	for _, c := range []struct{ method, path string }{
		{http.MethodPost, "/payment-authorizations"},
		{http.MethodPost, "/payment-authorizations/{authorizationId}/voids"},
	} {
		item := doc.Paths.Find(c.path)
		if item == nil {
			t.Errorf("pinned spec has no path %s, the gateway calls it", c.path)
			continue
		}
		if item.Operations()[c.method] == nil {
			t.Errorf("pinned spec path %s has no %s operation", c.path, c.method)
		}
	}
}

// R2: every testdata example satisfies the pinned schema of the message it
// stands for: the authorize answer (AUTHORIZED and DECLINED variants) and the
// problem+json bodies of the statuses the gateway branches on.
func TestExamplesSatisfyPinnedSpec(t *testing.T) {
	doc := loadPinned(t)
	for name, sch := range map[string]string{
		"authorization.example.json":          "PaymentAuthorization",
		"authorization-declined.example.json": "PaymentAuthorization",
		"decline-422.example.json":            "Problem",
		"void-conflict.example.json":          "Problem",
	} {
		var v any
		if err := json.Unmarshal(readExample(t, name), &v); err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if err := schema(t, doc, sch).VisitJSON(v); err != nil {
			t.Errorf("%s does not satisfy the pinned %s schema: %v", name, sch, err)
		}
	}
}

func authorizeReq() exchange.AuthorizeRequest {
	return exchange.AuthorizeRequest{
		SessionID: "cs-1", Token: "tok-7", Currency: "USD", TotalMinor: 6399,
		Allocations: []exchange.Allocation{{GroupNo: 1, AmountMinor: 6399}},
	}
}

// serveAuthorize answers authorize with the fixture and captures the request.
func serveAuthorize(t *testing.T, status int, fixture string, problem bool) (*Gateway, *[]byte) {
	t.Helper()
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		ct := "application/json"
		if problem {
			ct = "application/problem+json"
		}
		w.Header().Set("Content-Type", ct)
		w.WriteHeader(status)
		w.Write([]byte(fixture))
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL, srv.Client()), &body
}

// R3: the fixtures flow through the REAL gateway calls. The authorized and
// declined answers map to the domain; the request the gateway sends satisfies
// the pinned AuthorizeRequest schema; the 422 problem+json answer is the
// decline the gateway reports as a reason, not an error.
func TestGatewayDecodesSpecExamples(t *testing.T) {
	doc := loadPinned(t)

	t.Run("authorized", func(t *testing.T) {
		g, body := serveAuthorize(t, http.StatusCreated, string(readExample(t, "authorization.example.json")), false)
		a, err := g.Authorize(t.Context(), "cs-1:auth", authorizeReq())
		if err != nil {
			t.Fatal(err)
		}
		if !a.Authorized || a.ID != "auth-1" || a.Currency != "USD" || a.TotalMinor != 6399 {
			t.Errorf("authorization = %+v", a)
		}
		var sent any
		if err := json.Unmarshal(*body, &sent); err != nil {
			t.Fatal(err)
		}
		if err := schema(t, doc, "AuthorizeRequest").VisitJSON(sent); err != nil {
			t.Errorf("the request the gateway sends does not satisfy the pinned AuthorizeRequest schema: %v", err)
		}
	})

	t.Run("declined", func(t *testing.T) {
		g, _ := serveAuthorize(t, http.StatusCreated, string(readExample(t, "authorization-declined.example.json")), false)
		a, err := g.Authorize(t.Context(), "cs-1:auth", authorizeReq())
		if err != nil {
			t.Fatal(err)
		}
		if a.Authorized || a.ID != "auth-2" || a.DeclineReason != "payment-declined" {
			t.Errorf("authorization = %+v", a)
		}
	})

	t.Run("422 problem is a decline reason", func(t *testing.T) {
		g, _ := serveAuthorize(t, http.StatusUnprocessableEntity, string(readExample(t, "decline-422.example.json")), true)
		a, err := g.Authorize(t.Context(), "cs-1:auth", authorizeReq())
		if err != nil {
			t.Fatal(err)
		}
		if a.Authorized || a.DeclineReason != "authorization-rejected: allocation-mismatch" {
			t.Errorf("authorization = %+v", a)
		}
	})
}

// R3 (error mapping): a 200 or a tolerable 404/409 illegal-transition means
// the void is done; anything else is the payments-unavailable sentinel.
func TestVoidAnswerMapping(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		fixture string
		problem bool
	}{
		{"voided", http.StatusOK, "authorization.example.json", false},
		{"nothing to void", http.StatusNotFound, "void-conflict.example.json", true},
		{"already voided", http.StatusConflict, "void-conflict.example.json", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g, _ := serveAuthorize(t, tc.status, string(readExample(t, tc.fixture)), tc.problem)
			if err := g.Void(t.Context(), "cs-1:void", "auth-1"); err != nil {
				t.Errorf("void err = %v, want nil", err)
			}
		})
	}
	g, _ := serveAuthorize(t, http.StatusInternalServerError, `{}`, false)
	if err := g.Void(t.Context(), "cs-1:void", "auth-1"); !errors.Is(err, ports.ErrPaymentsUnavailable) {
		t.Errorf("void err = %v, want ports.ErrPaymentsUnavailable", err)
	}
}

// R4: every wire field the response DTO reads is declared by the pinned
// PaymentAuthorization schema, and every field the request DTO writes is
// declared by AuthorizeRequest (AllocationInput on the wire).
func TestFieldsWeReadAreSpecified(t *testing.T) {
	doc := loadPinned(t)
	checkDTOFields(t, reflect.TypeOf(authorization{}), schema(t, doc, "PaymentAuthorization"), "PaymentAuthorization", true)
	checkDTOFields(t, reflect.TypeOf(authorizeRequest{}), schema(t, doc, "AuthorizeRequest"), "AuthorizeRequest", false)
}

var timeType = reflect.TypeOf(time.Time{})

// checkDTOFields walks a DTO against a pinned schema. decode says the DTO
// READS the message (so optional fields must tolerate absence); a DTO that
// writes the message always sends its keys, and R3 checks what it sends.
func checkDTOFields(t *testing.T, dt reflect.Type, sch *openapi3.Schema, path string, decode bool) {
	t.Helper()
	props, required := flattenSchema(sch)
	for i := range dt.NumField() {
		f := dt.Field(i)
		tag := f.Tag.Get("json")
		name, _, _ := strings.Cut(tag, ",")
		if name == "" || name == "-" {
			continue // not a wire field
		}
		p := props[name]
		if p == nil || p.Value == nil {
			t.Errorf("%s.%s: json %q is not declared by the pinned schema", path, f.Name, name)
			continue
		}
		checkWireType(t, f.Type, p.Value, path+"."+name, decode)
		if decode && !required[name] && !toleratesAbsence(f) {
			t.Errorf("%s.%s: optional in the pinned schema, so the DTO must tolerate its absence (pointer, slice or omitempty)", path, name)
		}
	}
}

func flattenSchema(sch *openapi3.Schema) (map[string]*openapi3.SchemaRef, map[string]bool) {
	props := map[string]*openapi3.SchemaRef{}
	required := map[string]bool{}
	var walk func(*openapi3.Schema)
	walk = func(s *openapi3.Schema) {
		if s == nil {
			return
		}
		for n, p := range s.Properties {
			props[n] = p
		}
		for _, r := range s.Required {
			required[r] = true
		}
		for _, b := range s.AllOf {
			walk(b.Value)
		}
	}
	walk(sch)
	return props, required
}

func toleratesAbsence(f reflect.StructField) bool {
	_, _, has := strings.Cut(f.Tag.Get("json"), ",omitempty")
	return has || f.Type.Kind() == reflect.Pointer || f.Type.Kind() == reflect.Slice
}

// scalarWireTypes maps the scalar Go kinds a DTO may carry to the OpenAPI
// type that must declare them.
var scalarWireTypes = map[reflect.Kind]string{
	reflect.String: "string", reflect.Bool: "boolean",
	reflect.Int: "integer", reflect.Int8: "integer", reflect.Int16: "integer", reflect.Int32: "integer", reflect.Int64: "integer",
	reflect.Uint: "integer", reflect.Uint8: "integer", reflect.Uint16: "integer", reflect.Uint32: "integer", reflect.Uint64: "integer",
	reflect.Float32: "number", reflect.Float64: "number",
}

func checkWireType(t *testing.T, dt reflect.Type, sch *openapi3.Schema, path string, decode bool) {
	t.Helper()
	if dt == timeType {
		if !sch.Type.Includes("string") || sch.Format != "date-time" {
			t.Errorf("%s: time.Time, but the pinned schema says %v/%q", path, sch.Type.Slice(), sch.Format)
		}
		return
	}
	switch dt.Kind() {
	case reflect.Pointer:
		checkWireType(t, dt.Elem(), sch, path, decode)
	case reflect.Slice:
		checkArrayWireType(t, dt.Elem(), sch, path, decode)
	case reflect.Struct:
		checkObjectWireType(t, dt, sch, path, decode)
	default:
		if want, scalar := scalarWireTypes[dt.Kind()]; scalar {
			assertWireType(t, sch, path, want)
		} else {
			t.Errorf("%s: Go kind %s has no wire mapping", path, dt.Kind())
		}
	}
}

func checkArrayWireType(t *testing.T, elem reflect.Type, sch *openapi3.Schema, path string, decode bool) {
	t.Helper()
	if assertWireType(t, sch, path, "array") && sch.Items != nil && sch.Items.Value != nil {
		checkWireType(t, elem, sch.Items.Value, path+"[]", decode)
	}
}

func checkObjectWireType(t *testing.T, dt reflect.Type, sch *openapi3.Schema, path string, decode bool) {
	t.Helper()
	if assertWireType(t, sch, path, "object") {
		checkDTOFields(t, dt, sch, path, decode)
	}
}

func assertWireType(t *testing.T, sch *openapi3.Schema, path, want string) bool {
	if len(sch.Type.Slice()) > 0 && !sch.Type.Includes(want) {
		t.Errorf("%s: pinned schema type %v, the Go field is %s", path, sch.Type.Slice(), want)
		return false
	}
	return true
}
