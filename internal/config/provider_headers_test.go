package config

import (
	"encoding/json"
	"testing"
)

func TestProviderHeadersParse(t *testing.T) {
	var cfg Config
	blob := `{"providers":{"opencode-go":{"type":"openai-compatible","baseURL":"https://opencode.ai/zen/go/v1","headers":{"x-opencode-session":"${SESSION_ID}","x-custom":"static"}}}}`
	if err := json.Unmarshal([]byte(blob), &cfg); err != nil {
		t.Fatal(err)
	}
	p := cfg.Providers["opencode-go"]
	if p.Headers["x-opencode-session"] != "${SESSION_ID}" {
		t.Errorf("x-opencode-session = %q, want the verbatim placeholder", p.Headers["x-opencode-session"])
	}
	if p.Headers["x-custom"] != "static" {
		t.Errorf("x-custom = %q", p.Headers["x-custom"])
	}
}

func TestProviderHeadersEnvSubstitution(t *testing.T) {
	t.Setenv("PI_TEST_ZEN_TOKEN", "tok-123")
	cfg := Config{Providers: map[string]ProviderConfig{
		"zen": {BaseURL: "https://x.invalid", Headers: map[string]string{
			"authorization": "Bearer ${PI_TEST_ZEN_TOKEN}",
			"x-session":     "${SESSION_ID}",
			"x-both":        "${PI_TEST_ZEN_TOKEN}/${SESSION_ID}",
		}},
	}}

	substituteProviderEnv(&cfg, ".")

	h := cfg.Providers["zen"].Headers
	if h["authorization"] != "Bearer tok-123" {
		t.Errorf("authorization = %q, want the env var expanded", h["authorization"])
	}
	if h["x-session"] != "${SESSION_ID}" {
		// ${SESSION_ID} is not env: env substitution must not blank it.
		t.Errorf("x-session = %q, want the placeholder preserved", h["x-session"])
	}
	if h["x-both"] != "tok-123/${SESSION_ID}" {
		t.Errorf("x-both = %q, want env expanded and placeholder preserved", h["x-both"])
	}
}

func TestProviderHeadersOmittedWhenUnset(t *testing.T) {
	blob, err := json.Marshal(ProviderConfig{BaseURL: "https://x.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	if string(blob) != `{"baseURL":"https://x.invalid"}` {
		t.Errorf("marshaled = %s, want no headers key", blob)
	}
}
