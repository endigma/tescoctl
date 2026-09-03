package auth

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// chromedp only looks for a handful of binary names on $PATH, which finds
// nothing on a typical macOS install where browsers live in app bundles. These
// are the usual locations, most standard first.
//
// Only Chromium-family browsers that accept remote control are listed. Arc is
// deliberately absent: it is Chromium-based but refuses CDP, so offering it
// would fail confusingly.
var browserPaths = map[string][]string{
	"darwin": {
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Chromium.app/Contents/MacOS/Chromium",
		"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
		"/Applications/Vivaldi.app/Contents/MacOS/Vivaldi",
	},
	"linux": {
		"/usr/bin/google-chrome",
		"/usr/bin/chromium",
		"/usr/bin/chromium-browser",
		"/usr/bin/microsoft-edge",
		"/usr/bin/brave-browser",
	},
}

// pathNames are tried on $PATH when no bundled browser is found, which covers
// Nix, Homebrew and anything else that installs a plain binary.
var pathNames = []string{
	"google-chrome", "google-chrome-stable", "chromium", "chromium-browser",
	"microsoft-edge", "brave-browser",
}

// FindBrowser returns a usable Chromium binary, or "" to let chromedp try its
// own lookup. Home-relative locations are checked too, since browsers are often
// installed per-user.
func FindBrowser() string {
	candidates := browserPaths[runtime.GOOS]
	if home, err := os.UserHomeDir(); err == nil && runtime.GOOS == "darwin" {
		for _, p := range browserPaths["darwin"] {
			candidates = append(candidates, filepath.Join(home, p))
		}
	}

	for _, path := range candidates {
		if isExecutable(path) {
			return path
		}
	}
	for _, name := range pathNames {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	return ""
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
}
