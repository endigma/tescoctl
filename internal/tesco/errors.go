package tesco

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// APIKeyError reports that Tesco rejected the x-apikey. The public key is baked
// into Tesco's web bundles and rotates roughly monthly, so the fix is to lift a
// fresh one and pass it via --api-key or TESCO_API_KEY.
type APIKeyError struct {
	Op string
}

func (e *APIKeyError) Error() string {
	return fmt.Sprintf("tesco rejected the API key on %s (403 invalid client) — it has most likely rotated; "+
		"lift a fresh key from tesco.com and pass --api-key or set TESCO_API_KEY", e.Op)
}

// AuthExpiredError reports that an operation needed a session and did not have a
// usable one. Either no session is stored, or the stored one is no longer valid.
type AuthExpiredError struct {
	Op string
}

func (e *AuthExpiredError) Error() string {
	return fmt.Sprintf("tesco session expired or missing (%s returned 401) — run `tescoctl auth login`", e.Op)
}

// RateLimitedError reports a 429, or a 403 that is not an API-key rejection.
// Both mean back off; neither is retried.
type RateLimitedError struct {
	Op     string
	Status int
}

func (e *RateLimitedError) Error() string {
	what := "rate limited"
	if e.Status == 403 {
		what = "blocked"
	}
	return fmt.Sprintf("tesco %s on %s (HTTP %d) — back off and try again later", what, e.Op, e.Status)
}

// GraphQLError carries operation-level errors returned in the response envelope.
type GraphQLError struct {
	Op     string
	Errors []gqlError
}

func (e *GraphQLError) Error() string {
	msgs := make([]string, 0, len(e.Errors))
	for _, err := range e.Errors {
		msgs = append(msgs, err.Message)
	}
	return fmt.Sprintf("tesco %s failed: %s", e.Op, strings.Join(msgs, "; "))
}

// isNotFound reports whether a GraphQL error is Tesco's "no such entity"
// signal, which it sends as an operation error rather than a null result.
func isNotFound(err error) bool {
	var g *GraphQLError
	if !errors.As(err, &g) {
		return false
	}
	for _, e := range g.Errors {
		if strings.Contains(strings.ToLower(e.Message), "not-found") {
			return true
		}
	}
	return false
}

// IsAuthExpired reports whether err is or wraps an AuthExpiredError.
func IsAuthExpired(err error) bool {
	var target *AuthExpiredError
	return errors.As(err, &target)
}

// NotFoundError reports that a lookup returned no such entity.
type NotFoundError struct {
	Kind string
	ID   string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("no %s found for %q", e.Kind, e.ID)
}

// FractionalQuantityError reports a refused write: a quantity that is not a
// whole number on a line sold in whole pieces.
//
// Tesco accepts such a write and reads it back happily, but its own basket page
// then fails with "Sorry, an error has occurred" and never loads — taking every
// other line with it, and naming no culprit. The only way back is to remove the
// line by tpnc, which the user cannot discover from the page that will not
// load. Refusing the write is the only safe answer.
type FractionalQuantityError struct {
	TPNC     string
	Quantity float64
	Unit     string
}

func (e *FractionalQuantityError) Error() string {
	return fmt.Sprintf("quantity %s for %s is not a whole number, and tescoctl writes basket lines in %q — "+
		"tesco accepts such a line but then cannot render the basket page at all, so this write is refused",
		formatQuantity(e.Quantity), e.TPNC, e.Unit)
}

// BasketNotUpdatedError reports that UpdateBasket returned without a GraphQL
// error but the basket it returned does not match what was asked for.
//
// Tesco silently ignores the mutation for some products — no error, no changed
// line — so a write that reports success can be a write that never happened.
type BasketNotUpdatedError struct {
	TPNC string
	Want float64
	// Got is the quantity the returned line carries, nil when the line came
	// back without one. Present says whether a line came back at all.
	Got     *float64
	Present bool
}

func (e *BasketNotUpdatedError) Error() string {
	switch {
	case !e.Present && e.Want != 0:
		return fmt.Sprintf("tesco accepted the update but %s is not in the basket it returned — "+
			"the item was not added and tesco gave no reason (some products, such as counter or "+
			"catchweight lines, are rejected silently)", e.TPNC)
	case e.Want == 0:
		return fmt.Sprintf("tesco accepted the removal but %s is still in the basket it returned%s — "+
			"the item was not removed", e.TPNC, e.gotSuffix())
	default:
		return fmt.Sprintf("tesco accepted the update but %s came back%s, not %s — "+
			"the quantity was not set", e.TPNC, e.gotSuffix(), formatQuantity(e.Want))
	}
}

// gotSuffix renders what came back, saying nothing when the line arrived
// without a quantity rather than inventing one.
func (e *BasketNotUpdatedError) gotSuffix() string {
	if e.Got == nil {
		return ""
	}
	return " at quantity " + formatQuantity(*e.Got)
}

// formatQuantity prints a quantity the way the CLI does, without a trailing
// ".00" on whole numbers.
func formatQuantity(q float64) string {
	return strconv.FormatFloat(q, 'f', -1, 64)
}

// CatchweightError reports a product tescoctl cannot add: one sold by variable
// weight rather than by the piece.
//
// Every basket write goes out as whole pieces, and a catchweight line cannot be
// expressed that way, so the gateway drops the write and reports success. This
// is the diagnosis behind an otherwise unexplained no-op; it wraps that
// BasketNotUpdatedError, which stays the machine-readable fact.
type CatchweightError struct {
	TPNC    string
	Title   string
	Weights []float64
	err     error
}

func (e *CatchweightError) Error() string {
	msg := fmt.Sprintf("%s is sold by weight, not by the piece — tesco drops a %q write for such a "+
		"product without reporting an error, so nothing was added.", describe(e.TPNC, e.Title), UnitPieces)
	if len(e.Weights) == 0 {
		return msg + " Add it on tesco.com instead"
	}
	return fmt.Sprintf("%s Add it by weight instead: --weight %s (available: %s)",
		msg, formatQuantity(e.Weights[0]), formatWeights(e.Weights))
}

func (e *CatchweightError) Unwrap() error { return e.err }

// NotCatchweightError reports a weighed write against a product sold by the
// piece. Writing a weight to such a line is the mirror of the catchweight bug:
// the gateway takes it and the basket page then breaks.
type NotCatchweightError struct {
	TPNC  string
	Title string
}

func (e *NotCatchweightError) Error() string {
	return fmt.Sprintf("%s is sold by the piece, not by weight — use --qty, not --weight",
		describe(e.TPNC, e.Title))
}

// WeightNotOfferedError reports a weight Tesco does not offer for a product.
//
// The gateway does not reject one: it writes the line, reports success, and
// tesco.com's basket page then fails to render — taking every other line with
// it and naming none of them. Refusing the write here is the only guard.
type WeightNotOfferedError struct {
	TPNC    string
	Title   string
	Weight  float64
	Offered []float64
}

func (e *WeightNotOfferedError) Error() string {
	return fmt.Sprintf("%s is not sold in %skg — tesco offers %s. Writing an unlisted weight "+
		"produces a basket tesco.com cannot display, so it was refused",
		describe(e.TPNC, e.Title), formatQuantity(e.Weight), formatWeights(e.Offered))
}

// describe names a product for an error message, by tpnc and title where known.
func describe(tpnc, title string) string {
	if title == "" {
		return tpnc
	}
	return tpnc + " (" + title + ")"
}

// formatWeights renders a weight list as "1.8kg, 1.95kg, 2.1kg".
func formatWeights(ws []float64) string {
	out := make([]string, 0, len(ws))
	for _, w := range ws {
		out = append(out, formatQuantity(w)+"kg")
	}
	return strings.Join(out, ", ")
}
