package subagent

import (
	"strings"
	"testing"

	"github.com/spa-skyson/pi-rate/internal/config"
)

// corpConfig builds a config with a "smol" role on a declared provider "corp"
// and a "plan" role on the built-in "openai" provider.
func corpConfig() *config.Config {
	return &config.Config{
		Roles: map[string]config.RoleConfig{
			"smol": {Model: "gpt-5.6-sol", Provider: "corp"},
			"plan": {Model: "gpt-5.6-sol", Provider: "openai"},
		},
		Providers: map[string]config.ProviderConfig{
			"corp": {Type: "openai-compatible", BaseURL: "https://corp.example/v1"},
		},
	}
}

func TestAgentConfigModelFrontmatter(t *testing.T) {
	tests := []struct {
		name        string
		frontmatter string
		wantModel   string
		wantRole    string
	}{
		{"absent", "", "", ""},
		{"set", "model: corp-codex/gpt-5.6-sol\n", "corp-codex/gpt-5.6-sol", ""},
		{"alongside role", "role: smol\nmodel: corp-codex/gpt-5.6-sol\n", "corp-codex/gpt-5.6-sol", "smol"},
		{"trimmed", "model:    corp-codex/gpt-5.6-sol   \n", "corp-codex/gpt-5.6-sol", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := writeAgent(t, dir, "router", tt.frontmatter)

			cfg, err := ParseAgentFile(path)
			if err != nil {
				t.Fatalf("ParseAgentFile: %v", err)
			}
			if cfg.Model != tt.wantModel {
				t.Fatalf("Model = %q, want %q", cfg.Model, tt.wantModel)
			}
			if cfg.Role != tt.wantRole {
				t.Fatalf("Role = %q, want %q", cfg.Role, tt.wantRole)
			}
		})
	}
}

func TestAgentSpawnModel(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *config.Config
		agent   AgentConfig
		want    string
		wantErr string // substring
	}{
		{
			// A config with no roles would fail ResolveRole, so reaching the
			// model proves the frontmatter short-circuits the role lookup.
			name:  "frontmatter model wins, role never read",
			cfg:   &config.Config{},
			agent: AgentConfig{Name: "a", Model: "corp-codex/gpt-5.6-sol", Role: "no-such-role"},
			want:  "corp-codex/gpt-5.6-sol",
		},
		{
			name:  "role resolves its model",
			cfg:   corpConfig(),
			agent: AgentConfig{Name: "a", Role: "smol"},
			want:  "corp/gpt-5.6-sol",
		},
		{
			name:  "bare model on declared provider gets the prefix",
			cfg:   corpConfig(),
			agent: AgentConfig{Name: "a", Role: "smol"},
			want:  "corp/gpt-5.6-sol",
		},
		{
			name:  "bare model on built-in provider stays bare",
			cfg:   corpConfig(),
			agent: AgentConfig{Name: "a", Role: "plan"},
			want:  "gpt-5.6-sol",
		},
		{
			name: "already prefixed model is not double-prefixed",
			cfg: &config.Config{
				Roles: map[string]config.RoleConfig{
					"smol": {Model: "corp/gpt-5.6-sol"},
				},
				Providers: map[string]config.ProviderConfig{
					"corp": {BaseURL: "https://corp.example/v1"},
				},
			},
			agent: AgentConfig{Name: "a", Role: "smol"},
			want:  "corp/gpt-5.6-sol",
		},
		{
			name:    "unresolvable role errors",
			cfg:     &config.Config{},
			agent:   AgentConfig{Name: "a", Role: "smol"},
			wantErr: `resolving role "smol" for agent "a"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := agentSpawnModel(tt.cfg, tt.agent)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("agentSpawnModel: %v", err)
			}
			if got != tt.want {
				t.Fatalf("model = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestAgentSpawnBaseURL pins the named-provider override: the parent's
// explicit --url describes the parent model's endpoint, and a declared
// provider's child must read its own endpoint from config.json instead.
func TestAgentSpawnBaseURL(t *testing.T) {
	cfg := &config.Config{
		Providers: map[string]config.ProviderConfig{
			"corp": {BaseURL: "https://corp.example/v1"},
		},
	}
	tests := []struct {
		name      string
		parentURL string
		model     string
		want      string
	}{
		{"named model drops parent url", "https://parent.example/v1", "corp/gpt-5.6-sol", ""},
		{"built-in prefixed model keeps parent url", "https://parent.example/v1", "openai/gpt-5.6-sol", "https://parent.example/v1"},
		{"bare model keeps parent url", "https://parent.example/v1", "gpt-5.6-sol", "https://parent.example/v1"},
		{"no parent url stays empty", "", "openai/gpt-5.6-sol", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := agentSpawnBaseURL(cfg, tt.parentURL, tt.model); got != tt.want {
				t.Fatalf("baseURL = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestOrchestratorAgentModel pins the exported model lookup the TUI uses to
// label agent cards: the same raw string Spawn resolves — frontmatter model
// wins, otherwise the role — and empty for anything it cannot resolve.
func TestOrchestratorAgentModel(t *testing.T) {
	orch := NewOrchestrator(corpConfig(), "", []AgentConfig{
		{Name: "fronted", Model: "corp-codex/gpt-5.6-sol", Role: "no-such-role"},
		{Name: "roled", Role: "smol"},
	})
	tests := []struct {
		name string
		// agent is the registry name looked up; want is the expected model.
		agent string
		want  string
	}{
		{"frontmatter model wins without touching the role", "fronted", "corp-codex/gpt-5.6-sol"},
		{"role resolves to the provider-prefixed model", "roled", "corp/gpt-5.6-sol"},
		{"unknown agent yields empty", "nobody", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := orch.AgentModel(tt.agent); got != tt.want {
				t.Fatalf("AgentModel(%q) = %q, want %q", tt.agent, got, tt.want)
			}
		})
	}
}
