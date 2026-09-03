// Package cmd wires the CLI. Every command prints once and exits; there is no
// interactive mode.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/endigma/tescoctl/internal/auth"

	"github.com/endigma/tescoctl/internal/render"
	"github.com/endigma/tescoctl/internal/tesco"
	"github.com/urfave/cli/v3"
)

// version is overridden at build time with -ldflags "-X ...cmd.version=...".
var version = "dev"

// app is the per-invocation context: where output goes and who to ask.
type app struct {
	r       *render.Renderer
	c       *tesco.Client
	store   *auth.Store
	session *auth.Session
}

// New builds the root command. Root flags are persistent in urfave/cli v3, so
// --json and friends are available on every subcommand without re-declaring.
func New() *cli.Command {
	return &cli.Command{
		Name:    "grosh",
		Usage:   "A CLI for Tesco groceries",
		Version: version,
		Description: "grosh talks to Tesco's internal GraphQL gateway. It is not an\n" +
			"official or supported API: operations can change without notice.",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "json",
				Usage: "emit JSON on stdout instead of a table",
			},
			&cli.StringFlag{
				Name:    "api-key",
				Usage:   "override the public Tesco API key (it rotates)",
				Sources: cli.EnvVars("GROSH_API_KEY"),
				Value:   tesco.DefaultAPIKey,
			},
			&cli.DurationFlag{
				Name:  "throttle",
				Usage: "minimum gap between requests",
				Value: time.Second,
			},
			&cli.DurationFlag{
				Name:  "timeout",
				Usage: "per-request HTTP timeout",
				Value: 30 * time.Second,
			},
		},
		Commands: []*cli.Command{
			searchCmd(),
			productCmd(),
			categoriesCmd(),
			browseCmd(),
			authCmd(),
			basketCmd(),
			favouritesCmd(),
			ordersCmd(),
			orderCmd(),
			slotsCmd(),
		},
	}
}

func newApp(cmd *cli.Command) (*app, error) {
	store, err := auth.NewStore()
	if err != nil {
		return nil, err
	}

	a := &app{
		r:     render.New(os.Stdout, os.Stderr, cmd.Bool("json")),
		store: store,
	}

	// A missing session is normal: the read commands work anonymously.
	if session, err := store.Load(); err == nil {
		a.session = session
	} else if !errors.Is(err, auth.ErrNoSession) {
		return nil, err
	}

	a.c = tesco.New(tesco.Options{
		APIKey:   cmd.String("api-key"),
		Throttle: cmd.Duration("throttle"),
		HTTP:     &http.Client{Timeout: cmd.Duration("timeout")},
		Auth:     a.credentials,
	})
	return a, nil
}

// credentials feeds the client. Nil means call anonymously, which is what the
// read commands want and what an expired session degrades to.
func (a *app) credentials() *tesco.Auth {
	if a.session == nil {
		return nil
	}
	return &tesco.Auth{
		AccessToken:  a.session.AccessToken,
		CustomerUUID: a.session.CustomerUUID,
		Cookie:       a.session.CookieHeader(),
	}
}

// requireAuth fails early when a command needs a session and the stored one is
// missing or stale, so the user gets a clear instruction instead of a 401.
//
// There is no silent refresh: renewing a Tesco session means a human signing in
// to a browser, so a stale session is terminal for the current command.
func (a *app) requireAuth() error {
	if a.session == nil {
		return auth.ErrNoSession
	}
	if a.session.Expired(time.Minute) {
		return fmt.Errorf("stored tesco session expired at %s — run `grosh auth login`",
			a.session.ExpiresAt.Local().Format(time.RFC1123))
	}
	return nil
}

// errQuiet exits non-zero without printing anything. It is for a command whose
// own output is the report — `basket check` has already said what is wrong, and
// an "error:" line after it would only add noise.
var errQuiet = errors.New("")

// action adapts a command body to urfave's signature, routing failures through
// the renderer so that --json failures are still JSON, and exiting non-zero.
func action(fn func(context.Context, *cli.Command, *app) error) cli.ActionFunc {
	return func(ctx context.Context, cmd *cli.Command) error {
		a, err := newApp(cmd)
		if err != nil {
			// No renderer yet, so report plainly and bail.
			fmt.Fprintln(os.Stderr, "grosh:", err)
			return cli.Exit("", 1)
		}
		if err := fn(ctx, cmd, a); err != nil {
			if !errors.Is(err, errQuiet) {
				a.r.Fail(err)
			}
			return cli.Exit("", 1)
		}
		return nil
	}
}
