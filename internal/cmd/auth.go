package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/endigma/tescoctl/internal/auth"
	"github.com/endigma/tescoctl/internal/view"
	"github.com/urfave/cli/v3"
)

func authCmd() *cli.Command {
	return &cli.Command{
		Name:  "auth",
		Usage: "Manage the Tesco session",
		Commands: []*cli.Command{
			authLoginCmd(),
			authImportCmd(),
			authRefreshCmd(),
			authStatusCmd(),
			authLogoutCmd(),
		},
	}
}

func authLoginCmd() *cli.Command {
	return &cli.Command{
		Name:  "login",
		Usage: "Sign in to Tesco in a browser and save the session",
		Description: "Opens Chrome at the Tesco sign-in page and waits for you to log in.\n" +
			"Your credentials are never typed by tescoctl, only by you.",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "chrome",
				Usage: "path to the Chrome binary, if it cannot be found automatically",
			},
			&cli.DurationFlag{
				Name:  "wait",
				Usage: "how long to wait for sign-in",
				Value: 10 * time.Minute,
			},
		},
		Action: action(func(ctx context.Context, cmd *cli.Command, a *app) error {
			backend := &auth.Browser{
				ExecPath: cmd.String("chrome"),
				Timeout:  cmd.Duration("wait"),
				Progress: a.r.Err,
			}
			if a.r.JSON {
				backend.Progress = nil
			}
			return a.harvest(ctx, backend)
		}),
	}
}

func authImportCmd() *cli.Command {
	return &cli.Command{
		Name:  "import",
		Usage: "Save a session from a cookie export",
		Description: "Reads cookies exported from a signed-in browser. Accepts a JSON array\n" +
			"of {name, value} objects or an object mapping names to values.",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "file",
				Aliases: []string{"f"},
				Usage:   "read the export from a file instead of stdin",
			},
		},
		Action: action(func(ctx context.Context, cmd *cli.Command, a *app) error {
			src := os.Stdin
			if path := cmd.String("file"); path != "" {
				f, err := os.Open(path)
				if err != nil {
					return fmt.Errorf("opening cookie export: %w", err)
				}
				defer f.Close()
				src = f
			}
			return a.harvest(ctx, &auth.Importer{Src: src})
		}),
	}
}

func authStatusCmd() *cli.Command {
	return &cli.Command{
		Name:  "status",
		Usage: "Show the stored session",
		Action: action(func(ctx context.Context, cmd *cli.Command, a *app) error {
			status := a.sessionView()
			return a.r.Emit(status, func() string { return sessionStatus(a, status) })
		}),
	}
}

func authRefreshCmd() *cli.Command {
	return &cli.Command{
		Name:  "refresh",
		Usage: "Renew the session without signing in again",
		Description: "Redeems the stored refresh token for a new access token. This happens\n" +
			"automatically when a command needs it; run it by hand to renew early\n" +
			"or to check that renewal still works.",
		Action: action(func(ctx context.Context, cmd *cli.Command, a *app) error {
			if a.session == nil {
				return auth.ErrNoSession
			}
			if err := a.renew(ctx); err != nil {
				return err
			}
			status := a.sessionView()
			return a.r.Emit(status, func() string { return sessionStatus(a, status) })
		}),
	}
}

// sessionView renders the stored session for output. It is the one place that
// knows how a Session becomes a view.Session.
func (a *app) sessionView() view.Session {
	status := view.Session{Path: a.store.Path}
	if a.session == nil {
		return status
	}
	status.LoggedIn = true
	status.CustomerUUID = a.session.CustomerUUID
	status.Expired = a.session.Expired(time.Minute)
	status.Cookies = len(a.session.Cookies)
	status.Renewable = a.session.Renewable()
	if !a.session.ExpiresAt.IsZero() {
		status.ExpiresAt = a.session.ExpiresAt.Format(time.RFC3339)
	}
	if when, ok := a.session.RefreshExpiry(); ok {
		status.RenewableUntil = when.Format(time.RFC3339)
	}
	return status
}

func authLogoutCmd() *cli.Command {
	return &cli.Command{
		Name:  "logout",
		Usage: "Delete the stored session",
		Action: action(func(ctx context.Context, cmd *cli.Command, a *app) error {
			if err := a.store.Delete(); err != nil {
				return err
			}
			a.session = nil
			return a.r.Emit(view.Session{Path: a.store.Path}, func() string {
				return "Signed out."
			})
		}),
	}
}

// harvest runs a backend and persists whatever it returns.
func (a *app) harvest(ctx context.Context, backend auth.Backend) error {
	session, err := backend.Harvest(ctx)
	if err != nil {
		return err
	}
	if err := a.store.Save(session); err != nil {
		return err
	}
	a.session = session

	status := a.sessionView()
	return a.r.Emit(status, func() string { return sessionStatus(a, status) })
}

func sessionStatus(a *app, s view.Session) string {
	if !s.LoggedIn {
		return a.r.Styles.Muted.Render("Not signed in.") + "\nRun `tescoctl auth login`."
	}

	var b strings.Builder
	state := a.r.Styles.Price.Render("signed in")
	switch {
	case s.Expired && s.Renewable:
		// Not a problem: the next command renews it without a human.
		state = a.r.Styles.Muted.Render("expired, renews automatically")
	case s.Expired:
		state = a.r.Styles.Error.Render("expired")
	}
	fmt.Fprintf(&b, "%s %s\n", pad("Status", 11), state)
	fmt.Fprintf(&b, "%s %s\n", pad("Account", 11), s.CustomerUUID)
	if s.ExpiresAt != "" {
		when, err := time.Parse(time.RFC3339, s.ExpiresAt)
		if err == nil {
			fmt.Fprintf(&b, "%s %s\n", pad("Expires", 11), humanExpiry(when))
		}
	}
	if s.RenewableUntil != "" {
		when, err := time.Parse(time.RFC3339, s.RenewableUntil)
		if err == nil {
			fmt.Fprintf(&b, "%s %s\n", pad("Renews to", 11), humanExpiry(when))
		}
	}
	fmt.Fprintf(&b, "%s %d\n", pad("Cookies", 11), s.Cookies)
	fmt.Fprintf(&b, "%s %s", pad("File", 11), a.r.Styles.Muted.Render(s.Path))
	return b.String()
}

// humanExpiry renders the expiry with the remaining time, which is the part
// worth knowing.
func humanExpiry(when time.Time) string {
	stamp := when.Local().Format(time.RFC1123)
	remaining := time.Until(when)
	if remaining <= 0 {
		return stamp + " (expired)"
	}
	return fmt.Sprintf("%s (in %s)", stamp, remaining.Round(time.Minute))
}
