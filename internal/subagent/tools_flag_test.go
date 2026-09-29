package subagent

import (
	"context"
	"strings"
	"testing"
)

// TestSpawnOptsToolsReachesArgs pins the tools allow-list seam: the child's
// tool surface is chosen by the parent, on the child's command line. Without
// the flag the child keeps its full tool set.
func TestSpawnOptsToolsReachesArgs(t *testing.T) {
	t.Run("empty leaves flag off", func(t *testing.T) {
		args := spawnArgs(SpawnOpts{Prompt: "do the thing"})
		for _, a := range args {
			if a == "--tools" {
				t.Fatalf("args contain --tools for empty Tools: %v", args)
			}
		}
		if args[len(args)-1] != "do the thing" {
			t.Fatalf("prompt is not the final arg: %v", args)
		}
	})

	t.Run("list is passed as one comma-joined value", func(t *testing.T) {
		args := spawnArgs(SpawnOpts{Prompt: "do the thing", Tools: []string{"read", "bash"}})
		idx := -1
		for i, a := range args {
			if a == "--tools" {
				idx = i
				break
			}
		}
		if idx == -1 {
			t.Fatalf("args missing --tools: %v", args)
		}
		if args[idx+1] != "read,bash" {
			t.Fatalf("--tools value = %q, want %q", args[idx+1], "read,bash")
		}
		// The prompt must stay last — it is positional.
		if args[len(args)-1] != "do the thing" {
			t.Fatalf("prompt is not the final arg: %v", args)
		}
	})
}

// TestOrchestratorForwardsAgentTools drives the fill path end to end: an
// agent's Tools must land in the SpawnOpts the child is launched with. The
// mock pi echoes its own arguments as its result, so the child command line
// is observable without parsing process tables.
func TestOrchestratorForwardsAgentTools(t *testing.T) {
	piScript := `printf '{"type":"text_delta","delta":"%s"}\n' "$*"`
	piBinary := mockPiScript(t, piScript)
	orch := NewOrchestrator(testConfig(), "", nil)
	orch.spawner = NewSpawner(piBinary)

	tests := []struct {
		name      string
		tools     []string
		wantFlag  bool
		wantValue string
	}{
		{"no tools, no flag", nil, false, ""},
		{"tools reach the child", []string{"read", "bash"}, true, "--tools read,bash"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events, _, err := orch.Spawn(context.Background(), SpawnInput{
				Agent:  AgentConfig{Name: "worker", Role: "smol", Tools: tt.tools},
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
			if tt.wantFlag && !strings.Contains(result, tt.wantValue) {
				t.Fatalf("child args %q missing %q", result, tt.wantValue)
			}
			if !tt.wantFlag && strings.Contains(result, "--tools") {
				t.Fatalf("child args %q contain --tools with empty Tools", result)
			}
		})
	}
	orch.Shutdown()
}
