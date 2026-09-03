package tesco

import "context"

// resultList is the shared envelope for paginated product listings. Nodes that
// are not products (sponsored placements) decode to a zero Product.
type resultList struct {
	Info    *ListInfo `json:"info"`
	Results []struct {
		Node Product `json:"node"`
	} `json:"results"`
}

// listing pairs the trimmed page with the totals Tesco reported for the whole
// result set, so a caller can tell a full listing from a truncated one.
func (r resultList) listing(limit int) Listing {
	out := Listing{Products: r.products(limit)}
	if r.Info != nil {
		out.Total = r.Info.Total
		out.Page = r.Info.Page
	}
	return out
}

// products flattens the node list, skipping non-product nodes and trimming to
// limit. Tesco consistently returns about six more nodes than the requested
// count — verified at count=3, 6 and 24 — so a caller asking for six results
// gets six rather than twelve. Results are relevance-ordered, so the surplus is
// taken off the end.
func (r resultList) products(limit int) []Product {
	out := make([]Product, 0, len(r.Results))
	for _, entry := range r.Results {
		if entry.Node.Empty() {
			continue
		}
		out = append(out, entry.Node)
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// Search returns products matching query. Works anonymously.
func (c *Client) Search(ctx context.Context, query string, page, count int) (Listing, error) {
	var out struct {
		Search resultList `json:"search"`
	}
	err := c.exec(ctx, op{
		Name:  "Search",
		Query: searchQuery,
		MFE:   mfeSearch,
		Vars:  map[string]any{"query": query, "page": page, "count": count},
	}, &out)
	if err != nil {
		return Listing{}, err
	}
	return out.Search.listing(count), nil
}

// Product looks up one product by TPNC, including nutrition and pack size.
// Works anonymously.
func (c *Client) Product(ctx context.Context, tpnc string) (*Product, error) {
	var out struct {
		Product *Product `json:"product"`
	}
	err := c.exec(ctx, op{
		Name:  "GetProduct",
		Query: productQuery,
		MFE:   mfeProduct,
		Vars:  map[string]any{"tpnc": tpnc},
	}, &out)
	if err != nil {
		// Tesco reports an unknown TPNC as an operation error rather than a
		// null product, so the not-found case arrives here, not below.
		if isNotFound(err) {
			return nil, &NotFoundError{Kind: "product", ID: tpnc}
		}
		return nil, err
	}
	if out.Product == nil || out.Product.Empty() {
		return nil, &NotFoundError{Kind: "product", ID: tpnc}
	}
	return out.Product, nil
}

// Taxonomy returns the category tree. The ID on each node is the facet used by
// Category. Works anonymously.
func (c *Client) Taxonomy(ctx context.Context) ([]TaxonomyNode, error) {
	var out struct {
		Taxonomy []TaxonomyNode `json:"taxonomy"`
	}
	err := c.exec(ctx, op{
		Name:  "Taxonomy",
		Query: taxonomyQuery,
		MFE:   mfeSearch,
		Vars:  map[string]any{},
	}, &out)
	if err != nil {
		return nil, err
	}
	return out.Taxonomy, nil
}

// Category lists products in a category by facet id. Works anonymously.
func (c *Client) Category(ctx context.Context, facet string, page, count int) (Listing, error) {
	var out struct {
		Category resultList `json:"category"`
	}
	err := c.exec(ctx, op{
		Name:  "GetCategoryProducts",
		Query: categoryQuery,
		MFE:   mfeSearch,
		Vars:  map[string]any{"facet": facet, "page": page, "count": count},
	}, &out)
	if err != nil {
		return Listing{}, err
	}
	return out.Category.listing(count), nil
}
