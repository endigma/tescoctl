package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// token builds a JWT-shaped access token expiring at exp. Only the payload is
// read, so the signature is a placeholder.
func token(id string, exp time.Time) string {
	payload, _ := json.Marshal(map[string]any{"exp": exp.Unix(), "jti": id})
	return "hdr." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

// expiryCookie renders the OAuth.TokensExpiryTime value the way Tesco does:
// URL-encoded JSON of epoch milliseconds.
func expiryCookie(access, refresh time.Time) string {
	raw, _ := json.Marshal(map[string]int64{
		"AccessToken":  access.UnixMilli(),
		"RefreshToken": refresh.UnixMilli(),
	})
	return url.QueryEscape(string(raw))
}

func testSession(access, refresh time.Time) *Session {
	tok := token("old", access)
	return &Session{
		AccessToken:  tok,
		CustomerUUID: "cust-1",
		ExpiresAt:    access,
		Cookies: map[string]string{
			accessTokenCookie:  tok,
			customerCookie:     "cust-1",
			refreshTokenCookie: "refresh-abc",
			tokensExpiryCookie: expiryCookie(access, refresh),
			"_abck":            "akamai",
			"bm_sz":            "akamai",
		},
	}
}

func TestRefreshExpiry(t *testing.T) {
	refresh := time.Now().Add(30 * 24 * time.Hour).Truncate(time.Millisecond)
	s := testSession(time.Now().Add(time.Hour), refresh)

	got, ok := s.RefreshExpiry()
	if !ok {
		t.Fatal("RefreshExpiry reported nothing for a well-formed cookie")
	}
	if !got.Equal(refresh) {
		t.Errorf("RefreshExpiry = %s, want %s", got, refresh)
	}
}

func TestRenewable(t *testing.T) {
	hour := time.Now().Add(time.Hour)

	tests := []struct {
		name    string
		session *Session
		want    bool
	}{
		{"nil session", nil, false},
		{"live refresh token", testSession(hour, time.Now().Add(30*24*time.Hour)), true},
		{
			// The whole point: an access token can be long dead while the
			// refresh token behind it is still good.
			name:    "expired access, live refresh",
			session: testSession(time.Now().Add(-2*time.Hour), time.Now().Add(29*24*time.Hour)),
			want:    true,
		},
		{"lapsed refresh token", testSession(hour, time.Now().Add(-time.Minute)), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.session.Renewable(); got != tt.want {
				t.Errorf("Renewable() = %v, want %v", got, tt.want)
			}
		})
	}

	t.Run("no refresh cookie", func(t *testing.T) {
		s := testSession(hour, time.Now().Add(24*time.Hour))
		delete(s.Cookies, refreshTokenCookie)
		if s.Renewable() {
			t.Error("Renewable() = true without a refresh token")
		}
	})

	t.Run("no stated window", func(t *testing.T) {
		// An unreadable window is treated as live, matching how Expired treats
		// an unparseable access-token expiry: attempt the call, let Tesco judge.
		s := testSession(hour, time.Now().Add(24*time.Hour))
		delete(s.Cookies, tokensExpiryCookie)
		if !s.Renewable() {
			t.Error("Renewable() = false with no stated window; want true")
		}
	})
}

// refreshServer stands in for Tesco: it rotates the cookies the real endpoint
// rotates, then redirects to the landing page named in `from`.
func refreshServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/account/auth/en-GB/refresh-token", handler)
	mux.HandleFunc("/shop/en-GB/landing/groceries", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "groceries")
	})
	mux.HandleFunc("/account/login/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "sign in")
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func endpointFor(srv *httptest.Server) string {
	return srv.URL + "/account/auth/en-GB/refresh-token?soft-refresh=false"
}

func TestRefresherRotates(t *testing.T) {
	newToken := token("new", time.Now().Add(time.Hour))
	srv := refreshServer(t, func(w http.ResponseWriter, r *http.Request) {
		// The real endpoint rotates both tokens together.
		http.SetCookie(w, &http.Cookie{Name: accessTokenCookie, Value: newToken, Path: "/"})
		http.SetCookie(w, &http.Cookie{Name: refreshTokenCookie, Value: "refresh-xyz", Path: "/"})
		http.Redirect(w, r, "/shop/en-GB/landing/groceries", http.StatusFound)
	})

	old := testSession(time.Now().Add(-2*time.Hour), time.Now().Add(29*24*time.Hour))
	r := &Refresher{Session: old, endpoint: endpointFor(srv)}

	if r.Interactive() {
		t.Error("Refresher.Interactive() = true; the whole point is that it is not")
	}

	renewed, err := r.Harvest(context.Background())
	if err != nil {
		t.Fatalf("Harvest: %v", err)
	}
	if renewed.AccessToken != newToken {
		t.Error("renewed session did not pick up the rotated access token")
	}
	if got := renewed.Cookies[refreshTokenCookie]; got != "refresh-xyz" {
		t.Errorf("refresh token = %q, want the rotated one", got)
	}
	// Cookies the endpoint did not resend must survive, or the next refresh
	// goes out without the Akamai state that makes it work.
	if renewed.Cookies["_abck"] != "akamai" {
		t.Error("Akamai cookie was lost across the refresh")
	}
	if old.AccessToken == renewed.AccessToken {
		t.Error("Harvest mutated the input session; it must return a new one")
	}
}

func TestRefresherLoginRedirect(t *testing.T) {
	srv := refreshServer(t, func(w http.ResponseWriter, r *http.Request) {
		// A spent refresh token: Tesco sends the browser to sign in.
		http.Redirect(w, r, "/account/login/en-GB", http.StatusFound)
	})

	r := &Refresher{
		Session:  testSession(time.Now().Add(-2*time.Hour), time.Now().Add(24*time.Hour)),
		endpoint: endpointFor(srv),
	}
	_, err := r.Harvest(context.Background())
	if !errors.Is(err, ErrLoginRequired) {
		t.Fatalf("Harvest error = %v, want ErrLoginRequired", err)
	}
}

// TestRefresherClearedCookies covers what a spent refresh token actually does,
// observed live: Tesco answers 200 on the landing page — indistinguishable from
// success by status or URL — and deletes the OAuth cookies instead of rotating
// them. OAuth.Sid and the Akamai cookies survive, so the jar is not empty.
func TestRefresherClearedCookies(t *testing.T) {
	srv := refreshServer(t, func(w http.ResponseWriter, r *http.Request) {
		for _, name := range []string{accessTokenCookie, refreshTokenCookie, customerCookie} {
			http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1})
		}
		http.Redirect(w, r, "/shop/en-GB/landing/groceries", http.StatusFound)
	})

	r := &Refresher{
		Session:  testSession(time.Now().Add(-2*time.Hour), time.Now().Add(24*time.Hour)),
		endpoint: endpointFor(srv),
	}
	_, err := r.Harvest(context.Background())
	if !errors.Is(err, ErrLoginRequired) {
		t.Fatalf("Harvest error = %v, want ErrLoginRequired", err)
	}
}

func TestRefresherNoRotation(t *testing.T) {
	// Tesco answers 200 but leaves the token alone. Reporting success here
	// would send the caller back to retry the same dead credential.
	srv := refreshServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/shop/en-GB/landing/groceries", http.StatusFound)
	})

	r := &Refresher{
		Session:  testSession(time.Now().Add(-2*time.Hour), time.Now().Add(24*time.Hour)),
		endpoint: endpointFor(srv),
	}
	_, err := r.Harvest(context.Background())
	if err == nil {
		t.Fatal("Harvest succeeded without a rotated token")
	}
	if errors.Is(err, ErrLoginRequired) {
		t.Errorf("a stalled refresh was reported as a dead token: %v", err)
	}
}

func TestRefresherRejectsUnrenewable(t *testing.T) {
	t.Run("nil session", func(t *testing.T) {
		if _, err := (&Refresher{}).Harvest(context.Background()); !errors.Is(err, ErrNoSession) {
			t.Errorf("error = %v, want ErrNoSession", err)
		}
	})

	t.Run("no refresh token", func(t *testing.T) {
		s := testSession(time.Now(), time.Now().Add(24*time.Hour))
		delete(s.Cookies, refreshTokenCookie)
		_, err := (&Refresher{Session: s}).Harvest(context.Background())
		if err == nil {
			t.Fatal("Harvest succeeded with no refresh token")
		}
	})

	t.Run("lapsed window is not attempted", func(t *testing.T) {
		// No endpoint is set: reaching the network would panic the test with a
		// real request to tesco.com, so this also proves it short-circuits.
		s := testSession(time.Now().Add(-time.Hour), time.Now().Add(-time.Minute))
		_, err := (&Refresher{Session: s}).Harvest(context.Background())
		if !errors.Is(err, ErrLoginRequired) {
			t.Errorf("error = %v, want ErrLoginRequired", err)
		}
	})
}

func TestMigrateLegacyDir(t *testing.T) {
	t.Run("moves the old directory", func(t *testing.T) {
		base := t.TempDir()
		legacy := filepath.Join(base, legacyDirName)
		if err := os.MkdirAll(legacy, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(legacy, "session.json"), []byte(`{"accessToken":"x"}`), 0o600); err != nil {
			t.Fatal(err)
		}

		dir := filepath.Join(base, "tescoctl")
		if err := migrateLegacyDir(dir); err != nil {
			t.Fatalf("migrateLegacyDir: %v", err)
		}
		if _, err := os.Stat(filepath.Join(dir, "session.json")); err != nil {
			t.Errorf("session did not survive the migration: %v", err)
		}
		if _, err := os.Stat(legacy); !os.IsNotExist(err) {
			t.Error("legacy directory still present after migration")
		}
	})

	t.Run("does not clobber a newer directory", func(t *testing.T) {
		// Someone who signed in after the rename has a better session than the
		// legacy directory holds; migrating over it would log them out.
		base := t.TempDir()
		legacy := filepath.Join(base, legacyDirName)
		dir := filepath.Join(base, "tescoctl")
		for _, d := range []string{legacy, dir} {
			if err := os.MkdirAll(d, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(dir, "session.json"), []byte(`{"accessToken":"new"}`), 0o600); err != nil {
			t.Fatal(err)
		}

		if err := migrateLegacyDir(dir); err != nil {
			t.Fatalf("migrateLegacyDir: %v", err)
		}
		data, err := os.ReadFile(filepath.Join(dir, "session.json"))
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != `{"accessToken":"new"}` {
			t.Error("migration overwrote the newer session")
		}
	})

	t.Run("fresh install is not an error", func(t *testing.T) {
		if err := migrateLegacyDir(filepath.Join(t.TempDir(), "tescoctl")); err != nil {
			t.Errorf("migrateLegacyDir on a fresh install: %v", err)
		}
	})
}
