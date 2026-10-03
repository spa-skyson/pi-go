package config

import (
	"os"
	"testing"

	"github.com/spa-skyson/pi-rate/internal/testenv"
)

// TestMain strips the ambient pirate/provider environment (PI_* variables,
// provider keys and base URLs, PIRATE_HOME) before any test runs.
//
// This package's resolvers read the process environment as a fallback for
// config values, so a machine where pirate itself is configured leaks its
// settings into the tests: PI_STREAM_IDLE_TIMEOUT_MS flipped
// TestResolveStreamIdleTimeout and PIRATE_HOME redirected config I/O on the
// owner's machine. Tests that exercise environment resolution set the
// variables they need explicitly (t.Setenv), which wins over the cleanup and
// restores at the end.
func TestMain(m *testing.M) {
	testenv.UnsetEnv()
	os.Exit(m.Run())
}
