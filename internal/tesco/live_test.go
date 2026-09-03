//go:build live

// These tests hit the real gateway. Run them with:
//
//	go test -tags live ./internal/tesco/
//
// They are the substitute for a schema: Tesco disables introspection, so the
// only way to know our selections still match is to ask. Because the gateway
// runs GraphQL validation before authentication, an operation with a stale
// field answers "Cannot query field X on type Y" even without a session — which
// means the authenticated operations can be checked too, without an account.
package tesco

import (
	"context"
	"strings"
	"testing"
	"time"
)

func liveClient() *Client {
	// Politeness: these tests make a dozen or so requests.
	return New(Options{Throttle: time.Second})
}

func TestLiveAnonymousReads(t *testing.T) {
	c := liveClient()
	ctx := context.Background()

	t.Run("Search", func(t *testing.T) {
		listing, err := c.Search(ctx, "milk", 1, 5)
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		products := listing.Products
		if len(products) == 0 {
			t.Fatal("no results for milk — the search operation has probably drifted")
		}
		if products[0].Title == "" || products[0].TPNC == "" {
			t.Errorf("product decoded without title/tpnc: %+v", products[0])
		}
		if products[0].Price == nil || products[0].Price.Actual == nil {
			t.Error("product decoded without a price")
		}
		// The total is what tells a caller the listing was truncated; a search
		// for milk matches far more than the five asked for.
		if listing.Total <= len(products) {
			t.Errorf("total = %d for %d results — list info has probably drifted",
				listing.Total, len(products))
		}
	})

	t.Run("Product", func(t *testing.T) {
		// Tesco British Semi Skimmed Milk 2.272L.
		p, err := c.Product(ctx, "254656543")
		if err != nil {
			t.Fatalf("Product: %v", err)
		}
		if p.Details == nil || len(p.Details.Nutrition) == 0 {
			t.Error("product detail decoded without nutrition")
		}
		if p.Details != nil && len(p.Details.PackSize) == 0 {
			t.Error("product detail decoded without a pack size")
		}
	})

	t.Run("Taxonomy", func(t *testing.T) {
		nodes, err := c.Taxonomy(ctx)
		if err != nil {
			t.Fatalf("Taxonomy: %v", err)
		}
		if len(nodes) == 0 {
			t.Fatal("empty taxonomy")
		}
		for _, n := range nodes {
			if !IsFacet(n.ID) {
				t.Errorf("taxonomy node %q has id %q, which is not a facet", n.Name, n.ID)
			}
		}
	})

	t.Run("Category", func(t *testing.T) {
		listing, err := c.Category(ctx, EncodeFacet("Fresh Food"), 1, 5)
		if err != nil {
			t.Fatalf("Category: %v", err)
		}
		products := listing.Products
		if len(products) == 0 {
			t.Fatal("no products in Fresh Food — either the operation or the facet encoding has drifted")
		}
	})

	t.Run("SurplusTrimmed", func(t *testing.T) {
		listing, err := c.Search(ctx, "bread", 1, 3)
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		products := listing.Products
		if len(products) > 3 {
			t.Errorf("asked for 3, got %d — the surplus trim is not working", len(products))
		}
	})
}

// TestLiveAuthenticatedOperationsValidate checks the operations that need a
// session without having one. Each must fail with an auth error, proving it
// passed validation. A "Cannot query field" failure means Tesco renamed
// something and the operation needs updating.
func TestLiveAuthenticatedOperationsValidate(t *testing.T) {
	c := liveClient()
	ctx := context.Background()

	ops := map[string]func() error{
		"Basket":     func() error { _, err := c.Basket(ctx); return err },
		"Favourites": func() error { _, err := c.Favourites(ctx, 1, 5); return err },
		"Orders":     func() error { _, err := c.Orders(ctx, PreviousOrders, 1, 5); return err },
		"Order":      func() error { _, err := c.Order(ctx, "1"); return err },
		"Slots":      func() error { _, err := c.Slots(ctx, time.Now(), time.Now().Add(7*24*time.Hour)); return err },
		"SetQuantity": func() error {
			_, err := c.SetQuantity(ctx, "254656543", 0, "trn:tesco:order:uuid:placeholder")
			return err
		},
	}

	for name, run := range ops {
		t.Run(name, func(t *testing.T) {
			err := run()
			if err == nil {
				t.Fatal("succeeded without a session, which should be impossible")
			}
			if IsAuthExpired(err) {
				return // Validated, then correctly rejected for auth.
			}
			if strings.Contains(err.Error(), "Cannot query field") {
				t.Fatalf("operation no longer matches Tesco's schema: %v", err)
			}
			t.Fatalf("expected an auth failure, got: %v", err)
		})
	}
}
