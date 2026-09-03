package tesco

import "strings"

// Types mirror what xapi.tesco.com actually returns, captured from live
// responses rather than inferred from the reference JS query strings — those
// select `packSize`, `nutrition` and `promotions` as if they were objects when
// the server sends lists.
//
// Numeric fields that can legitimately be absent are pointers: a missing price
// must not decode to £0.00.

// Price is a product's current pricing. unitPrice/unitOfMeasure express the
// comparison price, e.g. 0.73 per "litre".
type Price struct {
	Actual        *float64 `json:"actual"`
	UnitPrice     *float64 `json:"unitPrice"`
	UnitOfMeasure string   `json:"unitOfMeasure"`
}

// PromotionPrice is the before/after pair on a promotion.
type PromotionPrice struct {
	AfterDiscount  *float64 `json:"afterDiscount"`
	BeforeDiscount *float64 `json:"beforeDiscount"`
}

// Promotion is a single offer attached to a product.
type Promotion struct {
	Description string          `json:"description"`
	StartDate   string          `json:"startDate"`
	EndDate     string          `json:"endDate"`
	Attributes  []string        `json:"attributes"`
	Price       *PromotionPrice `json:"price"`
}

// PackSize is one pack-size entry. Value arrives as a numeric string ("2.272").
type PackSize struct {
	Value string `json:"value"`
	Units string `json:"units"`
}

// Nutrient is one row of the nutrition table. The value columns are positional
// and their meaning comes from the first row, whose Name is "Typical Values"
// and whose values are the column headers ("Per 100ml", "Per 200ml", ...).
type Nutrient struct {
	Name   string `json:"name"`
	Value1 string `json:"value1"`
	Value2 string `json:"value2"`
	Value3 string `json:"value3"`
}

// Details is the descriptive block on a product, only populated on GetProduct.
type Details struct {
	PackSize    []PackSize `json:"packSize"`
	Nutrition   []Nutrient `json:"nutrition"`
	Ingredients []string   `json:"ingredients"`
}

// Product is a grocery item. TPNC is the SKU used for lookups and basket ops;
// TPNB identifies the underlying product across pack variants.
type Product struct {
	TPNC            string      `json:"tpnc"`
	TPNB            string      `json:"tpnb"`
	Title           string      `json:"title"`
	BrandName       string      `json:"brandName"`
	DefaultImageURL string      `json:"defaultImageUrl"`
	IsForSale       *bool       `json:"isForSale"`
	Price           *Price      `json:"price"`
	Promotions      []Promotion `json:"promotions"`
	Details         *Details    `json:"details"`

	// CatchWeightList is the set of weights a variable-weight product may be
	// bought in. It is returned only by the single-product lookup, so it is
	// empty on search and category results even for a catchweight product.
	CatchWeightList []CatchWeight `json:"catchWeightList"`
}

// CatchWeight is one selectable weight of a variable-weight product: Tesco
// offers a roasting joint in a few fixed weights, not by the continuous kilo.
// Weight is in kilograms and Price is the cost of that weight.
type CatchWeight struct {
	Weight  float64 `json:"weight"`
	Price   float64 `json:"price"`
	Default bool    `json:"default"`
}

// Catchweight reports whether a product is sold by variable weight rather than
// by the piece — a roasting joint bought in one of a few fixed weights, not a
// 700g pack.
//
// A non-empty CatchWeightList is the authoritative answer, but only the
// single-product lookup returns it. Search and category results fall back to a
// heuristic: the shelf price *is* the unit price, against a weight unit —
// what a catchweight product "costs" is what a kilo of it costs.
//
// UnitOfMeasure on its own says nothing, because nearly every product carries
// one as the basis of its comparison price — Nutella 350g reports £2.99 at
// £8.54/kg — so it must never be read as "sold by weight" by itself.
//
// This matters because a piece-denominated write for a catchweight line is
// silently dropped by the gateway. Such a line must be written in kilos, at one
// of the weights in CatchWeightList.
func (p Product) Catchweight() bool {
	if len(p.CatchWeightList) > 0 {
		return true
	}
	if p.Price == nil || p.Price.Actual == nil || p.Price.UnitPrice == nil {
		return false
	}
	if !strings.EqualFold(p.Price.UnitOfMeasure, "kg") {
		return false
	}
	return *p.Price.Actual == *p.Price.UnitPrice
}

// Weights lists the selectable weights of a catchweight product, in the order
// Tesco returns them.
func (p Product) Weights() []float64 {
	out := make([]float64, 0, len(p.CatchWeightList))
	for _, cw := range p.CatchWeightList {
		out = append(out, cw.Weight)
	}
	return out
}

// DefaultWeight returns the weight Tesco pre-selects, falling back to the first
// listed. It reports false when the product has no weights to choose from.
func (p Product) DefaultWeight() (float64, bool) {
	for _, cw := range p.CatchWeightList {
		if cw.Default {
			return cw.Weight, true
		}
	}
	if len(p.CatchWeightList) > 0 {
		return p.CatchWeightList[0].Weight, true
	}
	return 0, false
}

// HasWeight reports whether w is one of the selectable weights. Tesco rejects
// nothing here: an unlisted weight is accepted by the gateway and written to the
// basket, where it renders as an invalid line that breaks tesco.com's basket
// page — so this check is grosh's job, not the API's.
func (p Product) HasWeight(w float64) bool {
	for _, cw := range p.CatchWeightList {
		if cw.Weight == w {
			return true
		}
	}
	return false
}

// Empty reports whether the node decoded to nothing useful. Search and category
// results interleave sponsored and non-product nodes that do not match the
// `... on ProductType` fragment; those decode to a zero Product and are skipped.
func (p Product) Empty() bool { return p.TPNC == "" && p.Title == "" }

// ListInfo is what Tesco reports about a result set as a whole, alongside the
// page of products it returned. Total is the size of the whole set, which is the
// only way to know a listing was truncated: the products themselves cannot say.
//
// The gateway returns more nodes than asked for, so Count is Tesco's own idea of
// the page size and is not the number of products grosh emits.
type ListInfo struct {
	Total  int `json:"total"`
	Count  int `json:"count"`
	Page   int `json:"page"`
	Offset int `json:"offset"`
}

// Listing is one page of a product list, with what Tesco said about the whole
// set. Total is zero when the gateway did not report one.
type Listing struct {
	Products []Product
	Total    int
	Page     int
}

// TaxonomyNode is one level of the category tree. ID is the opaque facet used
// to browse the category — see facet.go for its encoding.
type TaxonomyNode struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Label    string         `json:"label"`
	Children []TaxonomyNode `json:"children"`
}

// Basket is the current trolley. GuidePrice is Tesco's estimate: weighted items
// settle at pick time, so the final charge can differ.
type Basket struct {
	ID             string       `json:"id"`
	GuidePrice     *float64     `json:"guidePrice"`
	IsInAmend      *bool        `json:"isInAmend"`
	AmendExpiry    string       `json:"amendExpiry"`
	ShoppingMethod string       `json:"shoppingMethod"`
	Items          []BasketItem `json:"items"`
}

// BasketItem is one line. Quantity is a number rather than an integer because
// loose items are ordered by weight.
type BasketItem struct {
	ID       string   `json:"id"`
	Quantity *float64 `json:"quantity"`
	Cost     *float64 `json:"cost"`
	Unit     string   `json:"unit"`
	Weight   *float64 `json:"weight"`
	Product  *Product `json:"product"`
}

// Slot is the fulfilment window attached to an order.
type Slot struct {
	Start  string   `json:"start"`
	End    string   `json:"end"`
	Charge *float64 `json:"charge"`
}

// DeliverySlot is a bookable delivery window.
type DeliverySlot struct {
	ID     string   `json:"id"`
	Start  string   `json:"start"`
	End    string   `json:"end"`
	Charge *float64 `json:"charge"`
	Status string   `json:"status"`

	// Group is a number, not a string. It was declared as a string and broke
	// every `grosh slots` call at the decoding step — the query was fine, so
	// the live suite, which validates that fields exist, could not see it.
	Group        int        `json:"group"`
	Price        *SlotPrice `json:"price"`
	LocationUUID string     `json:"locationUuid"`
}

// SlotPrice carries the Clubcard-discounted delivery charge where one applies.
type SlotPrice struct {
	BeforeDiscount *float64 `json:"beforeDiscount"`
	AfterDiscount  *float64 `json:"afterDiscount"`
}

// Order is a placed order.
type Order struct {
	ID              string      `json:"id"`
	OrderNo         string      `json:"orderNo"`
	Status          string      `json:"status"`
	CreatedDateTime string      `json:"createdDateTime"`
	TotalPrice      *float64    `json:"totalPrice"`
	Slot            *Slot       `json:"slot"`
	Items           []OrderItem `json:"items"`
}

// OrderItem is one line of an order.
type OrderItem struct {
	Quantity *float64 `json:"quantity"`
	Cost     *float64 `json:"cost"`
	Unit     string   `json:"unit"`
	Weight   *float64 `json:"weight"`
	Product  *Product `json:"product"`
}
