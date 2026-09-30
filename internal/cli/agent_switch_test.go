package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/spa-skyson/pi-rate/internal/config"
	"github.com/spa-skyson/pi-rate/internal/guardrail"
	"github.com/spa-skyson/pi-rate/internal/permission"
	"github.com/spa-skyson/pi-rate/internal/subagent"
	"github.com/spa-skyson/pi-rate/internal/tools"
)

// switchTestConfigs is a discovery result shape: one primary, one all-mode,
// one plain subagent.
func switchTestConfigs() []subagent.AgentConfig {
	return []subagent.AgentConfig{
		{Name: "pm", Description: "orchestrator", Instruction: "Run the project.", Mode: subagent.ModePrimary, Permission: permission.Rules{
			Tools: map[string]permission.Directive{"edit": permission.Ask},
		}, Steps: 42},
		{Name: "build", Instruction: "Build it.", Mode: subagent.ModeAll},
		{Name: "helper", Instruction: "Help.", Mode: subagent.ModeSubagent},
	}
}

// TestResolveDefaultAgent covers the config.json defaultAgent triage: a
// primary agent resolves, an unknown name and a subagent-only name fall back
// with a notice.
func TestResolveDefaultAgent(t *testing.T) {
	configs := switchTestConfigs()

	ac, ok, notice := resolveDefaultAgent(configs, "pm")
	if !ok || ac.Name != "pm" || notice != "" {
		t.Errorf("resolveDefaultAgent(pm) = (%q, %v, %q), want pm/true/empty", ac.Name, ok, notice)
	}

	_, ok, notice = resolveDefaultAgent(configs, "ghost")
	if ok || !strings.Contains(notice, "not found") {
		t.Errorf("unknown defaultAgent: ok=%v notice=%q, want fallback with a notice", ok, notice)
	}

	// A subagent-only name exists but must not take over the main session.
	_, ok, notice = resolveDefaultAgent(configs, "helper")
	if ok || !strings.Contains(notice, "not a primary agent") {
		t.Errorf("subagent-only defaultAgent: ok=%v notice=%q, want fallback with a notice", ok, notice)
	}

	// mode: all is switchable too.
	if _, ok, _ = resolveDefaultAgent(configs, "build"); !ok {
		t.Error("mode: all agent was rejected as defaultAgent")
	}
}

// TestAgentSwitchRejectsUnusableTargets pins the switcher's soft-failure
// contract: unknown and non-primary names error before any LLM is built, so
// the TUI can show a notice and stay put.
func TestAgentSwitchRejectsUnusableTargets(t *testing.T) {
	in := &callbackInputs{}
	if _, err := agentSwitch(context.Background(), config.Config{}, nil, "", in, switchTestConfigs(), "ghost", ""); err == nil {
		t.Error("unknown agent: expected an error")
	}
	if _, err := agentSwitch(context.Background(), config.Config{}, nil, "", in, switchTestConfigs(), "helper", ""); err == nil {
		t.Error("subagent-only agent: expected an error")
	}
}

// TestAgentSwitchUpdatesContextWindow pins the fix for the stale context
// gauge: a switch that changes the model must also move the tracker's
// window. A declared-provider model (zai-coding-plan/glm-5.3, declared 1M)
// landing on a tracker preset with the startup model's 200k must end at 1M
// even with a global contextWindow that used to pin every switch to it.
func TestAgentSwitchUpdatesContextWindow(t *testing.T) {
	resetResolveFlags(t)

	cfg := config.Config{
		// The user's global override — it must not pin the declared model.
		ContextWindow: 200000,
		Providers: map[string]config.ProviderConfig{
			"zai-coding-plan": {
				Type:    "openai-compatible",
				BaseURL: "https://zai.example/v1",
				APIKey:  "sk-zai",
				Models:  map[string]config.ProviderModelConfig{"glm-5.3": {ContextWindow: 1_000_000}},
			},
		},
	}
	configs := []subagent.AgentConfig{
		{Name: "build", Instruction: "Build it.", Mode: subagent.ModePrimary, Model: "zai-coding-plan/glm-5.3"},
	}
	tracker := guardrail.New(0)
	tracker.SetContextWindowSize(200000) // the startup model's window

	in := &callbackInputs{cfg: cfg, deduper: tools.NewResultDeduper(), compactMetrics: tools.NewCompactMetrics()}
	sw, err := agentSwitch(context.Background(), cfg, tracker, "", in, configs, "build", "")
	if err != nil {
		t.Fatalf("agentSwitch: %v", err)
	}
	if sw.LLM == nil {
		t.Fatal("agentSwitch returned a nil LLM for an agent with a model")
	}
	if got := tracker.ContextWindowSize(); got != 1_000_000 {
		t.Errorf("tracker window after switch = %d, want the declared 1000000", got)
	}
}

// TestAgentSwitchLLM_Override pins the session-override precedence: a
// non-empty modelOverride beats the agent's own model: (and its window), an
// empty one leaves the agent's model standing.
func TestAgentSwitchLLM_Override(t *testing.T) {
	resetResolveFlags(t)

	cfg := config.Config{
		Providers: map[string]config.ProviderConfig{
			"zai-coding-plan": {
				Type:    "openai-compatible",
				BaseURL: "https://zai.example/v1",
				APIKey:  "sk-zai",
				Models: map[string]config.ProviderModelConfig{
					"glm-5.2": {ContextWindow: 500_000},
					"glm-5.3": {ContextWindow: 1_000_000},
				},
			},
		},
	}
	ac := &subagent.AgentConfig{Name: "build", Mode: subagent.ModePrimary, Model: "zai-coding-plan/glm-5.2"}
	tracker := guardrail.New(0)

	llm, modelName, providerName, err := agentSwitchLLM(context.Background(), cfg, tracker, "", ac, "zai-coding-plan/glm-5.3")
	if err != nil {
		t.Fatalf("agentSwitchLLM(override): %v", err)
	}
	if llm == nil || modelName != "zai-coding-plan/glm-5.3" || providerName != "zai-coding-plan" {
		t.Errorf("override switch = (%v, %q, %q), want the override model", llm != nil, modelName, providerName)
	}
	if got := tracker.ContextWindowSize(); got != 1_000_000 {
		t.Errorf("tracker window = %d, want the override's 1000000", got)
	}

	// Empty override: the agent's own model stands.
	tracker2 := guardrail.New(0)
	_, modelName, _, err = agentSwitchLLM(context.Background(), cfg, tracker2, "", ac, "")
	if err != nil {
		t.Fatalf("agentSwitchLLM(no override): %v", err)
	}
	if modelName != "zai-coding-plan/glm-5.2" {
		t.Errorf("model = %q, want the agent's zai-coding-plan/glm-5.2", modelName)
	}
	if got := tracker2.ContextWindowSize(); got != 500_000 {
		t.Errorf("tracker window = %d, want the agent model's 500000", got)
	}
}

// TestEffectiveAgentSteps: the agent's own budget wins, the --steps flag
// stands in when the agent sets none.
func TestEffectiveAgentSteps(t *testing.T) {
	defer func() { flagSteps = 0 }()
	flagSteps = 7

	if got := effectiveAgentSteps(42); got != 42 {
		t.Errorf("effectiveAgentSteps(42) = %d, want 42", got)
	}
	if got := effectiveAgentSteps(0); got != 7 {
		t.Errorf("effectiveAgentSteps(0) = %d, want the --steps value 7", got)
	}
}

// TestPrimaryAgentsFor filters and sorts the switchable subset.
func TestPrimaryAgentsFor(t *testing.T) {
	got := primaryAgentsFor(switchTestConfigs())
	if len(got) != 2 {
		t.Fatalf("primaryAgentsFor returned %d agents, want 2 (pm, build)", len(got))
	}
	if got[0].Name != "build" || got[1].Name != "pm" {
		t.Errorf("primaryAgentsFor not sorted by name: %q, %q", got[0].Name, got[1].Name)
	}
}

// TestCallbackInputsBuild_MergesAgentPermissionRules verifies the runtime
// rebuild really gates on the merged rule set: the default session has no
// permission callback for an empty config, and building with an agent whose
// frontmatter carries rules adds exactly one before-callback for it.
func TestCallbackInputsBuild_MergesAgentPermissionRules(t *testing.T) {
	// Model production: deferredInit creates the deduper and the compaction
	// metrics once on the shared inputs, and every switch rebuilds from the
	// same callbackInputs.
	in := callbackInputs{
		cfg:            config.Config{},
		deduper:        tools.NewResultDeduper(),
		compactMetrics: tools.NewCompactMetrics(),
	}

	base := in.build(permission.Rules{}, 0)
	merged := in.build(permission.Merge(permission.Rules{}, switchTestConfigs()[0].Permission), 42)

	if len(merged.beforeTool) != len(base.beforeTool)+1 {
		t.Fatalf("merged rules should add exactly the permission callback: base=%d merged=%d",
			len(base.beforeTool), len(merged.beforeTool))
	}
	// The rebuilt chains stay composed singles and keep the shared deduper —
	// the auto-compact hook holds the original instance across switches.
	if len(merged.afterTool) != 1 {
		t.Fatalf("after-tool chain has %d entries, want 1 composed callback", len(merged.afterTool))
	}
	if merged.deduper != base.deduper {
		t.Error("the rebuild allocated a new deduper; the auto-compact hook would track the wrong one")
	}
	if merged.compactMetrics != base.compactMetrics {
		t.Error("the rebuild allocated new compaction metrics; the context gauge would lose the count")
	}
}
