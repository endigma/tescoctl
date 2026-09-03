// Package view holds the output models. These are deliberately separate from
// the tesco package's response structs: those track whatever Tesco's gateway
// happens to return today, while these are the contract for --json output and
// should only change when we mean to break downstream consumers.
package view

// Product is a product as reported by search, browse, and favourites.
type Product struct {
	TPNC          string   `json:"tpnc"`
	TPNB          string   `json:"tpnb,omitempty"`
	Title         string   `json:"title"`
	Brand         string   `json:"brand,omitempty"`
	Price         *float64 `json:"price"`
	UnitPrice     *float64 `json:"unitPrice,omitempty"`
	UnitOfMeasure string   `json:"unitOfMeasure,omitempty"`
	// IsForSale is Tesco's catalogue-level flag, and is deliberately not named
	// "available": it says nothing about store or slot stock, and a product can
	// report true while being impossible to add to a basket.
	IsForSale *bool    `json:"isForSale"`
	Offers    []string `json:"offers,omitempty"`
	ImageURL  string   `json:"imageUrl,omitempty"`
}

// ProductDetail extends Product with the fields only a single-product lookup
// returns.
type ProductDetail struct {
	Product
	URL         string     `json:"url,omitempty"`
	PackSize    string     `json:"packSize,omitempty"`
	Ingredients []string   `json:"ingredients,omitempty"`
	Nutrition   []Nutrient `json:"nutrition,omitempty"`

	// Weights are the weights a catchweight product may be bought in. It is
	// empty for a product sold by the piece, which is most of them.
	Weights []Weight `json:"weights,omitempty"`
}

// Weight is one selectable weight of a product sold by weight, in kilograms.
type Weight struct {
	Weight  float64  `json:"weight"`
	Price   *float64 `json:"price,omitempty"`
	Default bool     `json:"default,omitempty"`
}

// Nutrient is one row of a product's nutrition table. Columns names the value
// columns, taken from the table's header row.
type Nutrient struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

// BasketCheck is the result of verifying every line in a basket against the
// catalogue. Problems is empty when the basket is sound.
type BasketCheck struct {
	Checked  int             `json:"checked"`
	Problems []BasketProblem `json:"problems"`
}

// BasketProblem is one thing wrong with a basket line. Kind is a stable slug
// so a script can branch on it; Detail is the sentence a person reads.
type BasketProblem struct {
	TPNC   string `json:"tpnc"`
	Title  string `json:"title,omitempty"`
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
}

// The problem kinds a check can report.
const (
	// ProblemUnavailable is a line Tesco no longer sells. It stays in the
	// basket and is simply not delivered.
	ProblemUnavailable = "unavailable"

	// ProblemUnrenderable is a line tesco.com cannot display: a fractional
	// quantity that is not one of the product's catchweight options. The whole
	// basket page fails while it is there.
	ProblemUnrenderable = "unrenderable"

	// ProblemUnknown is a line whose product could not be looked up, so it was
	// neither cleared nor faulted.
	ProblemUnknown = "unknown"
)

// Category is one node of the category tree.
type Category struct {
	Facet    string     `json:"facet"`
	Name     string     `json:"name"`
	Label    string     `json:"label,omitempty"`
	Children []Category `json:"children,omitempty"`
}

// Error is the --json representation of a failure.
type Error struct {
	Error string `json:"error"`
}

// Session is the reported state of the stored login.
type Session struct {
	LoggedIn     bool   `json:"loggedIn"`
	Path         string `json:"path"`
	CustomerUUID string `json:"customerUuid,omitempty"`
	ExpiresAt    string `json:"expiresAt,omitempty"`
	Expired      bool   `json:"expired"`
	Cookies      int    `json:"cookies"`

	// Renewable reports whether the session can be renewed without a human,
	// and RenewableUntil is when that stops being true. An expired session
	// that is still renewable needs no action.
	Renewable      bool   `json:"renewable"`
	RenewableUntil string `json:"renewableUntil,omitempty"`
}

// Basket is the current trolley. GuidePrice is Tesco's estimate — weighted
// items settle at pick time, so the final charge can differ.
type Basket struct {
	ID          string       `json:"id"`
	GuidePrice  *float64     `json:"guidePrice"`
	ItemCount   int          `json:"itemCount"`
	InAmend     bool         `json:"inAmend"`
	CheckoutURL string       `json:"checkoutUrl"`
	Items       []BasketItem `json:"items"`
}

// BasketItem is one trolley line. Quantity is a number because loose items are
// ordered by weight.
type BasketItem struct {
	TPNC     string   `json:"tpnc"`
	Title    string   `json:"title"`
	Quantity *float64 `json:"quantity"`
	Unit     string   `json:"unit,omitempty"`
	Cost     *float64 `json:"cost"`
}

// Order is a placed order.
type Order struct {
	ID      string      `json:"id"`
	OrderNo string      `json:"orderNo"`
	Status  string      `json:"status"`
	Placed  string      `json:"placed,omitempty"`
	Total   *float64    `json:"total"`
	Slot    *Slot       `json:"slot,omitempty"`
	Items   []OrderItem `json:"items,omitempty"`
}

// OrderItem is one line of an order.
type OrderItem struct {
	TPNC     string   `json:"tpnc"`
	Title    string   `json:"title"`
	Quantity *float64 `json:"quantity"`
	Cost     *float64 `json:"cost"`
}

// Slot is a fulfilment window.
type Slot struct {
	ID     string   `json:"id,omitempty"`
	Start  string   `json:"start"`
	End    string   `json:"end"`
	Charge *float64 `json:"charge"`
	Status string   `json:"status,omitempty"`
}
