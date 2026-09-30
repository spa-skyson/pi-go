package cli

import (
	"testing"

	"github.com/spa-skyson/pi-rate/internal/config"
)

func providerHeadersConfig() config.Config {
	return config.Config{
		ExtraHeaders: map[string]string{"x-global": "g", "x-shared": "global"},
		Providers: map[string]config.ProviderConfig{
			"opencode-go": {
				BaseURL: "https://opencode.ai/zen/go/v1",
				Headers: map[string]string{
					"x-opencode-session": "${SESSION_ID}",
					"x-shared":           "provider", // provider beats the global
				},
			},
		},
	}
}

func TestProviderExtraHeaders(t *testing.T) {
	cfg := providerHeadersConfig()

	t.Run("precedence: flag beats provider beats global", func(t *testing.T) {
		got := providerExtraHeaders(cfg, "opencode-go", "ses_1", []string{"x-shared=flag", "x-flag=1"})
		want := map[string]string{
			"x-global":           "g",
			"x-shared":           "flag", // the explicit --header wins
			"x-flag":             "1",
			"x-opencode-session": "ses_1",
		}
		for k, v := range want {
			if got[k] != v {
				t.Errorf("[%q] = %q, want %q (all: %v)", k, got[k], v, got)
			}
		}
	})

	t.Run("provider header overrides the global one", func(t *testing.T) {
		got := providerExtraHeaders(cfg, "opencode-go", "ses_1", nil)
		if got["x-shared"] != "provider" {
			t.Errorf("x-shared = %q, want the provider's", got["x-shared"])
		}
	})

	t.Run("placeholder substituted with the session id", func(t *testing.T) {
		got := providerExtraHeaders(cfg, "opencode-go", "260929-1200-abcde-fghij", nil)
		if got["x-opencode-session"] != "260929-1200-abcde-fghij" {
			t.Errorf("x-opencode-session = %q", got["x-opencode-session"])
		}
	})

	t.Run("placeholder without a session id drops the header", func(t *testing.T) {
		got := providerExtraHeaders(cfg, "opencode-go", "", nil)
		if _, ok := got["x-opencode-session"]; ok {
			t.Errorf("empty header kept: %q", got["x-opencode-session"])
		}
		if got["x-global"] != "g" {
			t.Errorf("other headers lost: %v", got)
		}
	})

	t.Run("unknown provider keeps globals and flags", func(t *testing.T) {
		got := providerExtraHeaders(cfg, "anthropic", "ses", []string{"x-flag=1"})
		if got["x-global"] != "g" || got["x-flag"] != "1" || got["x-shared"] != "global" || len(got) != 3 {
			t.Errorf("got %v", got)
		}
	})

	t.Run("nothing anywhere yields nil", func(t *testing.T) {
		empty := config.Config{}
		if got := providerExtraHeaders(empty, "openai", "ses", nil); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})
}

func TestHeadersNeedSessionID(t *testing.T) {
	cfg := providerHeadersConfig()
	if !headersNeedSessionID(cfg, "opencode-go") {
		t.Error("provider with a ${SESSION_ID} header not detected")
	}
	if headersNeedSessionID(cfg, "anthropic") {
		t.Error("provider without headers flagged")
	}
	if headersNeedSessionID(config.Config{}, "opencode-go") {
		t.Error("empty config flagged")
	}
}
