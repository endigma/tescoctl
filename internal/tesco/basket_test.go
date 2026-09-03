package tesco

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// basketResponse builds an UpdateBasket reply carrying the given lines, in the
// shape the mutation selects.
func basketResponse(lines string) string {
	return `[{"data":{"basket":{"id":"o1","guidePrice":12.5,"items":[` + lines + `]}}}]`
}

// basketLine renders one basket line for basketResponse.
func basketLine(tpnc, quantity string) string {
	return `{"id":"` + tpnc + `","quantity":` + quantity + `,"unit":"pcs","product":{"tpnc":"` + tpnc + `","title":"Thing"}}`
}

// TestSilentNoOpRejected is the bug this check exists for: Tesco accepts the
// mutation for some products, answers with no error, and returns the basket
// unchanged. Reporting success there loses the write silently.
func TestSilentNoOpRejected(t *testing.T) {
	c := newTestClient(t, routed(t, map[string]string{
		"UpdateBasket": basketResponse(basketLine("281085590", "1")),
		// The lookup that would explain why cannot answer; the write still has
		// to be reported as failed.
		"GetProduct": `[{"errors":[{"message":"product-not-found"}],"data":{"product":null}}]`,
	}))

	_, err := c.SetQuantity(context.Background(), "259541778", 1, "o1")
	var notUpdated *BasketNotUpdatedError
	if !errors.As(err, &notUpdated) {
		t.Fatalf("want BasketNotUpdatedError, got %v", err)
	}
	if notUpdated.Present {
		t.Error("the line never came back, so Present should be false")
	}
}

// TestWrongQuantityRejected covers the half-applied write: the line is there,
// but not at the quantity that was asked for.
func TestWrongQuantityRejected(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, basketResponse(basketLine("259541778", "1")))
	})

	_, err := c.SetQuantity(context.Background(), "259541778", 3, "o1")
	var notUpdated *BasketNotUpdatedError
	if !errors.As(err, &notUpdated) {
		t.Fatalf("want BasketNotUpdatedError, got %v", err)
	}
	if notUpdated.Got == nil || *notUpdated.Got != 1 {
		t.Errorf("Got = %v, want the quantity that came back", notUpdated.Got)
	}
}

// TestAppliedWriteAccepted checks the confirmation does not reject a write that
// did land.
func TestAppliedWriteAccepted(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, basketResponse(basketLine("281085590", "1")+","+basketLine("259541778", "2")))
	})

	basket, err := c.SetQuantity(context.Background(), "259541778", 2, "o1")
	if err != nil {
		t.Fatalf("SetQuantity: %v", err)
	}
	if len(basket.Items) != 2 {
		t.Errorf("want the returned basket passed through, got %d items", len(basket.Items))
	}
}

// TestRemovalConfirmed checks the zero case both ways: a line gone is success,
// a line still there is not.
func TestRemovalConfirmed(t *testing.T) {
	gone := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, basketResponse(basketLine("281085590", "1")))
	})
	if _, err := gone.SetQuantity(context.Background(), "259541778", 0, "o1"); err != nil {
		t.Fatalf("removing a line that came back absent: %v", err)
	}

	stays := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, basketResponse(basketLine("259541778", "1")))
	})
	_, err := stays.SetQuantity(context.Background(), "259541778", 0, "o1")
	var notUpdated *BasketNotUpdatedError
	if !errors.As(err, &notUpdated) {
		t.Fatalf("want BasketNotUpdatedError for a line that survived removal, got %v", err)
	}
}

// TestFractionalQuantityNotSent is the important half of the fraction guard:
// the write must be refused before it reaches Tesco, because Tesco accepts it
// and the resulting line breaks tesco.com's basket page for the whole basket.
func TestFractionalQuantityNotSent(t *testing.T) {
	called := false
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
		io.WriteString(w, basketResponse(basketLine("259541778", "1.5")))
	})

	_, err := c.SetQuantity(context.Background(), "259541778", 1.5, "o1")
	var fractional *FractionalQuantityError
	if !errors.As(err, &fractional) {
		t.Fatalf("want FractionalQuantityError, got %v", err)
	}
	if called {
		t.Error("a fractional quantity must not reach tesco at all")
	}
}

// TestFractionalPieces pins the invariant that actually holds: fractions are
// legitimate for weighed goods, and only wrong against a whole-unit line.
func TestFractionalPieces(t *testing.T) {
	for _, tc := range []struct {
		quantity float64
		unit     string
		want     bool
	}{
		{1.5, "pcs", true},
		{1.5, "PCS", true},
		{2, "pcs", false},
		{0.1, "kg", false},
		{0.1, "", false},
	} {
		if got := FractionalPieces(tc.quantity, tc.unit); got != tc.want {
			t.Errorf("FractionalPieces(%v, %q) = %v, want %v", tc.quantity, tc.unit, got, tc.want)
		}
	}
}

// productResponse builds a GetProduct reply with a price/unit-price pair.
func productResponse(tpnc, title string, price, unitPrice float64, uom string) string {
	return fmt.Sprintf(`[{"data":{"product":{"tpnc":%q,"title":%q,"isForSale":true,`+
		`"price":{"actual":%v,"unitPrice":%v,"unitOfMeasure":%q}}}}]`, tpnc, title, price, unitPrice, uom)
}

// recording wraps a handler, noting every operation name it is asked for, so a
// test can assert that a request was never made at all.
func recording(h http.HandlerFunc, seen *[]string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var ops []struct {
			OperationName string `json:"operationName"`
		}
		if json.Unmarshal(body, &ops) == nil {
			for _, o := range ops {
				*seen = append(*seen, o.OperationName)
			}
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		h(w, r)
	}
}

// routed answers each operation by name, so a single client can serve the
// mutation and the product lookup that follows it.
func routed(t *testing.T, replies map[string]string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		var ops []struct {
			OperationName string `json:"operationName"`
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &ops); err != nil || len(ops) != 1 {
			t.Errorf("unexpected request body %s: %v", body, err)
			return
		}
		reply, ok := replies[ops[0].OperationName]
		if !ok {
			t.Errorf("unexpected operation %q", ops[0].OperationName)
			return
		}
		io.WriteString(w, reply)
	}
}

// TestCatchweightDiagnosed covers the class behind the silent no-op: a
// variable-weight product cannot be written in pieces, so the gateway drops the
// write. The user should be told that, not left with "tesco gave no reason".
func TestCatchweightDiagnosed(t *testing.T) {
	c := newTestClient(t, routed(t, map[string]string{
		"UpdateBasket": basketResponse(basketLine("281085590", "1")),
		// A joint priced per kilo: the shelf price is the unit price.
		"GetProduct": productResponse("259541778", "Tesco Large Pork Shoulder Joint", 5.00, 5.00, "kg"),
	}))

	_, err := c.SetQuantity(context.Background(), "259541778", 1, "o1")
	var catchweight *CatchweightError
	if !errors.As(err, &catchweight) {
		t.Fatalf("want CatchweightError, got %v", err)
	}
	// The diagnosis must not lose the fact that the write did not land.
	var notUpdated *BasketNotUpdatedError
	if !errors.As(err, &notUpdated) {
		t.Error("a CatchweightError should still unwrap to BasketNotUpdatedError")
	}
}

// TestFixedWeightNotDiagnosedAsCatchweight guards the detector against the
// false positive that would make it useless: unitOfMeasure "kg" is on nearly
// every product as the basis of its comparison price.
func TestFixedWeightNotDiagnosedAsCatchweight(t *testing.T) {
	c := newTestClient(t, routed(t, map[string]string{
		"UpdateBasket": basketResponse(basketLine("281085590", "1")),
		// Nutella 350g: £2.99 on the shelf, £8.54/kg for comparison.
		"GetProduct": productResponse("254656543", "Nutella 350G", 2.99, 8.54, "kg"),
	}))

	_, err := c.SetQuantity(context.Background(), "254656543", 1, "o1")
	var catchweight *CatchweightError
	if errors.As(err, &catchweight) {
		t.Fatalf("a fixed-weight pack was diagnosed as catchweight: %v", err)
	}
	var notUpdated *BasketNotUpdatedError
	if !errors.As(err, &notUpdated) {
		t.Fatalf("want the generic BasketNotUpdatedError, got %v", err)
	}
}

// TestCatchweight pins the shape the detector keys on.
func TestCatchweight(t *testing.T) {
	price := func(actual, unit float64, uom string) *Price {
		return &Price{Actual: &actual, UnitPrice: &unit, UnitOfMeasure: uom}
	}
	for name, tc := range map[string]struct {
		price *Price
		want  bool
	}{
		"joint sold per kilo":     {price(5.00, 5.00, "kg"), true},
		"fixed-weight pack":       {price(6.15, 8.79, "kg"), false},
		"same price, litre basis": {price(0.73, 0.73, "litre"), false},
		"no price at all":         {nil, false},
		"no unit price":           {&Price{UnitOfMeasure: "kg"}, false},
	} {
		if got := (Product{Price: tc.price}).Catchweight(); got != tc.want {
			t.Errorf("%s: Catchweight() = %v, want %v", name, got, tc.want)
		}
	}
}

// catchweightProductResponse builds a GetProduct reply for a variable-weight
// product, carrying the weights Tesco offers it in.
func catchweightProductResponse(tpnc, title string, weights ...float64) string {
	opts := make([]string, 0, len(weights))
	for i, w := range weights {
		opts = append(opts, fmt.Sprintf(`{"weight":%v,"price":%v,"default":%t}`, w, w*5, i == 0))
	}
	return fmt.Sprintf(`[{"data":{"product":{"tpnc":%q,"title":%q,"isForSale":true,`+
		`"price":{"actual":5,"unitPrice":5,"unitOfMeasure":"kg"},`+
		`"catchWeightList":[%s]}}}]`, tpnc, title, strings.Join(opts, ","))
}

// catchweightLine renders a basket line as Tesco returns one for a weighed
// product: the weight sits in quantity and the unit still reads "pcs".
func catchweightLine(tpnc, weight string) string {
	return `{"id":"` + tpnc + `","quantity":` + weight + `,"unit":"pcs","product":{"tpnc":"` + tpnc + `","title":"Joint"}}`
}

// TestWeightWriteLands is the happy path the whole catchweight machinery exists
// for: a listed weight is written in kilos and confirmed against the response.
func TestWeightWriteLands(t *testing.T) {
	c := newTestClient(t, routed(t, map[string]string{
		"GetProduct":   catchweightProductResponse("259541778", "Tesco Large Pork Shoulder Joint", 1.8, 1.95, 2.1),
		"UpdateBasket": basketResponse(catchweightLine("259541778", "1.8")),
	}))

	basket, err := c.SetWeight(context.Background(), "259541778", 1.8, "o1")
	if err != nil {
		t.Fatalf("a listed weight should write cleanly: %v", err)
	}
	if got := len(basket.Items); got != 1 {
		t.Fatalf("want the written line back, got %d lines", got)
	}
}

// TestUnlistedWeightNotSent is the guard that matters most. Tesco accepts a
// weight it does not offer, writes it, and reports success — and tesco.com then
// fails to render the entire basket, naming no line. The write must never leave.
func TestUnlistedWeightNotSent(t *testing.T) {
	var seen []string
	c := newTestClient(t, recording(routed(t, map[string]string{
		"GetProduct":   catchweightProductResponse("259541778", "Tesco Large Pork Shoulder Joint", 1.8, 1.95, 2.1),
		"UpdateBasket": basketResponse(catchweightLine("259541778", "1.5")),
	}), &seen))

	_, err := c.SetWeight(context.Background(), "259541778", 1.5, "o1")
	var notOffered *WeightNotOfferedError
	if !errors.As(err, &notOffered) {
		t.Fatalf("want WeightNotOfferedError, got %v", err)
	}
	for _, op := range seen {
		if op == "UpdateBasket" {
			t.Fatal("an unlisted weight reached the gateway; it must be refused before the write")
		}
	}
	for _, w := range notOffered.Offered {
		if w == 1.5 {
			t.Fatal("1.5 should not be reported as offered")
		}
	}
}

// TestWeightOnPieceProductRejected covers the mirror image: writing a weight to
// a product sold by the piece corrupts the basket the same way.
func TestWeightOnPieceProductRejected(t *testing.T) {
	c := newTestClient(t, routed(t, map[string]string{
		"GetProduct":   productResponse("254656543", "Nutella 350G", 2.99, 8.54, "kg"),
		"UpdateBasket": basketResponse(basketLine("254656543", "1")),
	}))

	_, err := c.SetWeight(context.Background(), "254656543", 1.8, "o1")
	var notCatchweight *NotCatchweightError
	if !errors.As(err, &notCatchweight) {
		t.Fatalf("want NotCatchweightError, got %v", err)
	}
}

// TestCatchweightListIsAuthoritative pins that a returned weight list decides
// the question, without recourse to the price heuristic.
func TestCatchweightListIsAuthoritative(t *testing.T) {
	actual, unit := 2.99, 8.54
	p := Product{
		Price:           &Price{Actual: &actual, UnitPrice: &unit, UnitOfMeasure: "kg"},
		CatchWeightList: []CatchWeight{{Weight: 1.8, Default: true}, {Weight: 2.1}},
	}
	// The heuristic alone would say no: the shelf price is not the unit price.
	if !p.Catchweight() {
		t.Error("a product with a weight list is catchweight whatever its prices say")
	}
	if got, ok := p.DefaultWeight(); !ok || got != 1.8 {
		t.Errorf("DefaultWeight() = %v, %v; want 1.8, true", got, ok)
	}
	if !p.HasWeight(2.1) || p.HasWeight(1.5) {
		t.Error("HasWeight should accept a listed weight and reject an unlisted one")
	}
}

// TestDefaultWeightFallsBackToFirst covers a list where Tesco marks nothing as
// the default.
func TestDefaultWeightFallsBackToFirst(t *testing.T) {
	p := Product{CatchWeightList: []CatchWeight{{Weight: 1.75}, {Weight: 2}}}
	if got, ok := p.DefaultWeight(); !ok || got != 1.75 {
		t.Errorf("DefaultWeight() = %v, %v; want 1.75, true", got, ok)
	}
	if _, ok := (Product{}).DefaultWeight(); ok {
		t.Error("a product with no weights has no default")
	}
}

// TestListingCarriesTotal pins that a page of results reports the size of the
// whole set. Nothing in the products reveals truncation: a full page and the
// first page of five hundred look identical.
func TestListingCarriesTotal(t *testing.T) {
	var info *ListInfo
	if got := (resultList{Info: info}).listing(10); got.Total != 0 {
		t.Errorf("a listing with no info should report no total, got %d", got.Total)
	}

	r := resultList{
		Info: &ListInfo{Total: 553, Count: 2, Page: 3},
		Results: []struct {
			Node Product `json:"node"`
		}{
			{Node: Product{TPNC: "1", Title: "One"}},
			{Node: Product{TPNC: "2", Title: "Two"}},
			{Node: Product{}}, // a sponsored node, skipped
		},
	}
	got := r.listing(10)
	if got.Total != 553 || got.Page != 3 {
		t.Errorf("listing() = total %d page %d; want 553, 3", got.Total, got.Page)
	}
	if len(got.Products) != 2 {
		t.Errorf("want the 2 real products, got %d", len(got.Products))
	}
}

// TestProductURL pins the tesco.com link, which is where a user is sent for
// everything grosh cannot do.
func TestProductURL(t *testing.T) {
	if got := ProductURL("259541778"); got != "https://www.tesco.com/groceries/en-GB/products/259541778" {
		t.Errorf("ProductURL() = %q", got)
	}
	if got := ProductURL(""); got != "" {
		t.Errorf("an empty tpnc should have no url, got %q", got)
	}
}
