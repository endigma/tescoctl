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
			"Your credentials are never typed by grosh, only by you.",
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
			status := view.Session{Path: a.store.Path}
			if a.session != nil {
				status.LoggedIn = true
				status.CustomerUUID = a.session.CustomerUUID
				status.Expired = a.session.Expired(time.Minute)
				status.Cookies = len(a.session.Cookies)
				if !a.session.ExpiresAt.IsZero() {
					status.ExpiresAt = a.session.ExpiresAt.Format(time.RFC3339)
				}
			}
			return a.r.Emit(status, func() string { return sessionStatus(a, status) })
		}),
	}
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

	status := view.Session{
		LoggedIn:     true,
		Path:         a.store.Path,
		CustomerUUID: session.CustomerUUID,
		Cookies:      len(session.Cookies),
	}
	if !session.ExpiresAt.IsZero() {
		status.ExpiresAt = session.ExpiresAt.Format(time.RFC3339)
	}
	return a.r.Emit(status, func() string { return sessionStatus(a, status) })
}

func sessionStatus(a *app, s view.Session) string {
	if !s.LoggedIn {
		return a.r.Styles.Muted.Render("Not signed in.") + "\nRun `grosh auth login`."
	}

	var b strings.Builder
	state := a.r.Styles.Price.Render("signed in")
	if s.Expired {
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
