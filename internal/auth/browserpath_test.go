package auth

import (
	"os"
	"testing"
)

// TestFindBrowser is environment-dependent, so it asserts only what must hold
// everywhere: whatever is returned is an executable file. It logs the find so
// `go test -v` doubles as a diagnostic for "why won't auth login start?".
func TestFindBrowser(t *testing.T) {
	path := FindBrowser()
	if path == "" {
		t.Log("no Chromium browser detected; chromedp will fall back to its own lookup")
		return
	}
	t.Logf("detected browser: %s", path)

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("FindBrowser returned %q which does not exist: %v", path, err)
	}
	if info.IsDir() {
		t.Errorf("FindBrowser returned a directory: %q", path)
	}
	if info.Mode()&0o111 == 0 {
		t.Errorf("FindBrowser returned a non-executable file: %q", path)
	}
}
