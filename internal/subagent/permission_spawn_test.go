package subagent

import (
	"context"
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/permission"
)

// TestOrchestratorForwardsAgentPermission drives the env handoff end to end:
// the agent's frontmatter rules must reach the child process in
// PI_AGENT_PERMISSION, and an agent without rules must send nothing. Same
// mock-pi echo pattern as TestOrchestratorForwardsAgentSampling.
func TestOrchestratorForwardsAgentPermission(t *testing.T) {
	piScript := `printf '{"type":"text_delta","delta":"%s"}\n' "$PI_AGENT_PERMISSION"`
	piBinary := mockPiScript(t, piScript)
	orch := NewOrchestrator(testConfig(), "", nil)
	orch.spawner = NewSpawner(piBinary)

	t.Run("rules reach the child", func(t *testing.T) {
		events, _, err := orch.Spawn(context.Background(), SpawnInput{
			Agent: AgentConfig{
				Name: "worker",
				Role: "smol",
				Permission: permission.Rules{
					Tools: map[string]permission.Directive{"edit": permission.Deny, "serena*": permission.Deny},
					Bash: []permission.BashRule{
						{Pattern: "git *", Directive: permission.Allow},
					},
				},
			},
			Prompt: "go",
		})
		if err != nil {
			t.Fatalf("spawn: %v", err)
		}
		var result string
		for ev := range events {
			if ev.Type == "text_delta" {
				result += ev.Content
			}
		}
		for _, want := range []string{`"edit":"deny"`, `"serena*":"deny"`, `"pattern":"git *","directive":"allow"`} {
			if !strings.Contains(result, want) {
				t.Fatalf("child env %q missing %q", result, want)
			}
		}
	})

	t.Run("no rules, nothing sent", func(t *testing.T) {
		events, _, err := orch.Spawn(context.Background(), SpawnInput{
			Agent:  AgentConfig{Name: "worker", Role: "smol"},
			Prompt: "go",
		})
		if err != nil {
			t.Fatalf("spawn: %v", err)
		}
		var result string
		for ev := range events {
			if ev.Type == "text_delta" {
				result += ev.Content
			}
		}
		if result != "" {
			t.Fatalf("child saw %q, want an empty PI_AGENT_PERMISSION", result)
		}
	})
}

// The serialized payload must parse back into the same rules — the child
// reads it with permission.FromEnv.
func TestAgentPermissionEnvRoundTrip(t *testing.T) {
	want := permission.Rules{
		Tools: map[string]permission.Directive{"edit": permission.Deny},
		Bash: []permission.BashRule{
			{Pattern: "*", Directive: permission.Ask},
			{Pattern: "git *", Directive: permission.Allow},
		},
	}
	agent := AgentConfig{Name: "pm", Permission: want}
	if agent.Permission.Empty() {
		t.Fatal("rules reported empty")
	}

	t.Setenv(permission.EnvVar, `{"edit":"deny","bash":[{"pattern":"*","directive":"ask"},{"pattern":"git *","directive":"allow"}]}`)
	got, err := permission.FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if got.Tools["edit"] != want.Tools["edit"] || len(got.Bash) != 2 || got.Bash[1] != want.Bash[1] {
		t.Errorf("round trip = %+v, want %+v", got, want)
	}
}
