package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/dimetron/pi-go/internal/agent"
	"github.com/dimetron/pi-go/internal/subagent"
)

// switchTestAgents returns two primary agent configs, deliberately listed
// out of alphabetical order: the cycle and the listing must sort.
func switchTestAgents() []subagent.AgentConfig {
	return []subagent.AgentConfig{
		{Name: "pm", Description: "orchestrator", Mode: subagent.ModePrimary},
		{Name: "build", Description: "executor", Mode: subagent.ModePrimary},
	}
}

// newSwitchTestModel builds a model wired to a real agent over a stub LLM,
// with a switcher that records the requested name and applies a distinct
// prompt per target, so the test can see what reached the runner.
func newSwitchTestModel(t *testing.T, asked *[]string) *model {
	t.Helper()
	ag, err := agent.New(agent.Config{
		Model:       &stubLLM{name: "base"},
		Instruction: "Built-in prompt.",
	})
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	switcher := func(_ context.Context, name string) (AgentSwitch, error) {
		*asked = append(*asked, name)
		instruction := "Built-in prompt."
		if name != "" {
			instruction = "Prompt of " + name + "."
		}
		return AgentSwitch{Instruction: instruction}, nil
	}
	return &model{
		ctx:         context.Background(),
		activeAgent: "",
		chatModel:   ChatModel{Messages: make([]message, 0)},
		cfg: Config{
			Agent:         ag,
			PrimaryAgents: switchTestAgents(),
			AgentSwitcher: switcher,
		},
	}
}

// TestNextAgentName pins the Shift+Tab cycle: default hands off to the first
// agent alphabetically, the last agent wraps back to default.
func TestNextAgentName(t *testing.T) {
	names := primaryAgentNames(switchTestAgents())
	if names[0] != "build" || names[1] != "pm" {
		t.Fatalf("primaryAgentNames not sorted: %v", names)
	}

	cases := []struct {
		current string
		want    string
	}{
		{"", "build"},      // default → first agent
		{"build", "pm"},    // alphabetical step
		{"pm", ""},         // last agent wraps to default
		{"ghost", "build"}, // vanished agent restarts the cycle
	}
	for _, tc := range cases {
		if got := nextAgentName(tc.current, names); got != tc.want {
			t.Errorf("nextAgentName(%q) = %q, want %q", tc.current, got, tc.want)
		}
	}
	// No primary agents: the cycle stays on default.
	if got := nextAgentName("", nil); got != "" {
		t.Errorf("nextAgentName with no agents = %q, want empty", got)
	}
}

// TestCycleAgentSequence drives Shift+Tab through the full cycle and back,
// asserting the switcher receives the right name at each stop and the
// transcript carries a notice.
func TestCycleAgentSequence(t *testing.T) {
	var asked []string
	m := newSwitchTestModel(t, &asked)

	shiftTab := (tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}).Key()
	want := []string{"build", "pm", "", "build"}
	for i, name := range want {
		newM, _, _ := m.handleToggleKey(shiftTab)
		m = newM.(*model)
		if m.activeAgent != name {
			t.Fatalf("step %d: activeAgent = %q, want %q", i, m.activeAgent, name)
		}
	}
	if len(asked) != len(want) {
		t.Fatalf("switcher called %d times, want %d", len(asked), len(want))
	}
	for i, name := range want {
		if asked[i] != name {
			t.Errorf("switcher call %d: got %q, want %q", i, asked[i], name)
		}
	}
	if !strings.Contains(m.chatModel.Messages[0].content, "Switched to agent **build**") {
		t.Errorf("expected a switch notice, got %q", m.chatModel.Messages[0].content)
	}
}

// TestShiftTabRefusesWhileRunning mirrors /model: no agent swap underneath a
// live turn.
func TestShiftTabRefusesWhileRunning(t *testing.T) {
	var asked []string
	m := newSwitchTestModel(t, &asked)
	m.running = true

	newM, _, _ := m.handleToggleKey((tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}).Key())
	m = newM.(*model)
	if m.activeAgent != "" {
		t.Errorf("agent switched while running: %q", m.activeAgent)
	}
	if len(asked) != 0 {
		t.Error("switcher was called while a response is running")
	}
	if m.flash == "" {
		t.Error("expected a refusal flash")
	}
}

// TestHandleAgentCommandSwitch drives /agent <name>: the switcher receives
// the exact name, "default" maps to the built-in agent, and the notice
// confirms.
func TestHandleAgentCommandSwitch(t *testing.T) {
	var asked []string
	m := newSwitchTestModel(t, &asked)

	newM, _ := m.handleSlashCommand("/agent pm")
	m = newM.(*model)
	if m.activeAgent != "pm" {
		t.Errorf("activeAgent = %q, want pm", m.activeAgent)
	}
	if len(asked) != 1 || asked[0] != "pm" {
		t.Fatalf("switcher got %v, want [pm]", asked)
	}

	newM, _ = m.handleSlashCommand("/agent default")
	m = newM.(*model)
	if m.activeAgent != "" {
		t.Errorf("activeAgent = %q, want empty (default)", m.activeAgent)
	}
	if last := asked[len(asked)-1]; last != "" {
		t.Errorf("switcher got %q, want \"\" for default", last)
	}
}

// TestHandleAgentCommandUnknown shows the soft failure: an unknown name is a
// notice and the active agent does not move.
func TestHandleAgentCommandUnknown(t *testing.T) {
	var asked []string
	m := newSwitchTestModel(t, &asked)
	// The switcher itself rejects unknown names, as the CLI one does
	// (unknown agent / non-primary are pre-checked errors there).
	m.cfg.AgentSwitcher = func(_ context.Context, name string) (AgentSwitch, error) {
		asked = append(asked, name)
		return AgentSwitch{}, errUnknownTestAgent
	}

	newM, _ := m.handleSlashCommand("/agent nope")
	m = newM.(*model)
	if m.activeAgent != "" {
		t.Errorf("activeAgent moved to %q on a failed switch", m.activeAgent)
	}
	if len(asked) != 1 || asked[0] != "nope" {
		t.Fatalf("switcher got %v, want [nope]", asked)
	}
	if !strings.Contains(m.chatModel.Messages[0].content, "Agent switch failed") {
		t.Errorf("expected a failure notice, got %q", m.chatModel.Messages[0].content)
	}
}

var errUnknownTestAgent = &testError{"unknown agent"}

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }

// TestHandleAgentCommandList checks the no-arg listing: every switchable
// agent with its description, the active one marked.
func TestHandleAgentCommandList(t *testing.T) {
	var asked []string
	m := newSwitchTestModel(t, &asked)
	m.activeAgent = "pm"

	newM, _ := m.handleSlashCommand("/agent")
	m = newM.(*model)
	content := m.chatModel.Messages[0].content
	for _, want := range []string{"default", "build", "pm", "orchestrator", "←"} {
		if !strings.Contains(content, want) {
			t.Errorf("listing missing %q:\n%s", want, content)
		}
	}
}

// TestAgentSidebarIndicator keeps the status display honest: the sidebar
// input carries the active agent name.
func TestAgentSidebarIndicator(t *testing.T) {
	var asked []string
	m := newSwitchTestModel(t, &asked)

	newM, _ := m.handleSlashCommand("/agent build")
	m = newM.(*model)
	in := m.sidebarRenderInput(30, 40)
	if in.AgentName != "build" {
		t.Errorf("sidebar AgentName = %q, want build", in.AgentName)
	}
}
