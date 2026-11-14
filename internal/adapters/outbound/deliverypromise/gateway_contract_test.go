// Consumer contract tests against the byte-pinned producer spec.
// testdata/delivery-promise.openapi.yaml is a byte copy of IQVO/delivery-promise
// develop's apis/openapi.yaml; contracts/pinned.json and the pinned-contracts
// CI job keep it equal to the producer. R1 the route the gateway calls is
// declared, R2 the example fixtures satisfy the producer's schema, R3 the
// gateway decodes the producer's shape into the domain, R4 every field the
// DTOs read is specified with a compatible type.
package deliverypromise

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
)

func loadPinned(t *testing.T) *openapi3.T {
	t.Helper()
	doc, err := openapi3.NewLoader().LoadFromFile("testdata/delivery-promise.openapi.yaml")
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
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/delivery-offers/{offerId}"},
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

// R2: every testdata example satisfies the pinned DeliveryOffer schema of the
// GET /delivery-offers/{offerId} answer (required keys present, enums and
// patterns compatible). The fixtures are derived from the schema (the spec
// carries property examples only, no whole-response example for this route).
func TestExamplesSatisfyPinnedSpec(t *testing.T) {
	doc := loadPinned(t)
	ref := doc.Components.Schemas["DeliveryOffer"]
	if ref == nil || ref.Value == nil {
		t.Fatal("schema DeliveryOffer is not in the pinned spec copy")
	}
	for _, name := range []string{"offer.example.json", "offer-expired.example.json"} {
		var v any
		if err := json.Unmarshal(readExample(t, name), &v); err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if err := ref.Value.VisitJSON(v); err != nil {
			t.Errorf("%s does not satisfy the pinned DeliveryOffer schema: %v", name, err)
		}
	}
}

// R3: the fixtures flow through the REAL gateway call into the domain. A
// QUOTED offer is quotable; an EXPIRED one is not; the expiry instant survives.
func TestGatewayDecodesSpecExamples(t *testing.T) {
	for _, tc := range []struct {
		fixture string
		offerID string
		quoted  bool
		minute  int
	}{
		{"offer.example.json", "of-77", true, 30},
		{"offer-expired.example.json", "of-78", false, 20},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			fixture := string(readExample(t, tc.fixture))
			var gotPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(fixture))
			}))
			defer srv.Close()

			st, err := New(srv.URL, srv.Client()).GetOffer(t.Context(), tc.offerID)
			if err != nil {
				t.Fatal(err)
			}
			if gotPath != "/delivery-offers/"+tc.offerID {
				t.Errorf("gateway called %s", gotPath)
			}
			if st.OfferID != tc.offerID || st.Quoted != tc.quoted {
				t.Errorf("status = %+v, want offerID %s quoted=%v", st, tc.offerID, tc.quoted)
			}
			want := time.Date(2026, 10, 25, 13, tc.minute, 0, 0, time.UTC)
			if !st.ExpiresAt.Equal(want) {
				t.Errorf("expiresAt = %v, want %v", st.ExpiresAt, want)
			}
		})
	}
}

// R4: every wire field the offerDTO reads is declared by the pinned
// DeliveryOffer schema with a compatible type, and fields the schema does not
// require are ones the DTO tolerates missing.
func TestFieldsWeReadAreSpecified(t *testing.T) {
	doc := loadPinned(t)
	ref := doc.Components.Schemas["DeliveryOffer"]
	if ref == nil || ref.Value == nil {
		t.Fatal("schema DeliveryOffer is not in the pinned spec copy")
	}
	checkDTOFields(t, reflect.TypeOf(offerDTO{}), ref.Value, "DeliveryOffer")
}

var timeType = reflect.TypeOf(time.Time{})

func checkDTOFields(t *testing.T, dt reflect.Type, sch *openapi3.Schema, path string) {
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
		checkWireType(t, f.Type, p.Value, path+"."+name)
		if !required[name] && !toleratesAbsence(f) {
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

func checkWireType(t *testing.T, dt reflect.Type, sch *openapi3.Schema, path string) {
	t.Helper()
	if dt == timeType {
		if !sch.Type.Includes("string") || sch.Format != "date-time" {
			t.Errorf("%s: time.Time, but the pinned schema says %v/%q", path, sch.Type.Slice(), sch.Format)
		}
		return
	}
	switch dt.Kind() {
	case reflect.Pointer:
		checkWireType(t, dt.Elem(), sch, path)
	case reflect.Slice:
		checkArrayWireType(t, dt.Elem(), sch, path)
	case reflect.Struct:
		checkObjectWireType(t, dt, sch, path)
	default:
		if want, scalar := scalarWireTypes[dt.Kind()]; scalar {
			assertWireType(t, sch, path, want)
		} else {
			t.Errorf("%s: Go kind %s has no wire mapping", path, dt.Kind())
		}
	}
}

func checkArrayWireType(t *testing.T, elem reflect.Type, sch *openapi3.Schema, path string) {
	t.Helper()
	if assertWireType(t, sch, path, "array") && sch.Items != nil && sch.Items.Value != nil {
		checkWireType(t, elem, sch.Items.Value, path+"[]")
	}
}

func checkObjectWireType(t *testing.T, dt reflect.Type, sch *openapi3.Schema, path string) {
	t.Helper()
	if assertWireType(t, sch, path, "object") {
		checkDTOFields(t, dt, sch, path)
	}
}

func assertWireType(t *testing.T, sch *openapi3.Schema, path, want string) bool {
	if len(sch.Type.Slice()) > 0 && !sch.Type.Includes(want) {
		t.Errorf("%s: pinned schema type %v, the Go field is %s", path, sch.Type.Slice(), want)
		return false
	}
	return true
}
