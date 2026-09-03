package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
)

// ErrLoginRequired reports that the refresh token is spent or revoked. It is
// the one failure the refresh path cannot recover from on its own: only a human
// signing in can clear it.
//
// Tesco does not announce it. The refresh call still answers 200 on the
// groceries landing page; what marks the failure is that the response clears
// the OAuth cookies instead of rotating them.
var ErrLoginRequired = errors.New("tesco refresh token is no longer valid — run `tescoctl auth login`")

const (
	// RefreshURL redeems the refresh token for a new access token.
	//
	// soft-refresh=false forces a rotation even while the current token is
	// still live, so a refresh is deterministic rather than a no-op that leaves
	// the caller unable to tell success from silence. The `from` parameter is
	// where Tesco sends the browser once the new token is written.
	RefreshURL = "https://www.tesco.com/account/auth/en-GB/refresh-token?soft-refresh=false" +
		"&from=https%3A%2F%2Fwww.tesco.com%2Fshop%2Fen-GB%2Flanding%2Fgroceries"

	// loginPath is a secondary signal that re-authentication is needed. A spent
	// refresh token usually clears the cookies instead (see Harvest), but a
	// revoked or logged-out account does redirect here, and following Tesco
	// into a form we cannot fill would only waste the request.
	loginPath = "/account/login"

	defaultRefreshUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
		"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/147.0.0.0 Safari/537.36"

	defaultRefreshTimeout = 30 * time.Second
)

// Refresher renews a session by redeeming its refresh token over plain HTTP.
//
// No browser is involved. Sign-in needs one because Akamai scrutinises a fresh,
// cookie-less client, but a refresh replays the `_abck` and `bm_*` cookies the
// original sign-in already earned, and Akamai accepts them. That is what makes
// this backend non-interactive, and what lets a session survive for the life of
// its refresh token — thirty days, in Tesco's current issuance — without a
// human.
//
// The rotated jar must be persisted. Tesco rotates the refresh token alongside
// the access token, so a refresh whose result is dropped has burned the
// credential that would have served the next one.
type Refresher struct {
	// Session is the session to renew. Required: its cookies are the whole
	// input, since the refresh token is one of them.
	Session *Session

	// HTTP overrides the client. Nil uses one with a 30s timeout.
	HTTP *http.Client

	// UserAgent overrides the browser identity presented to Akamai. Empty uses
	// the same Chrome string the rest of tescoctl sends.
	UserAgent string

	// endpoint overrides RefreshURL. It exists so the redirect and
	// no-rotation paths can be exercised against a test server; production
	// callers leave it empty.
	endpoint string
}

func (r *Refresher) Name() string { return "refresh-token" }

// Interactive is false: this is the backend that can service a silent
// mid-command renewal, which is the distinction Backend.Interactive exists to
// draw.
func (r *Refresher) Interactive() bool { return false }

// Harvest redeems the refresh token and returns the renewed session. It does
// not persist anything — the caller owns the store, and must save the result.
func (r *Refresher) Harvest(ctx context.Context) (*Session, error) {
	if r.Session == nil {
		return nil, ErrNoSession
	}
	if r.Session.Cookies[refreshTokenCookie] == "" {
		return nil, fmt.Errorf("stored session has no %s — it predates refresh support, so run `tescoctl auth login` once more",
			refreshTokenCookie)
	}
	if exp, ok := r.Session.RefreshExpiry(); ok && !time.Now().Before(exp) {
		return nil, fmt.Errorf("%w (it lapsed at %s)", ErrLoginRequired, exp.Local().Format(time.RFC1123))
	}

	before := r.Session.Cookies[accessTokenCookie]

	target := r.endpoint
	if target == "" {
		target = RefreshURL
	}
	site, err := url.Parse(target)
	if err != nil {
		return nil, fmt.Errorf("parsing refresh endpoint: %w", err)
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("building cookie jar: %w", err)
	}
	// Seed against tesco.com as a domain cookie, the way the browser held them,
	// so they survive a redirect to another tesco.com host. A test server on a
	// different host gets host-only cookies, which the jar accepts.
	domain := ""
	if strings.HasSuffix(site.Hostname(), "tesco.com") {
		domain = ".tesco.com"
	}
	seeded := make([]*http.Cookie, 0, len(r.Session.Cookies))
	for name, value := range r.Session.Cookies {
		seeded = append(seeded, &http.Cookie{Name: name, Value: value, Domain: domain, Path: "/"})
	}
	jar.SetCookies(site, seeded)

	// Stop at the sign-in page rather than following Tesco into a form we
	// cannot fill. This is the secondary re-authentication signal; the one that
	// fires in practice is the cookie clearing checked after the response.
	var sawLogin bool
	client := r.httpClient()
	client.Jar = jar
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if strings.HasPrefix(req.URL.Path, loginPath) {
			sawLogin = true
			return http.ErrUseLastResponse
		}
		if len(via) >= 15 {
			return errors.New("too many redirects")
		}
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("building refresh request: %w", err)
	}
	r.setHeaders(req)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("refreshing tesco session: %w", err)
	}
	defer resp.Body.Close()

	if sawLogin || strings.HasPrefix(resp.Request.URL.Path, loginPath) {
		return nil, ErrLoginRequired
	}

	// Read the jar rather than the response body: the point of the call is the
	// Set-Cookie headers, and the jar has already merged them over the cookies
	// we seeded, so cookies Tesco did not resend are preserved.
	raw := make([]RawCookie, 0, len(r.Session.Cookies))
	present := make(map[string]bool, len(raw))
	for _, c := range jar.Cookies(site) {
		raw = append(raw, RawCookie{Name: c.Name, Value: c.Value})
		present[c.Name] = true
	}

	// A spent refresh token does not redirect to the sign-in page. Tesco
	// answers 200 on the landing page, exactly as it does on success, and
	// signals the failure by *clearing* the credentials: OAuth.AccessToken,
	// OAuth.RefreshToken and UUID come back deleted, while OAuth.Sid and the
	// Akamai cookies survive. Verified against a deliberately burned token —
	// this, not the redirect, is the check that catches re-authentication.
	if !present[accessTokenCookie] || !present[refreshTokenCookie] {
		return nil, fmt.Errorf("%w (tesco cleared the session cookies rather than rotating them, "+
			"which means the stored refresh token had already been redeemed)", ErrLoginRequired)
	}

	renewed, err := FromCookies(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: the refresh returned no usable session (%v)", ErrLoginRequired, err)
	}
	// A token that did not rotate means the refresh did not happen, whatever
	// the status code said. Returning it would report success and leave the
	// caller retrying the same dead credential.
	if renewed.AccessToken == before {
		return nil, fmt.Errorf("tesco accepted the refresh (HTTP %d, landed on %s) but did not rotate the access token",
			resp.StatusCode, resp.Request.URL)
	}
	return renewed, nil
}

func (r *Refresher) httpClient() *http.Client {
	if r.HTTP == nil {
		return &http.Client{Timeout: defaultRefreshTimeout}
	}
	// Copy: CheckRedirect and Jar are set per-refresh, and mutating a client
	// the caller owns would leak that into their other requests.
	c := *r.HTTP
	return &c
}

// setHeaders presents the request as the page navigation Tesco expects. Akamai
// scores the header set, so a bare Go request is more likely to be challenged
// than one that looks like the redirect a browser would have followed.
func (r *Refresher) setHeaders(req *http.Request) {
	ua := r.UserAgent
	if ua == "" {
		ua = defaultRefreshUA
	}
	h := req.Header
	h.Set("user-agent", ua)
	h.Set("accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	h.Set("accept-language", "en-GB,en;q=0.9")
	h.Set("upgrade-insecure-requests", "1")
	h.Set("sec-fetch-dest", "document")
	h.Set("sec-fetch-mode", "navigate")
	h.Set("sec-fetch-site", "same-origin")
	h.Set("sec-fetch-user", "?1")
	h.Set("referer", "https://www.tesco.com/groceries/en-GB/")
}
