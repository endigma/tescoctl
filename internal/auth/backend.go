package auth

import "context"

// Backend mints a session. Implementations differ only in how they get a
// browser's cookies: drive one, or be handed an export.
type Backend interface {
	// Name identifies the backend in help text and errors.
	Name() string

	// Harvest obtains a fresh session, blocking as long as it needs to.
	Harvest(ctx context.Context) (*Session, error)

	// Interactive reports whether Harvest needs a human. Non-interactive
	// backends can be used to refresh a session mid-command; interactive ones
	// cannot, so a 401 there is terminal.
	Interactive() bool
}
