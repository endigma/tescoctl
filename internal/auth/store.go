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
var ErrNoSession = errors.New("no stored tesco session — run `grosh auth login`")

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
	return filepath.Join(base, "grosh"), nil
}

// NewStore returns a Store at the default location.
func NewStore() (*Store, error) {
	dir, err := DefaultDir()
	if err != nil {
		return nil, err
	}
	return &Store{Path: filepath.Join(dir, "session.json")}, nil
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
		return nil, fmt.Errorf("stored session at %s is corrupt (%w) — run `grosh auth login`", s.Path, err)
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
