package tesco

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
)

// Category facets are opaque ids of the form "b;" + base64(urlencoded(name)),
// e.g. "b;Q2xvdGhpbmclMjAmJTIwQWNjZXNzb3JpZXM=" decodes to
// "Clothing%20&%20Accessories". Note the inner encoding leaves "&" alone and
// escapes spaces as %20 — encodeURI semantics, not encodeURIComponent. The
// reference JS gets this wrong by base64-ing the raw name.

const facetPrefix = "b;"

// EncodeFacet builds the facet id for a department name. It is best-effort:
// prefer the ID that Taxonomy returns, which is authoritative.
func EncodeFacet(name string) string {
	return facetPrefix + base64.StdEncoding.EncodeToString([]byte(url.PathEscape(name)))
}

// DecodeFacet recovers the department name from a facet id.
func DecodeFacet(facet string) (string, error) {
	raw, ok := strings.CutPrefix(facet, facetPrefix)
	if !ok {
		return "", fmt.Errorf("facet %q does not start with %q", facet, facetPrefix)
	}
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return "", fmt.Errorf("facet %q is not valid base64: %w", facet, err)
	}
	name, err := url.PathUnescape(string(decoded))
	if err != nil {
		return "", fmt.Errorf("facet %q has an undecodable name: %w", facet, err)
	}
	return name, nil
}

// IsFacet reports whether s already looks like a facet id rather than a plain
// department name, so callers can accept either.
func IsFacet(s string) bool { return strings.HasPrefix(s, facetPrefix) }
