package shoppingcart_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/IQVO/checkout/internal/adapters/outbound/shoppingcart"
	"github.com/IQVO/checkout/internal/application/ports"
	cs "github.com/IQVO/checkout/internal/domain/checkoutsession"
)

const cartJSON = `{"cartId":"cart-1","state":"OPEN","lines":[{"catalogItemId":"A","quantity":2},{"catalogItemId":"B","quantity":1}],
"destinationPostalPrefix":"941","deliveryQuote":{"groups":[
 {"groupNo":1,"lineNos":[1],"offers":[{"offerId":"o1","tier":"NEXT_DAY"},{"offerId":"o2","tier":"STANDARD"}]},
 {"groupNo":2,"lineNos":[2],"offers":[{"offerId":"o3","tier":"STANDARD"}]}]},
"selections":[{"groupNo":1,"offerId":"o1"},{"groupNo":2,"offerId":"o3"}],
"checkoutReadiness":{"ready":true,"allGroupsSelected":true,"noOfferExpired":true},"version":9}`

func serve(t *testing.T, status int, body string) (*shoppingcart.Gateway, *string) {
	t.Helper()
	path := new(string)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*path = r.Method + " " + r.URL.EscapedPath()
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return shoppingcart.New(srv.URL, nil), path
}

func TestGetCartMapsTheSelectedGroups(t *testing.T) {
	g, path := serve(t, 200, cartJSON)
	c, err := g.GetCart(context.Background(), "cart-1")
	if err != nil {
		t.Fatal(err)
	}
	if *path != "GET /carts/cart-1" || c.DestinationPostalPrefix != "941" || !c.Snapshot.Ready || c.Snapshot.Version != 9 {
		t.Fatalf("path %q cart %+v", *path, c)
	}
	want := []cs.CartGroup{{GroupNo: 1, OfferID: "o1", Tier: cs.TierNextDay, LineNos: []int{1}}, {GroupNo: 2, OfferID: "o3", Tier: cs.TierStandard, LineNos: []int{2}}}
	if len(c.Snapshot.Groups) != 2 || c.Snapshot.Groups[0].OfferID != want[0].OfferID || c.Snapshot.Groups[0].Tier != want[0].Tier || c.Snapshot.Groups[1].Tier != want[1].Tier {
		t.Fatalf("groups %+v", c.Snapshot.Groups)
	}
	if len(c.Snapshot.Lines) != 2 || c.Snapshot.Lines[1].CatalogItemID != "B" {
		t.Fatalf("lines %+v", c.Snapshot.Lines)
	}
}

func TestGetCartIsNotReadyWhenClosedOrUnselected(t *testing.T) {
	g, _ := serve(t, 200, `{"cartId":"c","state":"CHECKED_OUT","checkoutReadiness":{"ready":true},"version":1}`)
	c, err := g.GetCart(context.Background(), "c")
	if err != nil || c.Snapshot.Ready || len(c.Snapshot.Groups) != 0 {
		t.Fatalf("cart %+v err %v", c, err)
	}
	g, _ = serve(t, 200, `{"cartId":"c","state":"OPEN","deliveryQuote":{"groups":[{"groupNo":1,"lineNos":[1],"offers":[{"offerId":"o","tier":"STANDARD"}]}]},"checkoutReadiness":{"ready":false},"version":1}`)
	c, _ = g.GetCart(context.Background(), "c")
	if c.Snapshot.Ready || c.Snapshot.Groups[0].OfferID != "" || c.Snapshot.Groups[0].Tier != "" {
		t.Fatalf("cart %+v", c)
	}
}

func TestGetCartFailures(t *testing.T) {
	g, _ := serve(t, 404, `{}`)
	if _, err := g.GetCart(context.Background(), "x"); !errors.Is(err, ports.ErrCartNotFound) {
		t.Fatalf("404: %v", err)
	}
	g, _ = serve(t, 503, ``)
	if _, err := g.GetCart(context.Background(), "x"); !errors.Is(err, ports.ErrShoppingCartUnavailable) {
		t.Fatalf("503: %v", err)
	}
	g, _ = serve(t, 200, `not json`)
	if _, err := g.GetCart(context.Background(), "x"); !errors.Is(err, ports.ErrShoppingCartUnavailable) {
		t.Fatalf("undecodable: %v", err)
	}
	dead := shoppingcart.New("http://127.0.0.1:1", nil)
	if _, err := dead.GetCart(context.Background(), "x"); !errors.Is(err, ports.ErrShoppingCartUnavailable) {
		t.Fatalf("transport: %v", err)
	}
}
