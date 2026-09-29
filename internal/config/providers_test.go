package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/testenv"
)

// loadFromGlobalConfig writes configJSON as the global ~/.pi-go/config.json
// and loads it from a project-free directory, so the merge sees exactly this
// file and nothing from a .pi-go directory above the checkout.
func loadFromGlobalConfig(t *testing.T, configJSON string) (Config, error) {
	t.Helper()
	home := t.TempDir()
	testenv.SetHome(t, home)
	if err := os.MkdirAll(filepath.Join(home, ".pi-go"), 0o700); err != nil {
		t.Fatalf("mkdir ~/.pi-go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".pi-go", "config.json"), []byte(configJSON), 0o600); err != nil {
		t.Fatalf("write config.json: %v", err)
	}
	return LoadFrom(t.TempDir())
}

func TestLoadProviders(t *testing.T) {
	t.Setenv("CORP_KEY", "sk-corp-secret")
	t.Setenv("CORP_URL", "https://headroom.example")
	t.Setenv("CORP_PROXY_KEY", "sk-proxy")
	cfg, err := loadFromGlobalConfig(t, `{
		"providers": {
			"corp-claude": {
				"type": "openai-compatible",
				"baseURL": "https://corp.example/v1",
				"apiKey": "${CORP_KEY}",
				"models": { "claude-opus-5": { "contextWindow": 200000 } }
			},
			"corp-proxy": {
				"type": "anthropic",
				"baseURL": "${CORP_URL}",
				"apiKeyEnv": "CORP_PROXY_KEY"
			}
		}
	}`)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if len(cfg.Providers) != 2 {
		t.Fatalf("providers = %d entries, want 2", len(cfg.Providers))
	}

	cc := cfg.Providers["corp-claude"]
	if cc.Type != "openai-compatible" || cc.BaseURL != "https://corp.example/v1" {
		t.Errorf("corp-claude = %+v", cc)
	}
	// ${VAR} in apiKey is expanded at load.
	if cc.APIKey != "sk-corp-secret" {
		t.Errorf("corp-claude apiKey = %q, want the expanded CORP_KEY", cc.APIKey)
	}
	if got := cc.Models["claude-opus-5"].ContextWindow; got != 200000 {
		t.Errorf("corp-claude claude-opus-5 contextWindow = %d, want 200000", got)
	}
	if cc.Protocol() != "openai" {
		t.Errorf("corp-claude protocol = %q, want openai", cc.Protocol())
	}

	cp := cfg.Providers["corp-proxy"]
	if cp.BaseURL != "https://headroom.example" {
		t.Errorf("corp-proxy baseURL = %q, want the expanded CORP_URL", cp.BaseURL)
	}
	if cp.Protocol() != "anthropic" {
		t.Errorf("corp-proxy protocol = %q, want anthropic", cp.Protocol())
	}
}

func TestLoadProviders_RejectsBuiltinName(t *testing.T) {
	_, err := loadFromGlobalConfig(t, `{
		"providers": { "Anthropic": { "baseURL": "https://corp.example" } }
	}`)
	if err == nil || !strings.Contains(err.Error(), "built-in") {
		t.Fatalf("err = %v, want a built-in-name collision", err)
	}
}

func TestLoadProviders_RejectsBadType(t *testing.T) {
	_, err := loadFromGlobalConfig(t, `{
		"providers": { "corp": { "type": "grpc", "baseURL": "https://corp.example" } }
	}`)
	if err == nil || !strings.Contains(err.Error(), `unknown type "grpc"`) {
		t.Fatalf("err = %v, want an unknown-type error", err)
	}
}

func TestLoadProviders_RejectsBadName(t *testing.T) {
	// A slash in the name would make "corp/x/model" ambiguous to split.
	_, err := loadFromGlobalConfig(t, `{
		"providers": { "corp/x": { "baseURL": "https://corp.example" } }
	}`)
	if err == nil || !strings.Contains(err.Error(), "invalid name") {
		t.Fatalf("err = %v, want an invalid-name error", err)
	}
}

func TestLoadProviders_RejectsMissingBaseURL(t *testing.T) {
	_, err := loadFromGlobalConfig(t, `{
		"providers": { "corp": { "apiKey": "sk" } }
	}`)
	if err == nil || !strings.Contains(err.Error(), "baseURL is required") {
		t.Fatalf("err = %v, want a missing-baseURL error", err)
	}
}

func TestLoadProviders_RejectsDuplicateName(t *testing.T) {
	_, err := loadFromGlobalConfig(t, `{
		"providers": {
			"Corp": { "baseURL": "https://corp.example" },
			"corp": { "baseURL": "https://other.example" }
		}
	}`)
	if err == nil || !strings.Contains(err.Error(), "duplicate name") {
		t.Fatalf("err = %v, want a duplicate-name error", err)
	}
}

func TestNamedProviderPrefix(t *testing.T) {
	cfg := Config{Providers: map[string]ProviderConfig{
		"corp":        {BaseURL: "https://corp.example"},
		"corp-claude": {BaseURL: "https://corp.example/v1"},
	}}

	// Longest prefix wins when one name extends the other.
	name, rest, ok := cfg.NamedProviderPrefix("corp-claude/claude-opus-5")
	if !ok || name != "corp-claude" || rest != "claude-opus-5" {
		t.Errorf("corp-claude/claude-opus-5 → (%q, %q, %v)", name, rest, ok)
	}
	// Prefix matching is case-insensitive, but the canonical name comes back.
	name, rest, ok = cfg.NamedProviderPrefix("CORP-CLAUDE/Claude-Opus-5")
	if !ok || name != "corp-claude" || rest != "Claude-Opus-5" {
		t.Errorf("CORP-CLAUDE/… → (%q, %q, %v)", name, rest, ok)
	}
	name, rest, ok = cfg.NamedProviderPrefix("corp/foo")
	if !ok || name != "corp" || rest != "foo" {
		t.Errorf("corp/foo → (%q, %q, %v)", name, rest, ok)
	}
	// No slash, no prefix — a bare "corp" is a model name, not a route.
	if _, _, ok := cfg.NamedProviderPrefix("corp"); ok {
		t.Error("bare corp must not match")
	}
	// Only declared providers match.
	if _, _, ok := cfg.NamedProviderPrefix("openai/gpt-x"); ok {
		t.Error("a built-in prefix must not match a declared-provider lookup")
	}
	if _, _, ok := cfg.NamedProviderPrefix("gpt-5.6-sol"); ok {
		t.Error("a bare model name must not match")
	}
}

func TestResolveRole_NamedPrefixWins(t *testing.T) {
	cfg := Config{
		Roles: map[string]RoleConfig{
			"default": {Model: "corp-claude/claude-opus-5", Provider: "anthropic"},
		},
		DefaultProvider: "openai",
		Providers: map[string]ProviderConfig{
			"corp-claude": {BaseURL: "https://corp.example/v1"},
		},
	}
	// Both the role's provider and DefaultProvider lose to the named prefix:
	// either of them winning would send the model to a built-in endpoint.
	_, prov, _, _, _, err := cfg.ResolveRole("default")
	if err != nil {
		t.Fatal(err)
	}
	if prov != "corp-claude" {
		t.Errorf("provider = %q, want corp-claude", prov)
	}

	// A bare name still falls through to the old chain.
	cfg.Roles["default"] = RoleConfig{Model: "claude-opus-5"}
	_, prov, _, _, _, err = cfg.ResolveRole("default")
	if err != nil {
		t.Fatal(err)
	}
	if prov != "anthropic" {
		t.Errorf("provider = %q, want anthropic (role provider for a bare name)", prov)
	}
}

func TestResolveBaseURLs_NamedProviders(t *testing.T) {
	cfg := Config{
		BaseURLs: map[string]string{"ollama": "http://localhost:11434"},
		Providers: map[string]ProviderConfig{
			"corp-claude": {BaseURL: "https://corp.example/v1"},
		},
	}
	urls := cfg.ResolveBaseURLs()
	if urls["corp-claude"] != "https://corp.example/v1" {
		t.Errorf("urls[corp-claude] = %q, want the declared endpoint", urls["corp-claude"])
	}
	if urls["ollama"] != "http://localhost:11434" {
		t.Errorf("urls[ollama] = %q, want the configured value", urls["ollama"])
	}
}

func TestResolveAPIKeys_NamedProviders(t *testing.T) {
	t.Setenv("CORP_PROXY_KEY", "sk-from-env")
	t.Setenv("ANTHROPIC_API_KEY", "sk-builtin")
	cfg := Config{Providers: map[string]ProviderConfig{
		// A literal apiKey wins over apiKeyEnv.
		"corp-claude": {BaseURL: "https://corp.example/v1", APIKey: "sk-literal", APIKeyEnv: "CORP_PROXY_KEY"},
		// No literal key: the env var is read.
		"corp-proxy": {BaseURL: "https://corp.example", APIKeyEnv: "CORP_PROXY_KEY"},
		// Neither: no entry at all, so callers see "no key".
		"corp-open": {BaseURL: "http://localhost:8080"},
	}}
	keys := cfg.ResolveAPIKeys()
	if keys["corp-claude"] != "sk-literal" {
		t.Errorf("keys[corp-claude] = %q, want the literal key", keys["corp-claude"])
	}
	if keys["corp-proxy"] != "sk-from-env" {
		t.Errorf("keys[corp-proxy] = %q, want the CORP_PROXY_KEY value", keys["corp-proxy"])
	}
	if _, ok := keys["corp-open"]; ok {
		t.Error("keys[corp-open] must be absent for a keyless provider")
	}
	// The built-in env sweep still runs underneath.
	if keys["anthropic"] != "sk-builtin" {
		t.Errorf("keys[anthropic] = %q, want the env value", keys["anthropic"])
	}
}

func TestContextWindowFor(t *testing.T) {
	cfg := Config{Providers: map[string]ProviderConfig{
		"corp-claude": {BaseURL: "https://corp.example/v1", Models: map[string]ProviderModelConfig{
			"claude-opus-5": {ContextWindow: 200000},
		}},
	}}
	if got := cfg.ContextWindowFor("corp-claude", "claude-opus-5"); got != 200000 {
		t.Errorf("ContextWindowFor = %d, want 200000", got)
	}
	if got := cfg.ContextWindowFor("corp-claude", "unknown"); got != 0 {
		t.Errorf("unknown model → %d, want 0", got)
	}
	if got := cfg.ContextWindowFor("not-declared", "claude-opus-5"); got != 0 {
		t.Errorf("unknown provider → %d, want 0", got)
	}
}

// TestSaveRoundTripKeepsProviders pins that Save serializes the providers
// section: Save marshals the whole config, and a section that silently
// vanished would break every named provider on the next save.
func TestSaveRoundTripKeepsProviders(t *testing.T) {
	cfg := Defaults()
	cfg.Providers = map[string]ProviderConfig{
		"corp-claude": {
			Type:    "openai-compatible",
			BaseURL: "https://corp.example/v1",
			APIKey:  "sk-corp",
			Models:  map[string]ProviderModelConfig{"claude-opus-5": {ContextWindow: 200000}},
		},
	}
	testenv.SetHome(t, t.TempDir())
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := LoadFrom(t.TempDir())
	if err != nil {
		t.Fatalf("re-load: %v", err)
	}
	got, ok := loaded.Providers["corp-claude"]
	if !ok {
		t.Fatalf("providers after round trip = %+v, want corp-claude kept", loaded.Providers)
	}
	if got.BaseURL != "https://corp.example/v1" || got.APIKey != "sk-corp" || got.Type != "openai-compatible" {
		t.Errorf("corp-claude after round trip = %+v", got)
	}
	if got.Models["claude-opus-5"].ContextWindow != 200000 {
		t.Errorf("context window after round trip = %+v", got.Models)
	}
}
