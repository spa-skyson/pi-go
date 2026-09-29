package subagent

import (
	"strings"
	"testing"
)

// TestOrchestrator_ResolveAgentInput pins the fix for user/project agents
// being invisible to SpawnWithInput: the agent must resolve through the
// orchestrator's registry (project > user > bundled), not through the
// bundled set alone.
func TestOrchestrator_ResolveAgentInput(t *testing.T) {
	configs := []AgentConfig{
		// A user/project agent: not a bundled name, carries a frontmatter model.
		{Name: "task2-probe", Role: "smol", Model: "corp-codex/gpt-5.6-sol", Description: "user agent"},
		// A bundled name, resolved through the same registry.
		{Name: "explore", Role: "smol", Description: "bundled agent"},
	}
	orch := NewOrchestrator(testConfig(), "", configs)

	t.Run("user agent resolves from registry", func(t *testing.T) {
		wt := true
		in := AgentInput{Type: "task2-probe", Prompt: "probe", Worktree: &wt, Timeout: 5000}
		spawn, err := orch.resolveAgentInput(in)
		if err != nil {
			t.Fatalf("resolveAgentInput: %v", err)
		}
		if spawn.Agent.Name != "task2-probe" {
			t.Fatalf("agent = %q, want task2-probe", spawn.Agent.Name)
		}
		if spawn.Agent.Model != "corp-codex/gpt-5.6-sol" {
			t.Fatalf("agent.Model = %q, want the frontmatter model", spawn.Agent.Model)
		}
		if spawn.Prompt != "probe" {
			t.Errorf("Prompt = %q, want probe", spawn.Prompt)
		}
		if spawn.Worktree == nil || !*spawn.Worktree {
			t.Errorf("Worktree = %v, want true", spawn.Worktree)
		}
		if spawn.Timeout != 5000 {
			t.Errorf("Timeout = %d, want 5000", spawn.Timeout)
		}
	})

	t.Run("bundled agent still resolves", func(t *testing.T) {
		spawn, err := orch.resolveAgentInput(AgentInput{Type: "explore", Prompt: "look"})
		if err != nil {
			t.Fatalf("resolveAgentInput: %v", err)
		}
		if spawn.Agent.Name != "explore" || spawn.Prompt != "look" {
			t.Fatalf("agent = %q, prompt = %q, want explore/look", spawn.Agent.Name, spawn.Prompt)
		}
	})

	t.Run("unknown name errors with registry list", func(t *testing.T) {
		_, err := orch.resolveAgentInput(AgentInput{Type: "no-such-agent", Prompt: "p"})
		if err == nil {
			t.Fatal("expected error for unknown agent")
		}
		if !strings.Contains(err.Error(), "unknown agent") {
			t.Fatalf("error = %v, want 'unknown agent'", err)
		}
		for _, name := range []string{"task2-probe", "explore"} {
			if !strings.Contains(err.Error(), name) {
				t.Errorf("error %q does not list registry agent %q", err, name)
			}
		}
	})
}
