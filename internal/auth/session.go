// Package auth handles the Tesco session: harvesting it from a signed-in
// browser, persisting it, and turning it into request credentials.
//
// The whole session is cookies. The access token lasts an hour, but the jar
// also carries a refresh token good for thirty days, which refresh.go redeems
// over plain HTTP — so a human is needed roughly monthly, not hourly.
package auth

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strings"
	"time"
)

// Cookie names worth keeping: the OAuth pair that authenticates the call, and
// the Akamai cookies that keep the request from being challenged.
var (
	cookiePrefixes = []string{"OAuth.", "UUID", "bm_", "_abck"}
	cookieNames    = []string{"_pxhd", "ak_bmsc"}
)

// accessTokenCookie holds the bearer token; customerCookie identifies the
// account. Both must be present for a session to be usable.
//
// refreshTokenCookie redeems a new access token without a human (see
// refresh.go), and tokensExpiryCookie is Tesco's own statement of when each of
// the two dies.
const (
	accessTokenCookie  = "OAuth.AccessToken"
	customerCookie     = "UUID"
	refreshTokenCookie = "OAuth.RefreshToken"
	tokensExpiryCookie = "OAuth.TokensExpiryTime"
)

// Session is a harvested Tesco browser session.
type Session struct {
	AccessToken  string            `json:"accessToken"`
	CustomerUUID string            `json:"customerUuid"`
	Cookies      map[string]string `json:"cookies"`
	ExpiresAt    time.Time         `json:"expiresAt,omitzero"`
	HarvestedAt  time.Time         `json:"harvestedAt"`
}

// RawCookie is the minimum shape needed from any cookie source — chromedp,
// a browser extension export, or hand-written JSON.
type RawCookie struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Keep reports whether a cookie is one we need to replay.
func Keep(name string) bool {
	if slices.Contains(cookieNames, name) {
		return true
	}
	for _, prefix := range cookiePrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// FromCookies builds a Session from a browser's cookie list, keeping only the
// cookies that matter. It fails when sign-in has not completed.
func FromCookies(cookies []RawCookie) (*Session, error) {
	jar := make(map[string]string)
	for _, c := range cookies {
		if Keep(c.Name) {
			jar[c.Name] = c.Value
		}
	}

	token, uuid := jar[accessTokenCookie], jar[customerCookie]
	if token == "" || uuid == "" {
		return nil, fmt.Errorf("harvested %d tesco cookies but not %s and %s — sign-in did not complete",
			len(jar), accessTokenCookie, customerCookie)
	}

	s := &Session{
		AccessToken:  token,
		CustomerUUID: uuid,
		Cookies:      jar,
		HarvestedAt:  time.Now(),
	}
	if exp, ok := jwtExpiry(token); ok {
		s.ExpiresAt = exp
	}
	return s, nil
}

// CookieHeader serialises the jar for the Cookie request header. Names are
// sorted so the header is stable across calls, which makes traffic easier to
// compare when debugging.
func (s *Session) CookieHeader() string {
	if s == nil || len(s.Cookies) == 0 {
		return ""
	}
	pairs := make([]string, 0, len(s.Cookies))
	for _, name := range slices.Sorted(maps.Keys(s.Cookies)) {
		pairs = append(pairs, name+"="+s.Cookies[name])
	}
	return strings.Join(pairs, "; ")
}

// Expired reports whether the access token is gone or within skew of expiry.
// A session with no parseable expiry is treated as live: better to attempt the
// call and handle a 401 than to refuse on a guess.
func (s *Session) Expired(skew time.Duration) bool {
	if s == nil || s.AccessToken == "" {
		return true
	}
	if s.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().Add(skew).After(s.ExpiresAt)
}

// Renewable reports whether the session carries a refresh token that has not
// yet lapsed. A session can be renewable long after its access token has
// expired — that is the whole point of the refresh path.
func (s *Session) Renewable() bool {
	if s == nil || s.Cookies[refreshTokenCookie] == "" {
		return false
	}
	if exp, ok := s.RefreshExpiry(); ok {
		return time.Now().Before(exp)
	}
	// No stated window: assume redeemable and let Tesco be the judge, the same
	// way an unparseable access-token expiry is treated as live.
	return true
}

// RefreshExpiry reads when the refresh token dies, from the cookie in which
// Tesco states it. Tesco has been issuing thirty-day refresh tokens against
// sixty-minute access tokens, so this is the real ceiling on how long tescoctl
// can go without a human.
func (s *Session) RefreshExpiry() (time.Time, bool) {
	if s == nil {
		return time.Time{}, false
	}
	windows := tokenWindows(s.Cookies[tokensExpiryCookie])
	when, ok := windows["RefreshToken"]
	return when, ok
}

// tokenWindows decodes tokensExpiryCookie: a URL-encoded JSON object mapping
// token names to epoch milliseconds. A malformed value yields nothing rather
// than an error — it is a hint, and no decision depends on it alone.
func tokenWindows(raw string) map[string]time.Time {
	if raw == "" {
		return nil
	}
	decoded, err := url.QueryUnescape(raw)
	if err != nil {
		return nil
	}
	var ms map[string]int64
	if err := json.Unmarshal([]byte(decoded), &ms); err != nil {
		return nil
	}
	out := make(map[string]time.Time, len(ms))
	for name, epoch := range ms {
		out[name] = time.UnixMilli(epoch)
	}
	return out
}

// jwtExpiry reads the exp claim from the access token. Tesco's access token is
// a JWT, but nothing breaks if that stops being true.
func jwtExpiry(token string) (time.Time, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, false
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Exp == 0 {
		return time.Time{}, false
	}
	return time.Unix(claims.Exp, 0), true
}
