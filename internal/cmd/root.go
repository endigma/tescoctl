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
		Name:    "tescoctl",
		Usage:   "A CLI for Tesco groceries",
		Version: version,
		Description: "tescoctl talks to Tesco's internal GraphQL gateway. It is not an\n" +
			"official or supported API: operations can change without notice.",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "json",
				Usage: "emit JSON on stdout instead of a table",
			},
			&cli.StringFlag{
				Name:    "api-key",
				Usage:   "override the public Tesco API key (it rotates)",
				Sources: cli.EnvVars("TESCO_API_KEY"),
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
		Refresh:  a.refresh,
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

// requireAuth fails early when a command needs a session and there is none, so
// the user gets a clear instruction instead of a 401.
//
// An expired access token is not a failure. The stored jar carries a refresh
// token good for thirty days, so a stale session is renewed here rather than
// sent back to a browser; only a spent refresh token needs a human.
func (a *app) requireAuth(ctx context.Context) error {
	if a.session == nil {
		return auth.ErrNoSession
	}
	if !a.session.Expired(time.Minute) {
		return nil
	}
	if !a.session.Renewable() {
		return fmt.Errorf("stored tesco session expired at %s and cannot be renewed — run `tescoctl auth login`",
			a.session.ExpiresAt.Local().Format(time.RFC1123))
	}
	return a.renew(ctx)
}

// refresh is the hook the client calls after a 401. It reports whether the
// call is worth retrying; a spent refresh token is not an error here, because
// the client renders its own "run auth login" message for that case.
func (a *app) refresh(ctx context.Context) (bool, error) {
	if a.session == nil || !a.session.Renewable() {
		return false, nil
	}
	if err := a.renew(ctx); err != nil {
		if errors.Is(err, auth.ErrLoginRequired) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// renew redeems the refresh token and persists the result.
//
// Persisting is not optional. Tesco rotates the refresh token alongside the
// access token, so the copy on disk is dead the moment this call succeeds — a
// renewal that is not written out has burned the credential the next one needs,
// which is why a failed save is reported as a lost session rather than swallowed.
func (a *app) renew(ctx context.Context) error {
	renewed, err := (&auth.Refresher{Session: a.session}).Harvest(ctx)
	if err != nil {
		return err
	}
	a.session = renewed
	if err := a.store.Save(renewed); err != nil {
		return fmt.Errorf("renewed the tesco session but could not save it to %s (%w) — "+
			"the previous one is no longer valid, so run `tescoctl auth login`", a.store.Path, err)
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
			fmt.Fprintln(os.Stderr, "tescoctl:", err)
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
