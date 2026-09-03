package tesco

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultEndpoint is Tesco's internal GraphQL gateway.
	DefaultEndpoint = "https://xapi.tesco.com/"

	// DefaultAPIKey is the public key baked into Tesco's web bundles. It is not
	// a secret, but it does rotate — override it when Tesco starts returning
	// 403 "Invalid Client".
	DefaultAPIKey = "TvOSZJHlEk0pjniDGQFAc9Q59WGAR4dA"

	defaultUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
		"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/147.0.0.0 Safari/537.36"

	defaultThrottle = time.Second

	// CheckoutURL is where a filled basket is paid for. Checkout is a separate
	// CSRF-protected app with 3-D Secure and is deliberately not automated.
	CheckoutURL = "https://www.tesco.com/checkout/en-GB/groceries/order-summary?basketType=GROCERY"

	// productURLPrefix is where a product lives on tesco.com. The path takes the
	// TPNC, the same id every tescoctl command uses.
	productURLPrefix = "https://www.tesco.com/groceries/en-GB/products/"
)

// ProductURL is the tesco.com page for a product, or "" for an empty tpnc.
// It is the place to go for what tescoctl cannot do — catchweight pickers,
// substitutions, and checkout.
func ProductURL(tpnc string) string {
	if tpnc == "" {
		return ""
	}
	return productURLPrefix + tpnc
}

// Auth is the credential set for authenticated operations, harvested from a
// signed-in browser session.
type Auth struct {
	AccessToken  string
	CustomerUUID string
	Cookie       string
}

// Options configures a Client. The zero value is usable; every field has a
// sensible default.
type Options struct {
	Endpoint  string
	APIKey    string
	UserAgent string

	// Throttle is the minimum gap between requests. Requests are gated
	// serially, so concurrent callers are spaced rather than bursting.
	Throttle time.Duration

	HTTP *http.Client

	// Auth returns the current credentials, or nil to call anonymously.
	Auth func() *Auth

	// Refresh renews credentials after a 401 and reports whether it succeeded.
	// Nil means a 401 is terminal.
	Refresh func(context.Context) (bool, error)
}

// Client talks to Tesco's GraphQL gateway.
type Client struct {
	endpoint  string
	apiKey    string
	userAgent string
	throttle  time.Duration
	http      *http.Client
	auth      func() *Auth
	refresh   func(context.Context) (bool, error)

	mu          sync.Mutex
	nextAllowed time.Time
}

// New builds a Client from opts.
func New(opts Options) *Client {
	c := &Client{
		endpoint:  cmpOr(opts.Endpoint, DefaultEndpoint),
		apiKey:    cmpOr(opts.APIKey, DefaultAPIKey),
		userAgent: cmpOr(opts.UserAgent, defaultUserAgent),
		throttle:  opts.Throttle,
		http:      opts.HTTP,
		auth:      opts.Auth,
		refresh:   opts.Refresh,
	}
	if c.throttle <= 0 {
		c.throttle = defaultThrottle
	}
	if c.http == nil {
		c.http = &http.Client{Timeout: 30 * time.Second}
	}
	return c
}

func cmpOr(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// op is a single GraphQL operation plus the micro-frontend tag the gateway
// routes on.
type op struct {
	Name  string
	Query string
	MFE   string
	Vars  map[string]any
}

// gqlError is one operation-level error from the response envelope. Tesco
// reports auth failures here with HTTP 200 on the outer response, so the
// in-envelope status matters as much as the transport one.
type gqlError struct {
	Message    string `json:"message"`
	Path       []any  `json:"path"`
	Extensions struct {
		Code string `json:"code"`
		HTTP struct {
			Status int `json:"status"`
		} `json:"http"`
	} `json:"extensions"`
}

type envelope struct {
	Data   json.RawMessage `json:"data"`
	Errors []gqlError      `json:"errors"`
	Status int             `json:"status"`
}

// exec runs one operation and decodes its data into out. It retries once after
// a successful credential refresh, and never retries a 403 or 429.
func (c *Client) exec(ctx context.Context, o op, out any) error {
	for attempt := 0; attempt < 2; attempt++ {
		env, err := c.do(ctx, o)
		if err != nil {
			return err
		}

		if unauthorized(env) {
			if attempt == 0 && c.refresh != nil {
				ok, rerr := c.refresh(ctx)
				if rerr != nil {
					return fmt.Errorf("refreshing tesco session for %s: %w", o.Name, rerr)
				}
				if ok {
					continue
				}
			}
			return &AuthExpiredError{Op: o.Name}
		}
		if len(env.Errors) > 0 {
			return &GraphQLError{Op: o.Name, Errors: env.Errors}
		}
		if out != nil && len(env.Data) > 0 {
			if err := json.Unmarshal(env.Data, out); err != nil {
				return fmt.Errorf("decoding tesco %s response: %w", o.Name, err)
			}
		}
		return nil
	}
	return &AuthExpiredError{Op: o.Name}
}

// do performs one HTTP round trip, translating transport-level failures into
// the error taxonomy and returning the unwrapped envelope.
func (c *Client) do(ctx context.Context, o op) (envelope, error) {
	if err := c.wait(ctx); err != nil {
		return envelope{}, err
	}

	// Tesco expects a JSON array of operations, each carrying its mfe tag.
	body, err := json.Marshal([]map[string]any{{
		"operationName": o.Name,
		"variables":     o.Vars,
		"extensions":    map[string]any{"mfeName": o.MFE},
		"query":         o.Query,
	}})
	if err != nil {
		return envelope{}, fmt.Errorf("encoding tesco %s request: %w", o.Name, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return envelope{}, fmt.Errorf("building tesco %s request: %w", o.Name, err)
	}
	c.setHeaders(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return envelope{}, fmt.Errorf("calling tesco %s: %w", o.Name, err)
	}
	defer resp.Body.Close()

	// Read once: non-2xx bodies are frequently plain text, not JSON.
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return envelope{}, fmt.Errorf("reading tesco %s response: %w", o.Name, err)
	}

	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		// Surface as an in-envelope 401 so exec's refresh path handles it.
		return envelope{Errors: []gqlError{synthetic401()}}, nil
	case resp.StatusCode == http.StatusForbidden:
		if strings.Contains(strings.ToLower(string(raw)), "invalid client") {
			return envelope{}, &APIKeyError{Op: o.Name}
		}
		return envelope{}, &RateLimitedError{Op: o.Name, Status: resp.StatusCode}
	case resp.StatusCode == http.StatusTooManyRequests:
		return envelope{}, &RateLimitedError{Op: o.Name, Status: resp.StatusCode}
	}

	// The response mirrors the request: an array, one entry per operation.
	var batch []envelope
	if err := json.Unmarshal(raw, &batch); err != nil {
		var single envelope
		if err2 := json.Unmarshal(raw, &single); err2 != nil {
			return envelope{}, fmt.Errorf("tesco %s returned HTTP %d with an unparseable body: %s",
				o.Name, resp.StatusCode, snippet(raw))
		}
		return single, nil
	}
	if len(batch) == 0 {
		return envelope{}, fmt.Errorf("tesco %s returned an empty batch", o.Name)
	}
	return batch[0], nil
}

func (c *Client) setHeaders(req *http.Request) {
	h := req.Header
	h.Set("content-type", "application/json")
	h.Set("accept", "application/json")
	h.Set("accept-language", "en-GB")
	h.Set("language", "en-GB")
	h.Set("region", "UK")
	h.Set("x-apikey", c.apiKey)
	h.Set("user-agent", c.userAgent)
	h.Set("traceid", uuid()+":"+uuid())
	h.Set("trkid", uuid())

	if c.auth == nil {
		return
	}
	if a := c.auth(); a != nil {
		h.Set("authorization", "Bearer "+a.AccessToken)
		h.Set("customer-uuid", a.CustomerUUID)
		if a.Cookie != "" {
			h.Set("cookie", a.Cookie)
		}
	}
}

// wait hands out request slots serially so that N concurrent callers are spaced
// throttle apart rather than all firing at once. The lock is not held across
// the sleep.
func (c *Client) wait(ctx context.Context) error {
	c.mu.Lock()
	slot := c.nextAllowed
	if now := time.Now(); slot.Before(now) {
		slot = now
	}
	c.nextAllowed = slot.Add(c.throttle)
	c.mu.Unlock()

	delay := time.Until(slot)
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// unauthorized reports whether the envelope represents an auth failure. Tesco
// returns HTTP 200 with the failure buried in the envelope, and its services do
// not agree on how to report it: basket and favourites answer "Unauthorized"
// with extensions.http.status 401, while orders and slots answer "A token was
// expected, but not defined" with no status at all and a misleading
// INTERNAL_SERVER_ERROR code. Both mean the same thing.
func unauthorized(env envelope) bool {
	if env.Status == http.StatusUnauthorized {
		return true
	}
	for _, e := range env.Errors {
		if e.Extensions.HTTP.Status == http.StatusUnauthorized {
			return true
		}
		msg := strings.ToLower(e.Message)
		if strings.Contains(msg, "unauthor") || strings.Contains(msg, "token was expected") {
			return true
		}
	}
	return false
}

func synthetic401() gqlError {
	var e gqlError
	e.Message = "Unauthorized"
	e.Extensions.HTTP.Status = http.StatusUnauthorized
	return e
}

func snippet(b []byte) string {
	const max = 200
	s := strings.TrimSpace(string(b))
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}

func uuid() string {
	var b [16]byte
	// crypto/rand.Read never fails on supported platforms.
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	h := hex.EncodeToString(b[:])
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}
