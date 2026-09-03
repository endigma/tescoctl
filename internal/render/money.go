package render

import (
	"fmt"
	"strconv"
	"strings"
)

// Money formats a nullable GBP amount. Absent prices render as an em dash
// rather than £0.00 — the distinction matters, which is why the underlying
// fields are pointers.
func Money(v *float64) string {
	if v == nil {
		return "—"
	}
	return "£" + strconv.FormatFloat(*v, 'f', 2, 64)
}

// UnitPrice formats the comparison price, e.g. "£0.73/litre". Empty when either
// half is missing.
func UnitPrice(v *float64, unit string) string {
	if v == nil || unit == "" {
		return ""
	}
	return fmt.Sprintf("£%s/%s", strconv.FormatFloat(*v, 'f', 2, 64), unit)
}

// Truncate shortens s to at most n runes, ending in an ellipsis.
func Truncate(s string, n int) string {
	runes := []rune(strings.TrimSpace(s))
	if len(runes) <= n {
		return string(runes)
	}
	if n <= 1 {
		return string(runes[:n])
	}
	return string(runes[:n-1]) + "…"
}
