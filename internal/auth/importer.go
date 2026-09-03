package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Importer builds a session from a cookie export produced elsewhere — devtools,
// a cookie-export extension, or another tool. It is the fallback for when
// driving a browser is blocked or unwanted, and the only backend that works
// without Chrome installed.
type Importer struct {
	Src io.Reader
}

func (i *Importer) Name() string { return "import" }

// Interactive is false: the data is already there, so this backend can service
// a mid-command refresh.
func (i *Importer) Interactive() bool { return false }

func (i *Importer) Harvest(ctx context.Context) (*Session, error) {
	data, err := io.ReadAll(i.Src)
	if err != nil {
		return nil, fmt.Errorf("reading cookie export: %w", err)
	}
	cookies, err := parseCookies(data)
	if err != nil {
		return nil, err
	}
	return FromCookies(cookies)
}

// parseCookies accepts the shapes cookie exports actually arrive in:
//
//   - a JSON array of cookie objects, which is what browser extensions emit;
//   - a JSON object mapping names to values;
//   - either of those wrapped in a {"cookies": …} envelope;
//   - a raw Cookie header, "a=1; b=2", which is what you get by copying the
//     Cookie request header out of the DevTools network panel. That path
//     matters because the auth cookies are HttpOnly and so invisible to
//     document.cookie — the header is the one place they can be read without
//     an extension.
func parseCookies(data []byte) ([]RawCookie, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, errors.New("the cookie export is empty")
	}

	// A Cookie header is not JSON, so try it first when the input plainly is
	// not JSON rather than after three failed unmarshals.
	if trimmed[0] != '{' && trimmed[0] != '[' {
		return parseCookieHeader(string(trimmed))
	}

	var envelope struct {
		Cookies json.RawMessage `json:"cookies"`
	}
	if err := json.Unmarshal(trimmed, &envelope); err == nil && len(envelope.Cookies) > 0 {
		trimmed = envelope.Cookies
	}

	var list []RawCookie
	if err := json.Unmarshal(trimmed, &list); err == nil && len(list) > 0 {
		return list, nil
	}

	var jar map[string]string
	if err := json.Unmarshal(trimmed, &jar); err == nil && len(jar) > 0 {
		out := make([]RawCookie, 0, len(jar))
		for name, value := range jar {
			out = append(out, RawCookie{Name: name, Value: value})
		}
		return out, nil
	}

	return nil, errors.New("could not read the cookie export: expected a JSON array of " +
		"{\"name\",\"value\"} objects, an object mapping names to values, or a raw " +
		"\"name=value; …\" Cookie header")
}

// parseCookieHeader reads a Cookie request header. A pasted header often still
// carries its "Cookie:" label, and DevTools' "Copy value" sometimes wraps it
// across lines, so both are tolerated.
func parseCookieHeader(s string) ([]RawCookie, error) {
	s = strings.Join(strings.Fields(s), " ")
	if rest, ok := strings.CutPrefix(s, "Cookie: "); ok {
		s = rest
	} else if rest, ok := strings.CutPrefix(s, "cookie: "); ok {
		s = rest
	}

	parsed, err := http.ParseCookie(s)
	if err != nil {
		return nil, fmt.Errorf("could not read that as a Cookie header: %w", err)
	}

	out := make([]RawCookie, 0, len(parsed))
	for _, c := range parsed {
		out = append(out, RawCookie{Name: c.Name, Value: c.Value})
	}
	return out, nil
}
