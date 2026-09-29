package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/permission"
	"github.com/dimetron/pi-go/internal/subagent"
	"github.com/dimetron/pi-go/internal/tools"
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
	if _, err := agentSwitch(context.Background(), config.Config{}, nil, "", in, switchTestConfigs(), "ghost"); err == nil {
		t.Error("unknown agent: expected an error")
	}
	if _, err := agentSwitch(context.Background(), config.Config{}, nil, "", in, switchTestConfigs(), "helper"); err == nil {
		t.Error("subagent-only agent: expected an error")
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
