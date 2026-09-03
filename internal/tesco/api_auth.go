package tesco

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

// OrderContext scopes an order search to a type and a set of statuses.
type OrderContext struct {
	Type     string   `json:"type"`
	Statuses []string `json:"statuses"`
}

// PreviousOrders scopes an order search to completed grocery orders.
var PreviousOrders = []OrderContext{{Type: "GROCERY", Statuses: []string{"Previous"}}}

// PendingOrders scopes an order search to orders not yet delivered, across
// every order type Tesco splits a basket into.
var PendingOrders = []OrderContext{
	{Type: "GROCERY", Statuses: []string{"Pending"}},
	{Type: "MARKETPLACE", Statuses: []string{"Pending"}},
	{Type: "FNF", Statuses: []string{"Pending"}},
}

// Basket returns the current trolley. Requires a session.
func (c *Client) Basket(ctx context.Context) (*Basket, error) {
	var out struct {
		Basket *Basket `json:"basket"`
	}
	err := c.exec(ctx, op{
		Name:  "GetBasket",
		Query: basketQuery,
		MFE:   mfeBasket,
		Vars:  map[string]any{},
	}, &out)
	if err != nil {
		return nil, err
	}
	if out.Basket == nil {
		return &Basket{}, nil
	}
	return out.Basket, nil
}

// The units a basket line can be written in. Most lines are whole pieces;
// catchweight products — a roasting joint sold in a few fixed weights — must be
// written in kilos instead, and are silently dropped when written as pieces.
//
// A fractional quantity written against UnitPieces produces a line tesco.com's
// own basket page cannot render, which is why the two must be chosen together.
const (
	UnitPieces = "pcs"
	UnitKilos  = "kg"
)

// FractionalPieces reports the combination that corrupts a basket: a quantity
// that is not a whole number on a line sold in whole pieces. tesco.com accepts
// such a line, then fails to render the entire basket page, naming no line.
func FractionalPieces(quantity float64, unit string) bool {
	return strings.EqualFold(unit, UnitPieces) && quantity != math.Trunc(quantity)
}

// SetQuantity sets a line's quantity, adding the product when it is not in the
// basket and removing it when quantity is zero. Tesco uses one mutation for all
// three, keyed on the basket's order id. Requires a session.
//
// The basket in the mutation response is checked against the request: Tesco
// answers some writes with a success it does not honour, so the returned basket
// is the only evidence a write actually landed.
func (c *Client) SetQuantity(ctx context.Context, tpnc string, quantity float64, orderID string) (*Basket, error) {
	if FractionalPieces(quantity, UnitPieces) {
		return nil, &FractionalQuantityError{TPNC: tpnc, Quantity: quantity, Unit: UnitPieces}
	}

	basket, err := c.write(ctx, tpnc, quantity, UnitPieces, orderID)
	if err != nil {
		return nil, err
	}
	if err := confirmQuantity(basket, tpnc, quantity); err != nil {
		return nil, c.diagnose(ctx, err, tpnc, quantity)
	}
	return basket, nil
}

// SetWeight sets a catchweight line to one of the product's selectable weights,
// in kilograms. Requires a session.
//
// The weight is checked against the product's CatchWeightList first, which costs
// a lookup. That is deliberate: the gateway accepts an unlisted weight, writes
// it, and reports success, but tesco.com's basket page then fails to render —
// taking the whole basket with it, naming no line. There is no cheaper way to
// know, and no way to detect the damage afterwards from the API alone.
func (c *Client) SetWeight(ctx context.Context, tpnc string, weight float64, orderID string) (*Basket, error) {
	product, err := c.Product(ctx, tpnc)
	if err != nil {
		return nil, fmt.Errorf("looking up %s before a weighed write: %w", tpnc, err)
	}
	if len(product.CatchWeightList) == 0 {
		return nil, &NotCatchweightError{TPNC: tpnc, Title: product.Title}
	}
	if !product.HasWeight(weight) {
		return nil, &WeightNotOfferedError{
			TPNC: tpnc, Title: product.Title,
			Weight: weight, Offered: product.Weights(),
		}
	}

	basket, err := c.write(ctx, tpnc, weight, UnitKilos, orderID)
	if err != nil {
		return nil, err
	}
	if err := confirmQuantity(basket, tpnc, weight); err != nil {
		return nil, err
	}
	return basket, nil
}

// write performs the UpdateBasket mutation and returns the basket it answers
// with, resolving the order id first when the caller did not supply one.
func (c *Client) write(ctx context.Context, tpnc string, value float64, unit, orderID string) (*Basket, error) {
	if orderID == "" {
		basket, err := c.Basket(ctx)
		if err != nil {
			return nil, fmt.Errorf("reading basket before update: %w", err)
		}
		orderID = basket.ID
	}

	var out struct {
		Basket *Basket `json:"basket"`
	}
	err := c.exec(ctx, op{
		Name:  "UpdateBasket",
		Query: updateBasketQuery,
		MFE:   mfeBasket,
		Vars: map[string]any{
			"orderId": orderID,
			"items": []map[string]any{{
				"adjustment":    false,
				"id":            tpnc,
				"newValue":      value,
				"newUnitChoice": unit,
			}},
		},
	}, &out)
	if err != nil {
		return nil, err
	}
	if out.Basket == nil {
		return &Basket{}, nil
	}
	return out.Basket, nil
}

// diagnose names the cause of a write that did not land, where the product can
// name it. It runs on the failure path rather than before every write because
// the check needs a product lookup: a write that lands needs no explanation,
// and pre-flighting every add would spend a request to learn nothing.
func (c *Client) diagnose(ctx context.Context, err error, tpnc string, quantity float64) error {
	var notUpdated *BasketNotUpdatedError
	if !errors.As(err, &notUpdated) || notUpdated.Present || quantity == 0 {
		return err
	}
	// A failed lookup leaves the generic error in place — the write still did
	// not land, which is the part the caller must not miss.
	product, lookupErr := c.Product(ctx, tpnc)
	if lookupErr != nil || !product.Catchweight() {
		return err
	}
	return &CatchweightError{TPNC: tpnc, Title: product.Title, Weights: product.Weights(), err: notUpdated}
}

// line returns the basket line for a product, or nil when it has none. Lines
// are matched on the product's tpnc; the line id carries the same value today,
// but only for products, so it is a fallback for a line with no product node.
func (b *Basket) line(tpnc string) *BasketItem {
	for i := range b.Items {
		item := &b.Items[i]
		if item.Product != nil {
			if item.Product.TPNC == tpnc {
				return item
			}
			continue
		}
		if item.ID == tpnc {
			return item
		}
	}
	return nil
}

// confirmQuantity checks the basket a write returned against what the write
// asked for. Tesco accepts the UpdateBasket mutation for some products, returns
// no GraphQL error, and simply does not apply it — so without this check the
// write is lost while grosh reports success and exits 0.
func confirmQuantity(b *Basket, tpnc string, want float64) error {
	line := b.line(tpnc)
	fail := &BasketNotUpdatedError{TPNC: tpnc, Want: want, Present: line != nil}
	if line != nil {
		fail.Got = line.Quantity
	}

	switch {
	case line == nil:
		// Absence is the goal of a removal and the failure of an add.
		if want == 0 {
			return nil
		}
	case line.Quantity == nil:
		// A line with no quantity tells us nothing either way; treat it as
		// unconfirmed rather than assume the write landed.
	case *line.Quantity == want:
		return nil
	}
	return fail
}

// Favourites returns the account's usual items. Requires a session.
func (c *Client) Favourites(ctx context.Context, page, count int) (Listing, error) {
	var out struct {
		Favourites struct {
			Info     *ListInfo `json:"info"`
			Products []Product `json:"products"`
		} `json:"favourites"`
	}
	err := c.exec(ctx, op{
		Name:  "GetFavourites",
		Query: favouritesQuery,
		MFE:   mfeFavourites,
		Vars:  map[string]any{"page": page, "count": count},
	}, &out)
	if err != nil {
		return Listing{}, err
	}

	products := make([]Product, 0, len(out.Favourites.Products))
	for _, p := range out.Favourites.Products {
		if !p.Empty() {
			products = append(products, p)
		}
	}
	listing := Listing{Products: products}
	if info := out.Favourites.Info; info != nil {
		listing.Total = info.Total
		listing.Page = info.Page
	}
	return listing, nil
}

// Orders lists orders matching contexts. Requires a session.
func (c *Client) Orders(ctx context.Context, contexts []OrderContext, page, count int) ([]Order, error) {
	var out struct {
		OrderSearch struct {
			Orders []Order `json:"orders"`
		} `json:"orderSearch"`
	}
	err := c.exec(ctx, op{
		Name:  "GetPreviousOrdersWithPagination",
		Query: ordersQuery,
		MFE:   mfeOrders,
		Vars: map[string]any{
			"orderContexts": contexts,
			"page":          page,
			"count":         count,
		},
	}, &out)
	if err != nil {
		return nil, err
	}
	return out.OrderSearch.Orders, nil
}

// Order returns one order with its lines. Requires a session.
func (c *Client) Order(ctx context.Context, id string) (*Order, error) {
	var out struct {
		Order *Order `json:"order"`
	}
	err := c.exec(ctx, op{
		Name:  "GetOrderReceipt",
		Query: orderQuery,
		MFE:   mfeOrders,
		Vars:  map[string]any{"id": id},
	}, &out)
	if err != nil {
		if isNotFound(err) {
			return nil, &NotFoundError{Kind: "order", ID: id}
		}
		return nil, err
	}
	if out.Order == nil {
		return nil, &NotFoundError{Kind: "order", ID: id}
	}
	return out.Order, nil
}

// Slots lists delivery windows in [start, end). Requires a session, because
// availability depends on the account's delivery address.
func (c *Client) Slots(ctx context.Context, start, end time.Time) ([]DeliverySlot, error) {
	var out struct {
		Delivery []DeliverySlot `json:"delivery"`
	}
	err := c.exec(ctx, op{
		Name:  "DeliverySlots",
		Query: slotsQuery,
		MFE:   mfeSlots,
		Vars: map[string]any{
			"start": start.UTC().Format(time.RFC3339),
			"end":   end.UTC().Format(time.RFC3339),
		},
	}, &out)
	if err != nil {
		return nil, err
	}
	return out.Delivery, nil
}
