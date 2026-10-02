package subagent

import (
	"reflect"
	"testing"

	"github.com/spa-skyson/pi-rate/internal/config"
)

// fallbackConfig extends corpConfig with fallback chains: on the "smol" role,
// on the "default" role, and a declared default provider.
func fallbackConfig() *config.Config {
	cfg := corpConfig()
	cfg.DefaultProvider = "corp"
	cfg.Roles["smol"] = config.RoleConfig{
		Model:          "gpt-5.6-sol",
		Provider:       "corp",
		FallbackModels: []string{"corp-codex/gpt-5.6-sol", "anthropic/claude-sonnet-4-6"},
	}
	cfg.Roles["default"] = config.RoleConfig{
		Model:          "gpt-5.6-sol",
		Provider:       "openai",
		FallbackModels: []string{"openai/gpt-5.6-mini"},
	}
	return cfg
}

func TestAgentFallbackModels(t *testing.T) {
	tests := []struct {
		name  string
		cfg   *config.Config
		agent AgentConfig
		want  []string
	}{
		{
			name:  "frontmatter wins over role",
			cfg:   fallbackConfig(),
			agent: AgentConfig{Role: "smol", FallbackModels: []string{"a/b"}},
			want:  []string{"a/b"},
		},
		{
			name:  "taken from the agent's role",
			cfg:   fallbackConfig(),
			agent: AgentConfig{Role: "smol"},
			want:  []string{"corp-codex/gpt-5.6-sol", "anthropic/claude-sonnet-4-6"},
		},
		{
			name:  "unknown role falls back to default role",
			cfg:   fallbackConfig(),
			agent: AgentConfig{Role: "nobody"},
			want:  []string{"openai/gpt-5.6-mini"},
		},
		{
			name: "no role fallback configured yields nil",
			cfg: &config.Config{
				Roles: map[string]config.RoleConfig{
					"smol": {Model: "m", Provider: "corp"},
				},
			},
			agent: AgentConfig{Role: "smol"},
			want:  nil,
		},
		{
			name:  "frontmatter chain is capped",
			cfg:   fallbackConfig(),
			agent: AgentConfig{Role: "smol", FallbackModels: []string{"a/1", "a/2", "a/3", "a/4", "a/5"}},
			want:  []string{"a/1", "a/2", "a/3"},
		},
		{
			name:  "bare name gets declared default provider prefix",
			cfg:   fallbackConfig(),
			agent: AgentConfig{Role: "smol", FallbackModels: []string{"gpt-5.6-sol"}},
			want:  []string{"corp/gpt-5.6-sol"},
		},
		{
			name: "bare name rides the primary model's provider, not the default one",
			cfg: func() *config.Config {
				cfg := fallbackConfig()
				cfg.DefaultProvider = "openai"
				cfg.Roles["smol"] = config.RoleConfig{
					Model:          "gpt-5.6-sol",
					Provider:       "corp",
					FallbackModels: []string{"gpt-5.6-mini"},
				}
				return cfg
			}(),
			agent: AgentConfig{Role: "smol"},
			want:  []string{"corp/gpt-5.6-mini"},
		},
		{
			name: "bare name falls back to the default provider when the primary has none",
			cfg: func() *config.Config {
				cfg := fallbackConfig()
				cfg.DefaultProvider = "corp"
				cfg.Roles["smol"] = config.RoleConfig{
					Model:          "plain-model",
					FallbackModels: []string{"gpt-5.6-mini"},
				}
				return cfg
			}(),
			agent: AgentConfig{Role: "smol"},
			want:  []string{"corp/gpt-5.6-mini"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := agentFallbackModels(tt.cfg, tt.agent)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("agentFallbackModels(%+v) = %v, want %v", tt.agent, got, tt.want)
			}
		})
	}
}

// TestAgentFallbackModelsDoesNotAliasFrontmatter pins that normalizing bare
// names does not write through to the shared agent config slice.
func TestAgentFallbackModelsDoesNotAliasFrontmatter(t *testing.T) {
	cfg := fallbackConfig()
	agent := AgentConfig{Role: "smol", FallbackModels: []string{"gpt-5.6-sol"}}
	if got := agentFallbackModels(cfg, agent); !reflect.DeepEqual(got, []string{"corp/gpt-5.6-sol"}) {
		t.Fatalf("agentFallbackModels = %v, want [corp/gpt-5.6-sol]", got)
	}
	if want := []string{"gpt-5.6-sol"}; !reflect.DeepEqual(agent.FallbackModels, want) {
		t.Fatalf("agent.FallbackModels mutated in place: %v, want %v", agent.FallbackModels, want)
	}
}

// TestAgentFallbackModelsUnknownDefaultProvider pins that a bare name passes
// through unchanged when no default provider is declared.
func TestAgentFallbackModelsUnknownDefaultProvider(t *testing.T) {
	cfg := &config.Config{
		Roles: map[string]config.RoleConfig{
			"smol": {Model: "m", Provider: "corp"},
		},
	}
	got := agentFallbackModels(cfg, AgentConfig{Role: "smol", FallbackModels: []string{"bare-model"}})
	if !reflect.DeepEqual(got, []string{"bare-model"}) {
		t.Fatalf("agentFallbackModels = %v, want [bare-model]", got)
	}
}
