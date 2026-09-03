package auth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/chromedp/cdproto/storage"
	"github.com/chromedp/chromedp"
)

// LoginURL is where sign-in starts. Landing on the groceries homepage after
// signing in is what mints the OAuth cookies.
const LoginURL = "https://www.tesco.com/account/login/en-GB?from=/groceries/en-GB/"

// pollInterval is how often the harvester checks for the auth cookies. Slow
// enough to be free, fast enough that the window closes promptly once done.
const pollInterval = time.Second

// Browser drives a real Chrome so a human can sign in, then reads the cookies
// out of it. It never types credentials: Tesco uses email OTP and sits behind
// Akamai, and automating the form is both fragile and the part most likely to
// be treated as abuse. All this does is open a window and wait.
//
// The profile persists between runs, so subsequent logins usually skip the OTP.
type Browser struct {
	// ProfileDir holds the persistent Chrome profile. Empty uses the default
	// under the tescoctl config directory.
	ProfileDir string

	// ExecPath overrides the browser binary. Empty auto-detects via
	// FindBrowser, falling back to chromedp's own lookup.
	ExecPath string

	// Timeout bounds the whole login. Zero means ten minutes.
	Timeout time.Duration

	// Progress receives human-readable status, or nil for silence.
	Progress io.Writer
}

func (b *Browser) Name() string { return "chromedp" }

// Interactive is true: a human has to log in, so this cannot service a silent
// mid-command refresh.
func (b *Browser) Interactive() bool { return true }

func (b *Browser) Harvest(ctx context.Context) (*Session, error) {
	profile, err := b.profileDir()
	if err != nil {
		return nil, err
	}

	timeout := b.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.UserDataDir(profile),
		chromedp.Flag("headless", false),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("no-default-browser-check", true),
	)
	execPath := b.ExecPath
	if execPath == "" {
		execPath = FindBrowser()
	}
	if execPath != "" {
		opts = append(opts, chromedp.ExecPath(execPath))
	}

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, opts...)
	defer cancelAlloc()

	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	defer cancelBrowser()

	if err := chromedp.Run(browserCtx, chromedp.Navigate(LoginURL)); err != nil {
		return nil, fmt.Errorf("could not start a browser: %w\n\n"+
			"tescoctl looks for Google Chrome. If you have a different Chromium browser, "+
			"point at it with --chrome (note that some forks, Arc among them, refuse "+
			"remote control and will not work).\n"+
			"Otherwise export your cookies and use `tescoctl auth import` — see the README.", err)
	}

	b.report(browserName(execPath) + " is open — sign in to Tesco. This will finish on its own.")

	session, err := b.poll(browserCtx)
	if err != nil {
		return nil, err
	}
	b.report("Signed in. Session saved.")
	return session, nil
}

// poll watches the cookie jar until the auth pair appears. Polling rather than
// waiting on a URL because Tesco's post-login redirect chain varies, and the
// cookies are the thing we actually need.
func (b *Browser) poll(ctx context.Context) (*Session, error) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		session, err := readSession(ctx)
		if err == nil {
			return session, nil
		}
		// A browser that has gone away is terminal; an incomplete jar is not.
		if ctx.Err() != nil {
			return nil, loginTimeout(ctx.Err())
		}

		select {
		case <-ctx.Done():
			return nil, loginTimeout(ctx.Err())
		case <-ticker.C:
		}
	}
}

func loginTimeout(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.New("timed out waiting for sign-in — run `tescoctl auth login` again, " +
			"or export cookies and use `tescoctl auth import`")
	}
	return fmt.Errorf("sign-in interrupted: %w", err)
}

// readSession snapshots the browser's cookies and tries to build a session.
func readSession(ctx context.Context) (*Session, error) {
	var raw []RawCookie
	err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		cookies, err := storage.GetCookies().Do(ctx)
		if err != nil {
			return err
		}
		raw = raw[:0]
		for _, c := range cookies {
			raw = append(raw, RawCookie{Name: c.Name, Value: c.Value})
		}
		return nil
	}))
	if err != nil {
		return nil, err
	}
	return FromCookies(raw)
}

func (b *Browser) profileDir() (string, error) {
	if b.ProfileDir != "" {
		return b.ProfileDir, nil
	}
	dir, err := DefaultDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "chrome"), nil
}

// browserName turns a binary path into something worth printing, so the prompt
// names the browser that actually opened rather than assuming Chrome.
func browserName(execPath string) string {
	if execPath == "" {
		return "The browser"
	}
	return filepath.Base(execPath)
}

func (b *Browser) report(msg string) {
	if b.Progress != nil {
		fmt.Fprintln(b.Progress, msg)
	}
}
