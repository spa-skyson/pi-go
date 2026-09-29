package config

import (
	"fmt"
	"os"
	"strings"
)

// ProviderModelConfig describes one model served by a user-declared provider.
type ProviderModelConfig struct {
	// ContextWindow is the model's context window in tokens. Zero means
	// unknown: auto-compaction then falls back to the embedded catalog, which
	// does not know a custom endpoint's models, and ends up disabled. Declare
	// the window whenever the endpoint's models are absent from that catalog.
	ContextWindow int64 `json:"contextWindow,omitempty"`
}

// ProviderConfig is one user-declared provider in config.json's "providers"
// section. The key it is stored under becomes a model-name prefix:
// "corp-claude" serves "corp-claude/claude-opus-5".
//
// This type lives in internal/config, and internal/provider deliberately
// does not import it (the dependency would be a cycle): everything the rest
// of the program needs to know about a declared provider flows through
// Config — its endpoint through ResolveBaseURLs, its key through
// ResolveAPIKeys, its protocol through Protocol.
type ProviderConfig struct {
	// Type selects the wire protocol: "" and "openai-compatible" speak the
	// OpenAI protocol, "anthropic" the Anthropic one. Anything else fails
	// validation at load.
	Type string `json:"type,omitempty"`
	// BaseURL is the endpoint every request for this provider goes to.
	// Required. ${VAR} is expanded at load, like MCP server URLs.
	BaseURL string `json:"baseURL"`
	// APIKey is the credential sent with requests. ${VAR} is expanded at
	// load, so the secret can live in ~/.pi-go/.env instead of config.json.
	APIKey string `json:"apiKey,omitempty"`
	// APIKeyEnv names an environment variable to read the key from when
	// APIKey is empty. A literal APIKey wins over it.
	APIKeyEnv string `json:"apiKeyEnv,omitempty"`
	// Models declares per-model metadata, keyed by the model name as it is
	// sent to the endpoint (the part after the "name/" prefix).
	Models map[string]ProviderModelConfig `json:"models,omitempty"`
}

// Protocol maps the configured type onto the wire-protocol name the provider
// package keys its clients on: "openai" or "anthropic". Validation guarantees
// Type is one of "", "openai-compatible", "anthropic"; anything else reads as
// openai-compatible, the same default an empty type gets.
func (p ProviderConfig) Protocol() string {
	if p.Type == "anthropic" {
		return "anthropic"
	}
	return "openai"
}

// builtinProviderNames are the providers pi-go knows without any declaration.
// A declared provider must not shadow one of these: half the program routes
// by name alone (env vars, rate limits, listing endpoints), and a silent
// shadow would split that routing down the middle.
var builtinProviderNames = map[string]bool{
	"anthropic":    true,
	"openai":       true,
	"azure":        true,
	"gemini":       true,
	"mistral":      true,
	"xai":          true,
	"openrouter":   true,
	"ollama":       true,
	"opencode":     true,
	"agentgateway": true,
}

// validProviderTypes are the Type values ProviderConfig accepts.
var validProviderTypes = map[string]bool{
	"":                  true,
	"openai-compatible": true,
	"anthropic":         true,
}

// validateProviders checks every declared provider before the config is used.
// Each failure names the offending entry, so a typo in config.json points at
// itself instead of surfacing later as "unsupported provider" in the middle
// of a session.
func validateProviders(cfg *Config) error {
	seen := make(map[string]string, len(cfg.Providers)) // lower name → canonical
	for name, p := range cfg.Providers {
		if name == "" {
			return fmt.Errorf("providers: entry name must not be empty")
		}
		if builtinProviderNames[strings.ToLower(name)] {
			return fmt.Errorf("providers.%s: name collides with a built-in provider", name)
		}
		if !validProviderName(name) {
			return fmt.Errorf("providers.%s: invalid name (allowed: letters, digits, '.', '_', '-')", name)
		}
		lower := strings.ToLower(name)
		if prev, dup := seen[lower]; dup {
			return fmt.Errorf("providers.%s: duplicate name (matches %q)", name, prev)
		}
		seen[lower] = name
		if p.BaseURL == "" {
			return fmt.Errorf("providers.%s: baseURL is required", name)
		}
		if !validProviderTypes[p.Type] {
			return fmt.Errorf("providers.%s: unknown type %q (valid: \"openai-compatible\", \"anthropic\")", name, p.Type)
		}
	}
	return nil
}

// validProviderName reports whether name is usable as a model prefix. It must
// be non-empty [A-Za-z0-9._-]+: the name is joined to the model with a "/" and
// split back apart on that slash, so a slash inside the name would make
// "a/b/c" ambiguous.
func validProviderName(name string) bool {
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
		default:
			return false
		}
	}
	return name != ""
}

// substituteProviderEnv expands ${VAR} in each declared provider's baseURL
// and apiKey, from the same sources the MCP-URL substitution reads
// (~/.pi-go/.env, project .pi-go/.env, then the process environment). Secrets
// stay out of config.json; the expanded values live in memory only.
func substituteProviderEnv(cfg *Config, cwd string) {
	if len(cfg.Providers) == 0 {
		return
	}
	env := loadEnvFileFrom(cwd)
	for name, p := range cfg.Providers {
		if p.BaseURL != "" {
			p.BaseURL = substituteEnv(env, p.BaseURL)
		}
		if p.APIKey != "" {
			p.APIKey = substituteEnv(env, p.APIKey)
		}
		cfg.Providers[name] = p
	}
}

// NamedProviderPrefix reports the declared provider whose name prefixes
// modelName ("corp-claude/claude-opus-5" → "corp-claude", "claude-opus-5").
// Matching is case-insensitive on the prefix — users type these names in any
// case — and the longest prefix wins when one name extends another ("corp"
// and "corp-claude"). The returned name is the canonical key from Providers,
// so callers can look up the entry with it directly. ok is false for a model
// that carries no declared-provider prefix.
func (c *Config) NamedProviderPrefix(modelName string) (name, rest string, ok bool) {
	lower := strings.ToLower(modelName)
	bestLen := 0
	bestName := ""
	for n := range c.Providers {
		prefix := strings.ToLower(n) + "/"
		if strings.HasPrefix(lower, prefix) && len(prefix) > bestLen {
			bestLen = len(prefix)
			bestName = n
		}
	}
	if bestName == "" {
		return "", modelName, false
	}
	return bestName, modelName[bestLen:], true
}

// ResolveAPIKeys returns the environment-derived keys every built-in provider
// reads (APIKeys) plus each declared provider's key: the expanded apiKey, or
// the APIKeyEnv variable when no literal key is set. Entries without a key —
// a local endpoint that needs none — are left out, so callers keep treating
// an absent entry as "no key".
func (c *Config) ResolveAPIKeys() map[string]string {
	keys := APIKeys()
	for name, p := range c.Providers {
		key := p.APIKey
		if key == "" && p.APIKeyEnv != "" {
			key = os.Getenv(p.APIKeyEnv)
		}
		if key != "" {
			keys[name] = key
		}
	}
	return keys
}

// ContextWindowFor returns the context window declared for model on the named
// provider, in tokens. Zero means "not declared" — the model may still have a
// window in the embedded catalog, so callers treat zero as "keep looking",
// not "no window".
func (c *Config) ContextWindowFor(providerName, model string) int64 {
	p, ok := c.Providers[providerName]
	if !ok {
		return 0
	}
	return p.Models[model].ContextWindow
}
