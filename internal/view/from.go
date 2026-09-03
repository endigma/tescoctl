package view

import (
	"strings"

	"github.com/endigma/tescoctl/internal/tesco"
)

// FromProduct converts a Tesco product into the listing view model.
func FromProduct(p tesco.Product) Product {
	v := Product{
		TPNC:      p.TPNC,
		TPNB:      p.TPNB,
		Title:     p.Title,
		Brand:     p.BrandName,
		IsForSale: p.IsForSale,
		ImageURL:  p.DefaultImageURL,
	}
	if p.Price != nil {
		v.Price = p.Price.Actual
		v.UnitPrice = p.Price.UnitPrice
		v.UnitOfMeasure = p.Price.UnitOfMeasure
	}
	for _, promo := range p.Promotions {
		if d := strings.TrimSpace(promo.Description); d != "" {
			v.Offers = append(v.Offers, d)
		}
	}
	return v
}

// FromProducts converts a listing.
func FromProducts(ps []tesco.Product) []Product {
	out := make([]Product, 0, len(ps))
	for _, p := range ps {
		out = append(out, FromProduct(p))
	}
	return out
}

// FromProductDetail converts a single-product lookup, including the fields only
// that operation returns.
func FromProductDetail(p tesco.Product) ProductDetail {
	v := ProductDetail{Product: FromProduct(p)}
	v.URL = tesco.ProductURL(p.TPNC)
	for _, cw := range p.CatchWeightList {
		price := cw.Price
		v.Weights = append(v.Weights, Weight{Weight: cw.Weight, Price: &price, Default: cw.Default})
	}
	if p.Details == nil {
		return v
	}
	if len(p.Details.PackSize) > 0 {
		ps := p.Details.PackSize[0]
		v.PackSize = strings.TrimSpace(ps.Value + ps.Units)
	}
	for _, ing := range p.Details.Ingredients {
		if ing = strings.TrimSpace(ing); ing != "" {
			v.Ingredients = append(v.Ingredients, ing)
		}
	}
	v.Nutrition = fromNutrition(p.Details.Nutrition)
	return v
}

// fromNutrition flattens Tesco's positional value1/value2/value3 columns into a
// slice, dropping trailing empties so a two-column table does not carry a third
// blank column.
func fromNutrition(rows []tesco.Nutrient) []Nutrient {
	out := make([]Nutrient, 0, len(rows))
	for _, r := range rows {
		values := []string{r.Value1, r.Value2, r.Value3}
		for len(values) > 0 && strings.TrimSpace(values[len(values)-1]) == "" {
			values = values[:len(values)-1]
		}
		if r.Name == "" && len(values) == 0 {
			continue
		}
		out = append(out, Nutrient{Name: r.Name, Values: values})
	}
	return out
}

// FromTaxonomy converts the category tree.
func FromTaxonomy(nodes []tesco.TaxonomyNode) []Category {
	out := make([]Category, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, Category{
			Facet:    n.ID,
			Name:     n.Name,
			Label:    n.Label,
			Children: FromTaxonomy(n.Children),
		})
	}
	return out
}

// FromBasket converts the trolley, flattening each line to the fields worth
// showing.
func FromBasket(b *tesco.Basket, checkoutURL string) Basket {
	v := Basket{ID: b.ID, GuidePrice: b.GuidePrice, CheckoutURL: checkoutURL}
	if b.IsInAmend != nil {
		v.InAmend = *b.IsInAmend
	}
	for _, item := range b.Items {
		line := BasketItem{Quantity: item.Quantity, Unit: item.Unit, Cost: item.Cost}
		if item.Product != nil {
			line.TPNC = item.Product.TPNC
			line.Title = item.Product.Title
		}
		v.Items = append(v.Items, line)
	}
	v.ItemCount = len(v.Items)
	return v
}

// FromOrder converts an order. Items are only present on a single-order lookup.
func FromOrder(o tesco.Order) Order {
	v := Order{
		ID:      o.ID,
		OrderNo: o.OrderNo,
		Status:  o.Status,
		Placed:  o.CreatedDateTime,
		Total:   o.TotalPrice,
	}
	if o.Slot != nil {
		v.Slot = &Slot{Start: o.Slot.Start, End: o.Slot.End, Charge: o.Slot.Charge}
	}
	for _, item := range o.Items {
		line := OrderItem{Quantity: item.Quantity, Cost: item.Cost}
		if item.Product != nil {
			line.TPNC = item.Product.TPNC
			line.Title = item.Product.Title
		}
		v.Items = append(v.Items, line)
	}
	return v
}

// FromOrders converts an order listing.
func FromOrders(orders []tesco.Order) []Order {
	out := make([]Order, 0, len(orders))
	for _, o := range orders {
		out = append(out, FromOrder(o))
	}
	return out
}

// FromSlots converts delivery windows, preferring the discounted charge where
// Clubcard pricing applies.
func FromSlots(slots []tesco.DeliverySlot) []Slot {
	out := make([]Slot, 0, len(slots))
	for _, s := range slots {
		v := Slot{ID: s.ID, Start: s.Start, End: s.End, Charge: s.Charge, Status: s.Status}
		if s.Price != nil && s.Price.AfterDiscount != nil {
			v.Charge = s.Price.AfterDiscount
		}
		out = append(out, v)
	}
	return out
}
