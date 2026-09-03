// Package auth handles the Tesco session: harvesting it from a signed-in
// browser, persisting it, and turning it into request credentials.
//
// The whole session is cookies. Tesco has no token-exchange endpoint, so
// "refreshing" means going back to a browser.
package auth

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
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
const (
	accessTokenCookie = "OAuth.AccessToken"
	customerCookie    = "UUID"
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
