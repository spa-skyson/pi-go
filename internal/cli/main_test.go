package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestMain redirects HOME to a throwaway directory for every test in this
// package.
//
// Code under test resolves paths through os.UserHomeDir and writes there —
// ~/.pi-go/config.json, session history, logs. Tests that exercise those paths
// without isolating HOME first will overwrite the developer's real
// configuration: running "go test ./..." was enough to rewrite the default
// model role and theme of the machine running it. Isolating at package scope
// makes that impossible by construction, rather than relying on every future
// test remembering to call t.Setenv("HOME", ...).
//
// Tests that need their own HOME can still override it; t.Setenv restores this
// value rather than the developer's when they finish.
// testBrowserStubEnv switches this binary into stand-in-browser mode. A test
// that needs $BROWSER to point at something runnable sets it and points BROWSER
// at os.Args[0]: the child exits here, before m.Run, so no tests are re-run and
// no real browser is launched. A literal path like /usr/bin/true does not
// travel -- on Windows it fails exec.LookPath, and Open falls through to
// "cmd /c start", which opens the runner's actual browser.
const testBrowserStubEnv = "PI_TEST_BROWSER_STUB"

func TestMain(m *testing.M) {
	if os.Getenv(testBrowserStubEnv) != "" {
		os.Exit(0)
	}

	dir, err := os.MkdirTemp("", "pi-go-test-home-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "isolating HOME: %v\n", err)
		os.Exit(1)
	}
	os.Setenv("HOME", dir)
	os.Setenv("USERPROFILE", dir) // os.UserHomeDir on Windows

	// lastSessionFile is a package-level var resolved from $HOME at init time,
	// which happens before TestMain runs — setting HOME above cannot reach it.
	// Repoint it explicitly so print-mode tests that do not already override it
	// cannot write to the developer's real ~/.pi-go.
	lastSessionFile = filepath.Join(dir, ".pi-go", "last-session.json")

	// Isolating HOME is not enough: loadDotEnv also looks for .pi-go/.env by
	// walking from the working directory up to the filesystem root, which does
	// not consult the home directory at all. Run from a directory with no
	// .pi-go above it, so that walk finds nothing.
	//
	// Whether it finds anything depends on where the checkout sits. A CI
	// runner's workspace has no .pi-go on the path to the root and every test
	// passes; a developer's checkout under ~ reaches the real ~/.pi-go/.env,
	// and loadDotEnv exports its contents with os.Setenv — process-wide, past
	// the end of the test that triggered it. Four unrelated tests in this
	// package then fail on that machine alone.
	if err := os.Chdir(dir); err != nil {
		fmt.Fprintf(os.Stderr, "isolating the working directory: %v\n", err)
		os.Exit(1)
	}

	code := m.Run()

	os.RemoveAll(dir)
	os.Exit(code)
}
