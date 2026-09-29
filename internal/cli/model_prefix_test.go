package cli

import (
	"testing"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/testenv"
)

// multiProviderConfig is the shape a user hits after /model has written a
// provider into the default role: two providers with their own endpoints, and
// the default role naming one of them.
//
// The env base-URL overrides are cleared because they legitimately outrank the
// config file (ResolveBaseURLs), and a developer machine pointing one of these
// providers at a local gateway would otherwise decide the assertion.
func multiProviderConfig(t *testing.T) config.Config {
	t.Helper()
	for _, name := range []string{"ANTHROPIC_BASE_URL", "OPENROUTER_BASE_URL"} {
		t.Setenv(name, "")
	}
	return config.Config{
		Roles: map[string]config.RoleConfig{
			"default": {Model: "claude-opus-5", Provider: "anthropic"},
		},
		BaseURLs: map[string]string{
			"anthropic":  "https://corp.example/v1",
			"openrouter": "https://home.example/v1",
		},
	}
}

func TestResolveSwitchedModel_PrefixBeatsRoleProvider(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	origURL := flagURL
	t.Cleanup(func() { flagURL = origURL })
	flagURL = ""

	cfg := multiProviderConfig(t)
	info, baseURL, _, err := resolveSwitchedModel(cfg, "openrouter/gemma-4", "anthropic")
	if err != nil {
		t.Fatalf("resolveSwitchedModel: %v", err)
	}
	if info.Provider != "openrouter" {
		t.Errorf("provider = %q, want openrouter", info.Provider)
	}
	if baseURL != "https://home.example/v1" {
		t.Errorf("baseURL = %q, want the openrouter endpoint", baseURL)
	}
}

func TestResolveSwitchedModel_BareNameUsesRoleProvider(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	origURL := flagURL
	t.Cleanup(func() { flagURL = origURL })
	flagURL = ""

	cfg := multiProviderConfig(t)
	info, baseURL, _, err := resolveSwitchedModel(cfg, "claude-opus-5", "anthropic")
	if err != nil {
		t.Fatalf("resolveSwitchedModel: %v", err)
	}
	if info.Provider != "anthropic" {
		t.Errorf("provider = %q, want anthropic", info.Provider)
	}
	if baseURL != "https://corp.example/v1" {
		t.Errorf("baseURL = %q, want the anthropic endpoint", baseURL)
	}
}

func TestResolveRuntimeModel_PrefixBeatsRoleProvider(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	origSession, origModel, origURL := flagSession, flagModel, flagURL
	t.Cleanup(func() { flagSession, flagModel, flagURL = origSession, origModel, origURL })
	flagSession, flagModel, flagURL = "", "", ""

	cfg := multiProviderConfig(t)
	info, baseURL, err := resolveRuntimeModelForRole(cfg, "openrouter/gemma-4", "anthropic", "default")
	if err != nil {
		t.Fatalf("resolveRuntimeModelForRole: %v", err)
	}
	if info.Provider != "openrouter" {
		t.Errorf("provider = %q, want openrouter", info.Provider)
	}
	if baseURL != "https://home.example/v1" {
		t.Errorf("baseURL = %q, want the openrouter endpoint", baseURL)
	}
}
