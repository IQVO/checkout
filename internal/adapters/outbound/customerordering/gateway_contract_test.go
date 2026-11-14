// Consumer contract tests against the byte-pinned producer spec.
// testdata/customer-ordering.openapi.yaml is a byte copy of IQVO/customer-ordering
// develop's apis/openapi.yaml; contracts/pinned.json and the pinned-contracts
// CI job keep it equal to the producer. R1 the route the gateway calls is
// declared, R2 the example fixtures satisfy the producer's schemas, R3 the
// request the ACL builds satisfies the request schema, the gateway decodes the
// placed answer and the 409 requote (zone-aware dates), R4 every field the
// DTOs read is specified with a compatible type. The customer-ordering spec
// carries property examples only; the fixtures are derived from its schemas.
package customerordering

import (
	"encoding/json"
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
	cs "github.com/IQVO/checkout/internal/domain/checkoutsession"
)

func loadPinned(t *testing.T) *openapi3.T {
	t.Helper()
	doc, err := openapi3.NewLoader().LoadFromFile("testdata/customer-ordering.openapi.yaml")
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
		{http.MethodPost, "/checkout"},
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
// stands for: the 201 placed answer and the 409 problem that carries the
// re-quoted offers (an allOf of Problem).
func TestExamplesSatisfyPinnedSpec(t *testing.T) {
	doc := loadPinned(t)
	for name, sch := range map[string]string{
		"order.example.json":        "CustomerOrder",
		"conflict-409.example.json": "CheckoutConflictProblem",
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

func draft() exchange.PlacementDraft {
	return exchange.PlacementDraft{
		ShopperID: "shopper-1", ShipToAddressID: "addr-9",
		Lines:  []cs.Line{{LineNo: 1, CatalogItemID: "sku-1", Quantity: 2}, {LineNo: 2, CatalogItemID: "sku-2", Quantity: 1}},
		Groups: []cs.Group{{GroupNo: 1, OfferID: "of-1", Tier: "NEXT_DAY", LineNos: []int{1, 2}}},
	}
}

// R3 (request side): the body the ACL builds satisfies the pinned
// CheckoutRequest schema, so the producer accepts what checkout stores.
func TestBuiltRequestSatisfiesPinnedSpec(t *testing.T) {
	body, err := BuildRequest(draft())
	if err != nil {
		t.Fatal(err)
	}
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatal(err)
	}
	if err := schema(t, loadPinned(t), "CheckoutRequest").VisitJSON(v); err != nil {
		t.Errorf("BuildRequest output does not satisfy the pinned CheckoutRequest schema: %v", err)
	}
}

// servePlace answers POST /checkout with the fixture and captures the request.
func servePlace(t *testing.T, status int, fixture string, problem bool) (*Gateway, *[]byte, *string) {
	t.Helper()
	var body []byte
	var key string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		key = r.Header.Get("Idempotency-Key")
		ct := "application/json"
		if problem {
			ct = "application/problem+json"
		}
		w.Header().Set("Content-Type", ct)
		w.WriteHeader(status)
		w.Write([]byte(fixture))
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL, srv.Client()), &body, &key
}

// R3: the fixtures flow through the REAL gateway call. The 201 answer is a
// placed order; the definitive 409 is a requote whose dates are days of the
// destination's calendar (the first offer names America/Chicago, the second
// carries no zone and reads as UTC).
func TestGatewayDecodesSpecExamples(t *testing.T) {
	t.Run("placed", func(t *testing.T) {
		g, _, key := servePlace(t, http.StatusCreated, string(readExample(t, "order.example.json")), false)
		out, err := g.Place(t.Context(), "cs-1", []byte(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		if *key != "cs-1" {
			t.Errorf("Idempotency-Key = %q, want the session id", *key)
		}
		if out.Kind != exchange.OutcomePlaced || out.OrderID != "co-1" {
			t.Errorf("outcome = %+v", out)
		}
	})

	t.Run("requote", func(t *testing.T) {
		g, _, _ := servePlace(t, http.StatusConflict, string(readExample(t, "conflict-409.example.json")), true)
		out, err := g.Place(t.Context(), "cs-1", []byte(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		if out.Kind != exchange.OutcomeRequote || len(out.Offers) != 2 {
			t.Fatalf("outcome = %+v", out)
		}
		checkZonedRequotedOffer(t, out.Offers[0])
		checkZonelessRequotedOffer(t, out.Offers[1])
	})
}

func checkZonedRequotedOffer(t *testing.T, o cs.RequotedOffer) {
	t.Helper()
	if o.OfferID != "of-91" || o.GroupNo != 1 || o.Tier != cs.Tier("NEXT_DAY") {
		t.Errorf("first offer = %+v", o)
	}
	// 2026-10-26T12:00:00Z..20:00:00Z in America/Chicago is the day 2026-10-26.
	if o.DeliverByEarliest != "2026-10-26" || o.DeliverByLatest != "2026-10-26" {
		t.Errorf("first offer dates = %s..%s, want 2026-10-26..2026-10-26 (America/Chicago)", o.DeliverByEarliest, o.DeliverByLatest)
	}
	if want := time.Date(2026, 10, 25, 18, 30, 0, 0, time.UTC); !o.ExpiresAt.Equal(want) {
		t.Errorf("first offer expiresAt = %v, want %v", o.ExpiresAt, want)
	}
}

func checkZonelessRequotedOffer(t *testing.T, o cs.RequotedOffer) {
	t.Helper()
	if o.OfferID != "of-92" || o.DeliverByEarliest != "2026-10-28" || o.DeliverByLatest != "2026-10-29" {
		t.Errorf("second offer (no timeZone, UTC) = %+v", o)
	}
}

// R4: every wire field the response DTOs read is declared by the pinned
// schemas with a compatible type, and every field the request DTO writes is
// declared by CheckoutRequest.
func TestFieldsWeReadAreSpecified(t *testing.T) {
	doc := loadPinned(t)
	checkDTOFields(t, reflect.TypeOf(orderDTO{}), schema(t, doc, "CustomerOrder"), "CustomerOrder", true)
	checkDTOFields(t, reflect.TypeOf(conflictDTO{}), schema(t, doc, "CheckoutConflictProblem"), "CheckoutConflictProblem", true)
	checkDTOFields(t, reflect.TypeOf(offerDTO{}), schema(t, doc, "RequotedOffer"), "RequotedOffer", true)
	checkDTOFields(t, reflect.TypeOf(wireCheckout{}), schema(t, doc, "CheckoutRequest"), "CheckoutRequest", false)
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
