package session

import (
	"os"
	"testing"

	"github.com/spa-skyson/pi-rate/internal/testenv"
)

// TestMain strips the ambient pirate/provider environment before any test
// runs. AgentContextFromEnv and the session store read spawn-tree variables
// (PI_AGENT_ID, PI_AGENT_WORKTREE and friends) directly from the process
// environment, so a test binary launched from inside a running pirate session
// inherits that session's identity and fails on assertions of emptiness.
// Tests that need the variables set them explicitly (t.Setenv).
func TestMain(m *testing.M) {
	testenv.UnsetEnv()
	os.Exit(m.Run())
}
