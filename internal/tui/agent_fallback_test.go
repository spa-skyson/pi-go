package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	adkmodel "google.golang.org/adk/v2/model"

	"github.com/spa-skyson/pi-rate/internal/config"
	"github.com/spa-skyson/pi-rate/internal/subagent"
)

// errTerminalQuota is a quota-exhaustion failure as providers emit it: a
// terminal pattern from retry.IsTerminal, which the fallback path reacts to.
var errTerminalQuota = errors.New("you exceeded your current quota, check your plan and billing")

// newFallbackTestModel builds a minimal model for handleAgentDone calls: no
// agent, no lifecycle machinery — the same shape the existing handleAgentDone
// tests use. When ask is non-nil the switcher records every requested model
// name and answers with a stubLLM so the switch path runs to completion.
func newFallbackTestModel(t *testing.T, ask *[]string) *model {
	t.Helper()
	m := newTestModel(t)
	if ask != nil {
		m.cfg.ModelSwitcher = func(_ context.Context, modelName string) (adkmodel.LLM, string, string, error) {
			*ask = append(*ask, modelName)
			return &stubLLM{name: modelName}, modelName, "anthropic", nil
		}
	}
	return m
}

// transcriptContent joins the whole chat transcript, so assertions can look
// for a notice, the retry hint and the error in one string.
func transcriptContent(m *model) string {
	var sb strings.Builder
	for i := range m.chatModel.Messages {
		sb.WriteString(m.chatModel.Messages[i].content)
		sb.WriteString("\n")
	}
	return sb.String()
}

// TestFallbackSwitchOnTerminalError pins the first terminal error: the
// switcher is called with the first fallback from the agent frontmatter
// chain, a "switching to" notice lands in the chat, cfg carries the new
// model, and the chain position advances.
func TestFallbackSwitchOnTerminalError(t *testing.T) {
	var asked []string
	m := newFallbackTestModel(t, &asked)
	m.activeAgent = "pm"
	m.cfg.PrimaryAgents = []subagent.AgentConfig{
		{Name: "pm", FallbackModels: []string{"fb-one", "fb-two", "fb-three", "fb-four"}},
	}

	m.handleAgentDone(agentDoneMsg{err: errTerminalQuota})

	if len(asked) != 1 || asked[0] != "fb-one" {
		t.Fatalf("switcher got %v, want [fb-one]", asked)
	}
	if m.cfg.ModelName != "fb-one" {
		t.Errorf("ModelName = %q, want fb-one", m.cfg.ModelName)
	}
	if m.cfg.ProviderName != "anthropic" {
		t.Errorf("ProviderName = %q, want anthropic", m.cfg.ProviderName)
	}
	if m.modelFallbackUsed != 1 {
		t.Errorf("modelFallbackUsed = %d, want 1", m.modelFallbackUsed)
	}
	if m.agentModelOverride("pm") != "fb-one" {
		t.Errorf("agent override = %q, want fb-one (agent-scoped, session-only)", m.agentModelOverride("pm"))
	}
	if m.cfg.ActiveRole != "" {
		t.Errorf("ActiveRole = %q; an agent-scoped fallback must not touch role state", m.cfg.ActiveRole)
	}
	// The chat shows the switch and the original error; the retry hint
	// survives the switch because lastPromptFailed re-arms the prompt.
	joined := transcriptContent(m)
	for _, want := range []string{"switching to fb-one", "Error:", errTerminalQuota.Error()} {
		if !strings.Contains(joined, want) {
			t.Errorf("transcript lacks %q:\n%s", want, joined)
		}
	}
}

// TestFallbackSecondTerminalErrorUsesNextEntry pins the drain order: a second
// terminal error consumes the next unused chain entry, not the first one
// again.
func TestFallbackSecondTerminalErrorUsesNextEntry(t *testing.T) {
	var asked []string
	m := newFallbackTestModel(t, &asked)
	m.cfg.ActiveRole = "review"
	m.cfg.Roles = map[string]config.RoleConfig{
		"review": {Model: "primary", FallbackModels: []string{"fb-a", "fb-b", "fb-c"}},
	}

	m.handleAgentDone(agentDoneMsg{err: errTerminalQuota})
	m.handleAgentDone(agentDoneMsg{err: errTerminalQuota})

	if len(asked) != 2 || asked[0] != "fb-a" || asked[1] != "fb-b" {
		t.Fatalf("switcher got %v, want [fb-a fb-b] in order", asked)
	}
	if m.cfg.ModelName != "fb-b" {
		t.Errorf("ModelName = %q, want fb-b", m.cfg.ModelName)
	}
	if m.modelFallbackUsed != 2 {
		t.Errorf("modelFallbackUsed = %d, want 2", m.modelFallbackUsed)
	}
}

// TestFallbackChainExhausted pins the cap: after maxModelFallbacks switches
// the switcher stays silent and the error shows as before.
func TestFallbackChainExhausted(t *testing.T) {
	var asked []string
	m := newFallbackTestModel(t, &asked)
	m.cfg.Roles = map[string]config.RoleConfig{
		// Five configured entries — the consumer caps the chain at three.
		"default": {Model: "primary", FallbackModels: []string{"f1", "f2", "f3", "f4", "f5"}},
	}

	for i := 0; i < 5; i++ {
		m.handleAgentDone(agentDoneMsg{err: errTerminalQuota})
	}

	if len(asked) != maxModelFallbacks {
		t.Fatalf("switcher called %d times (%v), want %d", len(asked), asked, maxModelFallbacks)
	}
	if m.cfg.ModelName != "f3" {
		t.Errorf("ModelName = %q, want f3 (last consumed entry)", m.cfg.ModelName)
	}
	if m.modelFallbackUsed != maxModelFallbacks {
		t.Errorf("modelFallbackUsed = %d, want %d", m.modelFallbackUsed, maxModelFallbacks)
	}
	if !strings.Contains(transcriptContent(m), errTerminalQuota.Error()) {
		t.Error("final terminal error missing from the transcript")
	}
}

// TestFallbackNotOnNonTerminalError pins the error-class gate: a canceled
// turn is not a provider wall, so the switcher must not fire.
func TestFallbackNotOnNonTerminalError(t *testing.T) {
	var asked []string
	m := newFallbackTestModel(t, &asked)
	m.cfg.Roles = map[string]config.RoleConfig{
		"default": {Model: "primary", FallbackModels: []string{"f1"}},
	}

	m.handleAgentDone(agentDoneMsg{err: context.Canceled})
	m.handleAgentDone(agentDoneMsg{err: errors.New("agent loop aborted: model repeated a 106-character phrase 12 times")})

	if len(asked) != 0 {
		t.Errorf("switcher called for non-terminal errors: %v", asked)
	}
	if m.cfg.ModelName != "test-model" {
		t.Errorf("ModelName = %q, want it untouched", m.cfg.ModelName)
	}
	if m.modelFallbacks != nil {
		t.Errorf("chain latched %+v on non-terminal errors; want nil", m.modelFallbacks)
	}
}

// TestFallbackNilSwitcher pins the disabled path: without a ModelSwitcher the
// current behavior stands — error as-is, no panic, chain left latched so a
// later switcher finds the position.
func TestFallbackNilSwitcher(t *testing.T) {
	m := newFallbackTestModel(t, nil)
	m.cfg.Roles = map[string]config.RoleConfig{
		"default": {Model: "primary", FallbackModels: []string{"f1", "f2"}},
	}

	m.handleAgentDone(agentDoneMsg{err: errTerminalQuota})

	if m.cfg.ModelName != "test-model" {
		t.Errorf("ModelName = %q, want it untouched", m.cfg.ModelName)
	}
	if m.modelFallbackUsed != 0 {
		t.Errorf("modelFallbackUsed = %d, want 0 without a switcher", m.modelFallbackUsed)
	}
	if !strings.Contains(transcriptContent(m), errTerminalQuota.Error()) {
		t.Error("error missing from the transcript")
	}
}

// TestFallbackResetOnSuccessAndManualModel pins both resets: a successful
// turn re-resolves the chain on the next wall, and a manual /model discards
// the session chain entirely.
func TestFallbackResetOnSuccessAndManualModel(t *testing.T) {
	var asked []string
	m := newFallbackTestModel(t, &asked)
	m.cfg.Roles = map[string]config.RoleConfig{
		"default": {Model: "primary", FallbackModels: []string{"f1", "f2"}},
	}

	m.handleAgentDone(agentDoneMsg{err: errTerminalQuota})
	if len(asked) != 1 || m.modelFallbackUsed != 1 {
		t.Fatalf("first wall: asked=%v used=%d, want one switch", asked, m.modelFallbackUsed)
	}

	// Success re-arms the chain from scratch.
	m.handleAgentDone(agentDoneMsg{})
	if m.modelFallbacks != nil || m.modelFallbackUsed != 0 {
		t.Fatalf("success did not reset the chain: %+v used=%d", m.modelFallbacks, m.modelFallbackUsed)
	}

	// Second wall drains from the beginning again — the chain was
	// re-resolved, not continued.
	m.handleAgentDone(agentDoneMsg{err: errTerminalQuota})
	if len(asked) != 2 || asked[1] != "f1" {
		t.Fatalf("second wall: asked=%v, want f1 again after the reset", asked)
	}

	// A manual /model discards the chain: the next wall would re-resolve,
	// never continue the old sequence.
	m.handleSlashCommand("/model gpt-5.6-sol")
	if m.modelFallbacks != nil || m.modelFallbackUsed != 0 {
		t.Errorf("/model left a chain latched: %+v used=%d", m.modelFallbacks, m.modelFallbackUsed)
	}
	if m.cfg.ModelName != "gpt-5.6-sol" {
		t.Errorf("ModelName = %q, want gpt-5.6-sol after /model", m.cfg.ModelName)
	}
}

// TestFallbackFrontmatterOverridesRole pins the frontmatter-over-role
// priority stage A documented for the chain resolution.
func TestFallbackFrontmatterOverridesRole(t *testing.T) {
	var asked []string
	m := newFallbackTestModel(t, &asked)
	m.activeAgent = "pm"
	m.cfg.PrimaryAgents = []subagent.AgentConfig{
		{Name: "pm", FallbackModels: []string{"from-frontmatter"}},
	}
	m.cfg.Roles = map[string]config.RoleConfig{
		"default": {Model: "primary", FallbackModels: []string{"from-default-role"}},
	}

	m.handleAgentDone(agentDoneMsg{err: errTerminalQuota})

	if len(asked) != 1 || asked[0] != "from-frontmatter" {
		t.Fatalf("switcher got %v, want [from-frontmatter]", asked)
	}
}

// TestShortReasonKeepsFirstLine pins the transcript-side error condensation.
func TestShortReasonKeepsFirstLine(t *testing.T) {
	err := errors.New("first line\nsecond line\nthird line")
	if got := shortReason(err); got != "first line" {
		t.Errorf("shortReason = %q, want the first line only", got)
	}
	if got := shortReason(errors.New("single")); got != "single" {
		t.Errorf("shortReason single-line = %q", got)
	}
	if got := shortReason(nil); got != "" {
		t.Errorf("shortReason(nil) = %q, want empty", got)
	}
}
