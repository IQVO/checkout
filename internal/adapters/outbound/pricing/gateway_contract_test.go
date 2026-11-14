// Consumer contract tests against the byte-pinned producer spec.
// testdata/pricing.openapi.yaml is a byte copy of IQVO/pricing develop's
// apis/openapi.yaml; contracts/pinned.json and the pinned-contracts CI job
// keep it equal to the producer. R1 the route the gateway calls is declared,
// R2 the example fixtures satisfy the producer's schema, R3 the gateway's
// request satisfies the request schema and it decodes the answer into the
// domain, R4 every field the DTOs read is specified with a compatible type.
// The pricing spec carries no examples: the fixtures are derived from its
// schemas (producer example backfill pending).
package pricing

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
	doc, err := openapi3.NewLoader().LoadFromFile("testdata/pricing.openapi.yaml")
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
		{http.MethodPost, "/price-quotes"},
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

// R2: the example satisfies the pinned PriceQuote schema of the
// POST /price-quotes answer (required keys present, money shapes compatible).
func TestExamplesSatisfyPinnedSpec(t *testing.T) {
	var v any
	if err := json.Unmarshal(readExample(t, "quote.example.json"), &v); err != nil {
		t.Fatal(err)
	}
	if err := schema(t, loadPinned(t), "PriceQuote").VisitJSON(v); err != nil {
		t.Errorf("quote.example.json does not satisfy the pinned PriceQuote schema: %v", err)
	}
}

// quoteReq is one frozen session, the gateway's request input.
func quoteReq() exchange.QuoteRequest {
	return exchange.QuoteRequest{
		CartID: "cart-1", DestinationPostalPrefix: "787",
		Lines:  []cs.Line{{LineNo: 1, CatalogItemID: "sku-1", Quantity: 2}, {LineNo: 2, CatalogItemID: "sku-2", Quantity: 1}},
		Groups: []cs.Group{{GroupNo: 1, OfferID: "of-1", Tier: "NEXT_DAY", LineNos: []int{1, 2}}},
	}
}

// R3: the fixture flows through the REAL gateway call into the domain, and
// the request the gateway sends satisfies the pinned IssuePriceQuoteRequest
// schema (the producer would accept it).
func TestGatewayDecodesSpecExamples(t *testing.T) {
	doc := loadPinned(t)
	fixture := string(readExample(t, "quote.example.json"))
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/price-quotes" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		gotBody = readAll(t, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(fixture))
	}))
	defer srv.Close()

	q, err := New(srv.URL, srv.Client()).IssueQuote(t.Context(), "idem-1", quoteReq())
	if err != nil {
		t.Fatal(err)
	}
	checkQuoteRequestSatisfiesSpec(t, doc, gotBody)
	checkDecodedQuote(t, q)
}

func checkQuoteRequestSatisfiesSpec(t *testing.T, doc *openapi3.T, body []byte) {
	t.Helper()
	var sent any
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatalf("the gateway's request is not JSON: %v", err)
	}
	if err := schema(t, doc, "IssuePriceQuoteRequest").VisitJSON(sent); err != nil {
		t.Errorf("the request the gateway sends does not satisfy the pinned IssuePriceQuoteRequest schema: %v", err)
	}
}

func checkDecodedQuote(t *testing.T, q cs.PriceQuote) {
	t.Helper()
	if q.QuoteID != "pq-1" || q.Currency != "USD" || q.TaxMinor != 400 || q.TotalMinor != 6399 {
		t.Errorf("quote = %+v", q)
	}
	if len(q.Lines) != 2 || q.Lines[0].CatalogItemID != "sku-1" || q.Lines[0].LineTotalMinor != 3000 || q.Lines[1].LineTotalMinor != 2000 {
		t.Errorf("lines = %+v", q.Lines)
	}
	if len(q.Charges) != 1 || q.Charges[0].GroupNo != 1 || q.Charges[0].ChargeMinor != 999 {
		t.Errorf("charges = %+v", q.Charges)
	}
	if want := time.Date(2026, 10, 25, 13, 15, 0, 0, time.UTC); !q.ExpiresAt.Equal(want) {
		t.Errorf("expiresAt = %v, want %v", q.ExpiresAt, want)
	}
}

func readAll(t *testing.T, r *http.Request) []byte {
	t.Helper()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// R4: every wire field the response DTO reads is declared by the pinned
// PriceQuote schema with a compatible type, and every field the request DTO
// writes is declared by IssuePriceQuoteRequest.
func TestFieldsWeReadAreSpecified(t *testing.T) {
	doc := loadPinned(t)
	checkDTOFields(t, reflect.TypeOf(quoteResponse{}), schema(t, doc, "PriceQuote"), "PriceQuote", true)
	checkDTOFields(t, reflect.TypeOf(quoteRequest{}), schema(t, doc, "IssuePriceQuoteRequest"), "IssuePriceQuoteRequest", false)
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
