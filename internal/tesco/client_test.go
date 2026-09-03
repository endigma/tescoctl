package tesco

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// newTestClient wires a Client to a stub server with throttling disabled so the
// tests do not pay the politeness delay.
func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return New(Options{Endpoint: srv.URL, Throttle: time.Nanosecond})
}

// TestRequestEnvelope pins the wire format: Tesco wants an array of operations,
// each carrying its mfe tag, plus a fixed header set.
func TestRequestEnvelope(t *testing.T) {
	var got []map[string]any
	var headers http.Header

	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		headers = r.Header.Clone()
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Errorf("request body is not a JSON array: %v", err)
		}
		io.WriteString(w, `[{"data":{"search":{"results":[]}}}]`)
	})

	if _, err := c.Search(context.Background(), "milk", 1, 5); err != nil {
		t.Fatalf("Search: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("want a 1-element operation array, got %d elements", len(got))
	}
	if got[0]["operationName"] != "Search" {
		t.Errorf("operationName = %v, want Search", got[0]["operationName"])
	}
	ext, ok := got[0]["extensions"].(map[string]any)
	if !ok || ext["mfeName"] != mfeSearch {
		t.Errorf("extensions.mfeName = %v, want %q", got[0]["extensions"], mfeSearch)
	}
	for header, want := range map[string]string{
		"X-Apikey": DefaultAPIKey,
		"Region":   "UK",
		"Language": "en-GB",
	} {
		if headers.Get(header) != want {
			t.Errorf("header %s = %q, want %q", header, headers.Get(header), want)
		}
	}
	if headers.Get("Traceid") == "" || headers.Get("Trkid") == "" {
		t.Error("traceid/trkid headers should be set on every request")
	}
	if headers.Get("Authorization") != "" {
		t.Error("anonymous calls must not send an Authorization header")
	}
}

// TestEnvelope401 covers the case that actually bites: Tesco answers HTTP 200
// and hides the 401 inside the response envelope.
func TestEnvelope401(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `[{"errors":[{"message":"Unauthorized","extensions":{"http":{"status":401}}}],"data":{"basket":null},"status":401}]`)
	})

	_, err := c.Search(context.Background(), "milk", 1, 5)
	if !IsAuthExpired(err) {
		t.Fatalf("want AuthExpiredError, got %v", err)
	}
}

// TestRefreshRetriedOnce checks that a 401 triggers exactly one refresh and one
// retry, not a loop.
func TestRefreshRetriedOnce(t *testing.T) {
	var calls, refreshes int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			io.WriteString(w, `[{"errors":[{"message":"Unauthorized","extensions":{"http":{"status":401}}}]}]`)
			return
		}
		io.WriteString(w, `[{"data":{"search":{"results":[{"node":{"tpnc":"1","title":"Milk"}}]}}}]`)
	}))
	t.Cleanup(srv.Close)

	c := New(Options{
		Endpoint: srv.URL,
		Throttle: time.Nanosecond,
		Refresh: func(context.Context) (bool, error) {
			refreshes++
			return true, nil
		},
	})

	listing, err := c.Search(context.Background(), "milk", 1, 5)
	if err != nil {
		t.Fatalf("Search after refresh: %v", err)
	}
	products := listing.Products
	if len(products) != 1 {
		t.Fatalf("want 1 product after retry, got %d", len(products))
	}
	if refreshes != 1 {
		t.Errorf("refreshes = %d, want exactly 1", refreshes)
	}
	if calls != 2 {
		t.Errorf("HTTP calls = %d, want exactly 2", calls)
	}
}

// TestAPIKeyRejection distinguishes a rotated key from a bot block; both are
// 403 but only one is actionable by the user.
func TestAPIKeyRejection(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		io.WriteString(w, "Invalid Client")
	})

	_, err := c.Search(context.Background(), "milk", 1, 5)
	var keyErr *APIKeyError
	if !errors.As(err, &keyErr) {
		t.Fatalf("want APIKeyError, got %v", err)
	}
}

// TestRateLimitNotRetried is the important half of the politeness contract: a
// 429 must cost exactly one request, never a retry storm.
func TestRateLimitNotRetried(t *testing.T) {
	var calls int
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusTooManyRequests)
	})

	_, err := c.Search(context.Background(), "milk", 1, 5)
	var limited *RateLimitedError
	if !errors.As(err, &limited) {
		t.Fatalf("want RateLimitedError, got %v", err)
	}
	if calls != 1 {
		t.Errorf("HTTP calls = %d, want 1 — a 429 must not be retried", calls)
	}
}

// TestSurplusResultsTrimmed pins the workaround for Tesco returning more nodes
// than the requested count.
func TestSurplusResultsTrimmed(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `[{"data":{"search":{"results":[
			{"node":{"tpnc":"1","title":"a"}},
			{"node":{"tpnc":"2","title":"b"}},
			{"node":{"tpnc":"3","title":"c"}},
			{"node":{"tpnc":"4","title":"d"}}
		]}}}]`)
	})

	listing, err := c.Search(context.Background(), "milk", 1, 2)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	products := listing.Products
	if len(products) != 2 {
		t.Errorf("got %d products, want 2 — surplus nodes should be trimmed", len(products))
	}
}

// TestNonProductNodesSkipped covers sponsored placements, which do not match
// the ProductType fragment and decode to a zero Product.
func TestNonProductNodesSkipped(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `[{"data":{"search":{"results":[
			{"node":{"__typename":"SponsoredType"}},
			{"node":{"tpnc":"1","title":"Milk"}}
		]}}}]`)
	})

	listing, err := c.Search(context.Background(), "milk", 1, 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	products := listing.Products
	if len(products) != 1 || products[0].Title != "Milk" {
		t.Errorf("got %+v, want only the product node", products)
	}
}

// TestAbsentPriceStaysNil is why the price fields are pointers: a missing price
// must not silently read as £0.00.
func TestAbsentPriceStaysNil(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `[{"data":{"search":{"results":[
			{"node":{"tpnc":"1","title":"Milk","price":{"actual":null,"unitPrice":0.73}}}
		]}}}]`)
	})

	listing, err := c.Search(context.Background(), "milk", 1, 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	products := listing.Products
	if products[0].Price.Actual != nil {
		t.Errorf("absent price decoded to %v, want nil", *products[0].Price.Actual)
	}
	if products[0].Price.UnitPrice == nil || *products[0].Price.UnitPrice != 0.73 {
		t.Error("a present price should still decode")
	}
}

// TestNotFoundMapped checks the friendlier error for an unknown TPNC.
func TestNotFoundMapped(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `[{"errors":[{"message":"product-not-found"}],"data":{"product":null}}]`)
	})

	_, err := c.Product(context.Background(), "000000000")
	var nf *NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("want NotFoundError, got %v", err)
	}
}

// TestThrottleSpacesRequests checks the politeness gate actually delays.
func TestThrottleSpacesRequests(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `[{"data":{"search":{"results":[]}}}]`)
	}))
	t.Cleanup(srv.Close)

	c := New(Options{Endpoint: srv.URL, Throttle: 50 * time.Millisecond})
	ctx := context.Background()

	start := time.Now()
	for range 3 {
		if _, err := c.Search(ctx, "milk", 1, 1); err != nil {
			t.Fatalf("Search: %v", err)
		}
	}
	// Three requests means two gaps.
	if elapsed := time.Since(start); elapsed < 100*time.Millisecond {
		t.Errorf("3 requests took %v, want at least 100ms of throttling", elapsed)
	}
}

// TestTokenExpectedIsAuthFailure covers the orders/slots services, which report
// a missing session as "A token was expected, but not defined" with no HTTP
// status and an INTERNAL_SERVER_ERROR code. Without this the user would see a
// raw GraphQL error instead of being told to log in.
func TestTokenExpectedIsAuthFailure(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `[{"errors":[{"message":"A token was expected, but not defined","path":["orderSearch"],"extensions":{"code":"INTERNAL_SERVER_ERROR","serviceName":"orders"}}],"data":{"orderSearch":null},"status":200}]`)
	})

	_, err := c.Orders(context.Background(), PreviousOrders, 1, 5)
	if !IsAuthExpired(err) {
		t.Fatalf("want AuthExpiredError, got %v", err)
	}
}
