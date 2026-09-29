package subagent

import (
	"context"
	"strings"
	"testing"
)

// TestAgentConfigSamplingFrontmatter covers the temperature / reasoningEffort /
// steps keys: valid values land, unusable ones warn and leave the field zero
// (= inherit / unlimited), mirroring the timeout handling.
func TestAgentConfigSamplingFrontmatter(t *testing.T) {
	tests := []struct {
		name            string
		frontmatter     string
		wantTemperature float64
		wantReasoning   string
		wantSteps       int
	}{
		{"absent", "", 0, "", 0},
		{"all set", "temperature: 0.3\nreasoningEffort: high\nsteps: 150\n", 0.3, "high", 150},
		{"trimmed", "temperature:    0.25   \nreasoningEffort:   HIGH   \nsteps:   120   \n", 0.25, "high", 120},
		{"temperature minimal", "temperature: 0\n", 0, "", 0},
		{"temperature garbage", "temperature: hot\n", 0, "", 0},
		{"temperature negative", "temperature: -1\n", 0, "", 0},
		{"steps garbage", "steps: many\n", 0, "", 0},
		{"steps negative", "steps: -5\n", 0, "", 0},
		{"effort empty stays empty", "reasoningEffort: \n", 0, "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := writeAgent(t, dir, "sampler", tt.frontmatter)

			cfg, err := ParseAgentFile(path)
			if err != nil {
				t.Fatalf("ParseAgentFile: %v", err)
			}
			if cfg.Temperature != tt.wantTemperature {
				t.Errorf("Temperature = %v, want %v", cfg.Temperature, tt.wantTemperature)
			}
			if cfg.ReasoningEffort != tt.wantReasoning {
				t.Errorf("ReasoningEffort = %q, want %q", cfg.ReasoningEffort, tt.wantReasoning)
			}
			if cfg.Steps != tt.wantSteps {
				t.Errorf("Steps = %d, want %d", cfg.Steps, tt.wantSteps)
			}
		})
	}
}

func TestNormalizeReasoningEffort(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"minimal", "none"},
		{"MINIMAL", "none"},
		{"high", "high"},
		{"HIGH", "high"},
		{" medium ", "medium"},
		{"max", "max"},
		{"none", "none"},
		{"extreme", ""},
		{"very-high", ""},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := normalizeReasoningEffort(tt.in); got != tt.want {
				t.Fatalf("normalizeReasoningEffort(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestSpawnOptsSamplingReachesArgs pins the child command line: the three
// sampling knobs travel as flags only when set, and never displace the
// positional prompt from the end.
func TestSpawnOptsSamplingReachesArgs(t *testing.T) {
	t.Run("zeros leave all flags off", func(t *testing.T) {
		args := spawnArgs(SpawnOpts{Prompt: "do the thing"})
		for _, flag := range []string{"--temperature", "--thinking", "--steps"} {
			for _, a := range args {
				if a == flag {
					t.Fatalf("args contain %s for zero values: %v", flag, args)
				}
			}
		}
		if args[len(args)-1] != "do the thing" {
			t.Fatalf("prompt is not the final arg: %v", args)
		}
	})

	t.Run("set values become flags", func(t *testing.T) {
		args := spawnArgs(SpawnOpts{
			Prompt:        "do the thing",
			Temperature:   0.3,
			ThinkingLevel: "high",
			Steps:         150,
		})
		joined := strings.Join(args, " ")
		for _, want := range []string{"--temperature 0.3", "--thinking high", "--steps 150"} {
			if !strings.Contains(joined, want) {
				t.Fatalf("args %v missing %q", args, want)
			}
		}
		if args[len(args)-1] != "do the thing" {
			t.Fatalf("prompt is not the final arg: %v", args)
		}
	})
}

// TestOrchestratorForwardsAgentSampling drives the fill path end to end: the
// frontmatter values must land on the child's command line, and an unknown
// reasoningEffort must be dropped rather than forwarded. Same mock-pi echo
// pattern as TestOrchestratorForwardsAgentTools.
func TestOrchestratorForwardsAgentSampling(t *testing.T) {
	piScript := `printf '{"type":"text_delta","delta":"%s"}\n' "$*"`
	piBinary := mockPiScript(t, piScript)
	orch := NewOrchestrator(testConfig(), "", nil)
	orch.spawner = NewSpawner(piBinary)

	t.Run("values reach the child", func(t *testing.T) {
		events, _, err := orch.Spawn(context.Background(), SpawnInput{
			Agent: AgentConfig{
				Name:            "worker",
				Role:            "smol",
				Temperature:     0.3,
				ReasoningEffort: "HIGH",
				Steps:           150,
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
		for _, want := range []string{"--temperature 0.3", "--thinking high", "--steps 150"} {
			if !strings.Contains(result, want) {
				t.Fatalf("child args %q missing %q", result, want)
			}
		}
	})

	t.Run("unknown effort is not forwarded", func(t *testing.T) {
		events, _, err := orch.Spawn(context.Background(), SpawnInput{
			Agent:  AgentConfig{Name: "worker", Role: "smol", ReasoningEffort: "extreme"},
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
		if strings.Contains(result, "--thinking") {
			t.Fatalf("child args %q contain --thinking for unknown effort", result)
		}
	})

	orch.Shutdown()
}
