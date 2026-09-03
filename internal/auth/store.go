package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// ErrNoSession reports that nothing has been stored yet.
var ErrNoSession = errors.New("no stored tesco session — run `tescoctl auth login`")

// Store persists the session to disk. The file holds a bearer token, so it is
// written 0600 inside a 0700 directory.
type Store struct {
	Path string
}

// DefaultDir is the config directory, honouring XDG_CONFIG_HOME.
func DefaultDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locating config directory: %w", err)
	}
	return filepath.Join(base, "tescoctl"), nil
}

// legacyDirName is what the config directory was called before the tool was
// renamed. It holds a refresh token good for thirty days, so an existing
// install is migrated rather than silently started from scratch.
const legacyDirName = "grosh"

// NewStore returns a Store at the default location, migrating a pre-rename
// directory into place first.
func NewStore() (*Store, error) {
	dir, err := DefaultDir()
	if err != nil {
		return nil, err
	}
	if err := migrateLegacyDir(dir); err != nil {
		return nil, err
	}
	return &Store{Path: filepath.Join(dir, "session.json")}, nil
}

// migrateLegacyDir moves the old config directory to dir when dir does not yet
// exist. Both live under the same parent, so the rename is atomic: either the
// whole directory moves or nothing does, and there is no window in which the
// session exists in neither place.
//
// It is deliberately a no-op once dir exists. A user who has signed in since
// the rename has a newer session than the legacy directory holds, and clobbering
// it would log them out.
func migrateLegacyDir(dir string) error {
	if _, err := os.Stat(dir); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("checking config directory: %w", err)
	}

	legacy := filepath.Join(filepath.Dir(dir), legacyDirName)
	if _, err := os.Stat(legacy); err != nil {
		// Nothing to migrate: a fresh install, which is not an error.
		return nil
	}
	if err := os.Rename(legacy, dir); err != nil {
		return fmt.Errorf("migrating %s to %s: %w", legacy, dir, err)
	}
	return nil
}

// Load reads the stored session, returning ErrNoSession when there is none.
func (s *Store) Load() (*Session, error) {
	data, err := os.ReadFile(s.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNoSession
	}
	if err != nil {
		return nil, fmt.Errorf("reading session: %w", err)
	}

	var session Session
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, fmt.Errorf("stored session at %s is corrupt (%w) — run `tescoctl auth login`", s.Path, err)
	}
	return &session, nil
}

// Save writes the session atomically, so an interrupted write cannot leave a
// half-written file that Load would reject.
func (s *Store) Save(session *Session) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding session: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(s.Path), ".session-*.json")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	defer os.Remove(tmp.Name())

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("securing session file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("writing session: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing session file: %w", err)
	}
	if err := os.Rename(tmp.Name(), s.Path); err != nil {
		return fmt.Errorf("installing session file: %w", err)
	}
	return nil
}

// Delete removes the stored session. Removing a session that is not there is
// not an error.
func (s *Store) Delete() error {
	if err := os.Remove(s.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("removing session: %w", err)
	}
	return nil
}
