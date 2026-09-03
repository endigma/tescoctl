//go:build live

// Probe: can Tesco's refresh-token endpoint be redeemed over plain HTTP, with
// no browser at all?
//
//	go test -tags live -run TestLiveHTTPRefresh -v ./internal/auth/
//
// The stored session already carries OAuth.RefreshToken (Tesco marks it valid
// for 30 days) alongside the 60-minute OAuth.AccessToken. basketeer asserts a
// pure-HTTP refresh is rejected by Akamai and drives a headed Chrome instead —
// but it never replayed the _abck/bm_* cookies that Akamai actually checks,
// which grosh does keep. This test settles which is true.
//
// It does not write to the store. A rotated token is reported, not saved, so a
// failed probe cannot strand the real session.
package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"testing"
	"time"
)

// fingerprint identifies a credential across a rotation without printing it.
func fingerprint(v string) string {
	if v == "" {
		return "<absent>"
	}
	sum := sha256.Sum256([]byte(v))
	return hex.EncodeToString(sum[:])[:8]
}

// tokenWindows decodes OAuth.TokensExpiryTime, the cookie in which Tesco states
// when each token dies. Reading it is how we learn whether the refresh token's
// 30-day window is rolling or fixed.
func tokenWindows(raw string) map[string]time.Time {
	decoded, err := url.QueryUnescape(raw)
	if err != nil {
		return nil
	}
	var ms map[string]int64
	if err := json.Unmarshal([]byte(decoded), &ms); err != nil {
		return nil
	}
	out := make(map[string]time.Time, len(ms))
	for k, v := range ms {
		out[k] = time.UnixMilli(v)
	}
	return out
}

// refreshURL forces a rotation even while the current token is live
// (soft-refresh=false), then redirects to `from` once the new token is written.
const refreshURL = "https://www.tesco.com/account/auth/en-GB/refresh-token?soft-refresh=false" +
	"&from=https%3A%2F%2Fwww.tesco.com%2Fshop%2Fen-GB%2Flanding%2Fgroceries"

const probeUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
	"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/147.0.0.0 Safari/537.36"

func TestLiveHTTPRefresh(t *testing.T) {
	store, err := NewStore()
	if err != nil {
		t.Fatalf("locating store: %v", err)
	}
	session, err := store.Load()
	if err != nil {
		t.Fatalf("loading session: %v", err)
	}

	before := session.Cookies[accessTokenCookie]
	if before == "" {
		t.Fatal("stored session has no access token")
	}
	if rt := session.Cookies["OAuth.RefreshToken"]; rt == "" {
		t.Fatal("stored session has no OAuth.RefreshToken — nothing to redeem")
	}
	t.Logf("access token expires %s (in %s)",
		session.ExpiresAt.Format(time.RFC3339), time.Until(session.ExpiresAt).Round(time.Second))

	// Fingerprint the refresh credential so a rotation is visible without
	// putting the secret in test output.
	refreshBefore := session.Cookies["OAuth.RefreshToken"]
	sidBefore := session.Cookies["OAuth.Sid"]
	t.Logf("before: refresh=%s sid=%s", fingerprint(refreshBefore), fingerprint(sidBefore))
	for name, when := range tokenWindows(session.Cookies["OAuth.TokensExpiryTime"]) {
		t.Logf("  window %s: %s (in %s)", name, when.Format(time.RFC3339), time.Until(when).Round(time.Minute))
	}

	// Seed a jar with every cookie grosh keeps, including the Akamai set.
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("building jar: %v", err)
	}
	site, _ := url.Parse("https://www.tesco.com/")
	var seeded []*http.Cookie
	for name, value := range session.Cookies {
		seeded = append(seeded, &http.Cookie{Name: name, Value: value, Domain: ".tesco.com", Path: "/"})
	}
	jar.SetCookies(site, seeded)
	t.Logf("seeded %d cookies", len(seeded))

	// Record the redirect chain: where it lands is the diagnosis. A hop through
	// /account/login means the refresh token itself is dead.
	var chain []string
	client := &http.Client{
		Jar:     jar,
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			chain = append(chain, req.URL.String())
			if len(via) >= 15 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}

	req, err := http.NewRequest(http.MethodGet, refreshURL, nil)
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	req.Header.Set("user-agent", probeUserAgent)
	req.Header.Set("accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("accept-language", "en-GB,en;q=0.9")
	req.Header.Set("upgrade-insecure-requests", "1")
	req.Header.Set("sec-fetch-dest", "document")
	req.Header.Set("sec-fetch-mode", "navigate")
	req.Header.Set("sec-fetch-site", "same-origin")
	req.Header.Set("sec-fetch-user", "?1")
	req.Header.Set("referer", "https://www.tesco.com/groceries/en-GB/")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("refresh request: %v", err)
	}
	defer resp.Body.Close()

	t.Logf("final status: %s", resp.Status)
	t.Logf("final url:    %s", resp.Request.URL.String())
	for i, hop := range chain {
		t.Logf("  redirect %d: %s", i+1, hop)
	}

	// Read the jar back. Every cookie is collected, not just the ones grosh
	// currently keeps: a refresh may mint cookies Keep() would discard, and
	// discarding one the next refresh needs is how this design strands itself.
	jarNow := make(map[string]string)
	var names, dropped []string
	for _, c := range jar.Cookies(site) {
		names = append(names, c.Name)
		jarNow[c.Name] = c.Value
		if !Keep(c.Name) {
			dropped = append(dropped, c.Name)
		}
	}
	t.Logf("jar after: %v", names)
	if len(dropped) > 0 {
		t.Logf("NOTE: Keep() would discard %v", dropped)
	}

	after := jarNow[accessTokenCookie]
	switch {
	case after == "":
		t.Fatalf("FAIL: no %s in the jar after the refresh", accessTokenCookie)
	case after == before:
		t.Fatalf("FAIL: %s did not rotate — Akamai most likely rejected the HTTP refresh", accessTokenCookie)
	}
	t.Logf("SUCCESS: access token rotated over plain HTTP")
	if exp, ok := jwtExpiry(after); ok {
		t.Logf("  new expiry: %s (in %s)", exp.Format(time.RFC3339), time.Until(exp).Round(time.Second))
	}

	// The question that decides the implementation: does the refresh credential
	// itself rotate? If it does, a refresh that is not persisted burns it, and
	// the stored session is dead the moment the access token lapses.
	refreshAfter, sidAfter := jarNow["OAuth.RefreshToken"], jarNow["OAuth.Sid"]
	t.Logf("after:  refresh=%s sid=%s", fingerprint(refreshAfter), fingerprint(sidAfter))
	switch {
	case refreshAfter == "":
		t.Errorf("ROTATES: the refresh token is GONE from the jar — persisting is mandatory")
	case refreshAfter != refreshBefore:
		t.Logf("ROTATES: refresh token changed — grosh MUST save the whole jar after every refresh")
	default:
		t.Logf("STABLE: refresh token unchanged — the stored one stays redeemable")
	}
	if sidAfter != sidBefore {
		t.Logf("  (OAuth.Sid also rotated)")
	}

	// Rolling or fixed? A window that slid forward means renewal is indefinite
	// so long as grosh refreshes at least once every 30 days.
	windows := tokenWindows(jarNow["OAuth.TokensExpiryTime"])
	oldWindows := tokenWindows(session.Cookies["OAuth.TokensExpiryTime"])
	for name, when := range windows {
		t.Logf("  window %s: %s (in %s)", name, when.Format(time.RFC3339), time.Until(when).Round(time.Minute))
		if was, ok := oldWindows[name]; ok && when.After(was) {
			t.Logf("    ROLLING: slid forward by %s", when.Sub(was).Round(time.Second))
		} else if ok && when.Equal(was) {
			t.Logf("    FIXED: unchanged by this refresh")
		}
	}

	// Persist only on request. The probe defaults to read-only so a failed run
	// cannot damage the real session; with rotation confirmed, saving is how
	// the burned credential gets replaced by the live one.
	if os.Getenv("GROSH_PROBE_SAVE") != "1" {
		t.Logf("NOT SAVED (set GROSH_PROBE_SAVE=1 to persist the rotated session)")
		return
	}
	renewed, err := FromCookies(rawFrom(jarNow))
	if err != nil {
		t.Fatalf("rebuilding session from refreshed jar: %v", err)
	}
	if err := store.Save(renewed); err != nil {
		t.Fatalf("saving refreshed session: %v", err)
	}
	t.Logf("SAVED to %s", store.Path)
}

func rawFrom(jar map[string]string) []RawCookie {
	out := make([]RawCookie, 0, len(jar))
	for name, value := range jar {
		out = append(out, RawCookie{Name: name, Value: value})
	}
	return out
}
