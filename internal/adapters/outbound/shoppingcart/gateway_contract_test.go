// Consumer contract tests against the byte-pinned producer spec.
// testdata/shopping-cart.openapi.yaml is a byte copy of IQVO/shopping-cart
// develop's apis/openapi.yaml; contracts/pinned.json and the pinned-contracts
// CI job keep it equal to the producer. The copy buys four guarantees:
// R1 the route the gateway calls is declared, R2 the example fixtures satisfy
// the producer's schema, R3 the gateway decodes the producer's shape into the
// domain, R4 every field the DTOs read is specified with a compatible type.
package shoppingcart

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/IQVO/checkout/internal/application/ports"
	cs "github.com/IQVO/checkout/internal/domain/checkoutsession"
)

func loadPinned(t *testing.T) *openapi3.T {
	t.Helper()
	doc, err := openapi3.NewLoader().LoadFromFile("testdata/shopping-cart.openapi.yaml")
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

// R1: the pinned spec declares every (method, path) the gateway calls.
func TestPinnedSpecCoversEveryCallSite(t *testing.T) {
	doc := loadPinned(t)
	calls := []struct{ method, path string }{
		{http.MethodGet, "/carts/{cartId}"},
	}
	for _, c := range calls {
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
// stands for (required keys present, types compatible).
func TestExamplesSatisfyPinnedSpec(t *testing.T) {
	doc := loadPinned(t)
	for name, schema := range map[string]string{
		"cart.example.json": "Cart",
	} {
		var v any
		if err := json.Unmarshal(readExample(t, name), &v); err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		ref := doc.Components.Schemas[schema]
		if ref == nil || ref.Value == nil {
			t.Errorf("%s: schema %s is not in the pinned spec copy", name, schema)
			continue
		}
		if err := ref.Value.VisitJSON(v); err != nil {
			t.Errorf("%s does not satisfy the pinned %s schema: %v", name, schema, err)
		}
	}
}

// R3: the fixture flows through the REAL gateway call into the domain.
func TestGatewayDecodesSpecExamples(t *testing.T) {
	fixture := string(readExample(t, "cart.example.json"))
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(fixture))
	}))
	defer srv.Close()

	cart, err := New(srv.URL, srv.Client()).GetCart(t.Context(), "cart-1")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/carts/cart-1" {
		t.Errorf("gateway called %s, want /carts/cart-1", gotPath)
	}
	checkCartSnapshot(t, cart.Snapshot)
	if cart.DestinationPostalPrefix != "787" {
		t.Errorf("destinationPostalPrefix = %q", cart.DestinationPostalPrefix)
	}
}

func checkCartSnapshot(t *testing.T, s cs.CartSnapshot) {
	t.Helper()
	if s.CartID != "cart-1" || s.Version != 7 || !s.Ready {
		t.Errorf("snapshot = %+v", s)
	}
	if len(s.Lines) != 2 || s.Lines[0].CatalogItemID != "sku-1" || s.Lines[0].Quantity != 2 || s.Lines[1].Quantity != 1 {
		t.Errorf("lines = %+v", s.Lines)
	}
	if len(s.Groups) != 1 {
		t.Fatalf("groups = %+v", s.Groups)
	}
	checkCartGroup(t, s.Groups[0])
}

func checkCartGroup(t *testing.T, g cs.CartGroup) {
	t.Helper()
	if g.GroupNo != 1 || g.OfferID != "of-1" || g.Tier != cs.Tier("NEXT_DAY") || len(g.LineNos) != 2 {
		t.Errorf("group = %+v (tier comes from the selected offer of-1)", g)
	}
}

// R3 (error mapping): a 404 answer is the cart-not-found sentinel.
func TestGatewayMapsNotFoundToErrCartNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	if _, err := New(srv.URL, srv.Client()).GetCart(t.Context(), "nope"); !errors.Is(err, ports.ErrCartNotFound) {
		t.Errorf("err = %v, want ports.ErrCartNotFound", err)
	}
}

// R4: every wire field the DTOs read is declared by the pinned schema with a
// compatible type, and fields the schema does NOT require are ones the DTO
// tolerates missing (pointer, slice or omitempty).
func TestFieldsWeReadAreSpecified(t *testing.T) {
	doc := loadPinned(t)
	for dt, schema := range map[reflect.Type]string{
		reflect.TypeOf(cartDTO{}): "Cart",
	} {
		ref := doc.Components.Schemas[schema]
		if ref == nil || ref.Value == nil {
			t.Errorf("schema %s is not in the pinned spec copy", schema)
			continue
		}
		checkDTOFields(t, dt, ref.Value, schema, true)
	}
}

var timeType = reflect.TypeOf(time.Time{})

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
