//go:build !integration

package server

import (
	"os"
	"testing"

	"github.com/spa-skyson/pi-rate/internal/testenv"
)

// TestMain strips the ambient pirate/provider environment before any test
// runs. Without it, a machine with a configured pirate leaks provider
// credentials into the unit tests: ambient ANTHROPIC_BASE_URL made
// TestNewPromptHandler_NoAPIKey resolve credentials through the SDK's
// fallback chain instead of failing with "no API key".
//
// The integration build tag carries its own TestMain (integration_test.go);
// the !integration constraint keeps exactly one per tag set.
func TestMain(m *testing.M) {
	testenv.UnsetEnv()
	os.Exit(m.Run())
}
