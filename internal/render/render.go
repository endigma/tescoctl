// Package render handles all CLI output. Two modes: --json emits the view
// models verbatim, otherwise a lipgloss-styled human rendering. There is no TUI
// — every command prints once and exits.
package render

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/charmbracelet/lipgloss"
	"github.com/endigma/tescoctl/internal/view"
	"github.com/muesli/termenv"
)

// Renderer writes command output. Human output and diagnostics go to Err;
// under --json, Out carries JSON and nothing else.
type Renderer struct {
	JSON   bool
	Out    io.Writer
	Err    io.Writer
	Styles Styles
}

// New builds a Renderer. Colour is disabled under --json, when out is not a
// terminal, or when NO_COLOR is set, so piped output stays clean.
func New(out, errw io.Writer, asJSON bool) *Renderer {
	lg := lipgloss.NewRenderer(out)
	if asJSON || !isTerminal(out) || os.Getenv("NO_COLOR") != "" {
		lg.SetColorProfile(termenv.Ascii)
	}
	return &Renderer{JSON: asJSON, Out: out, Err: errw, Styles: newStyles(lg)}
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// Emit writes data as JSON under --json, otherwise calls human for the styled
// rendering. human is only invoked when it will be used, so it is free to be
// expensive.
func (r *Renderer) Emit(data any, human func() string) error {
	if r.JSON {
		enc := json.NewEncoder(r.Out)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		return enc.Encode(data)
	}
	s := human()
	if s == "" {
		return nil
	}
	_, err := fmt.Fprintln(r.Out, s)
	return err
}

// Fail reports err. Under --json it goes to stdout as a JSON object so that a
// consumer parsing stdout sees a structured failure; otherwise to stderr.
func (r *Renderer) Fail(err error) {
	if r.JSON {
		enc := json.NewEncoder(r.Out)
		enc.SetIndent("", "  ")
		_ = enc.Encode(view.Error{Error: err.Error()})
		return
	}
	fmt.Fprintln(r.Err, r.Styles.Error.Render("error:")+" "+err.Error())
}

// Note writes an advisory line to stderr. Suppressed under --json so stdout and
// stderr both stay machine-friendly.
func (r *Renderer) Note(format string, args ...any) {
	if r.JSON {
		return
	}
	fmt.Fprintln(r.Err, r.Styles.Muted.Render(fmt.Sprintf(format, args...)))
}
