package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	adkmodel "google.golang.org/adk/v2/model"

	"github.com/dimetron/pi-go/internal/agent"
	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/subagent"
)

// popupTestModel builds a model for the /model and /agent picker popup tests:
// a real agent over a stub LLM, a recording ModelSwitcher, and a candidate
// list covering both sources the cli ships — declared provider models and
// roles.
func popupTestModel(t *testing.T) (*model, *[]string) {
	t.Helper()
	ag, err := agent.New(agent.Config{
		Model:       &stubLLM{name: "base"},
		Instruction: "Built-in prompt.",
	})
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	var switched []string
	m := &model{
		ctx:         context.Background(),
		activeAgent: "",
		chatModel:   ChatModel{Messages: make([]message, 0)},
		cfg: Config{
			Agent: ag,
			ModelSwitcher: func(_ context.Context, modelName string) (adkmodel.LLM, string, string, error) {
				switched = append(switched, modelName)
				return &stubLLM{name: modelName}, modelName, "anthropic", nil
			},
			PrimaryAgents: []subagent.AgentConfig{
				{Name: "pm", Description: "orchestrator", Mode: subagent.ModePrimary},
			},
			ModelCandidates: []SearchItem{
				{Text: "zai-coding-plan/glm-5.3", Description: "zai-coding-plan · 200K"},
				{Text: "default", Description: "role · glm-5.3 [zai-coding-plan]"},
			},
			Roles: map[string]config.RoleConfig{
				"default": {Model: "glm-5.3"},
			},
		},
	}
	return m, &switched
}

// submit runs a slash command through the same dispatch a submitted prompt
// takes.
func submit(t *testing.T, m *model, input string) *model {
	t.Helper()
	next, _ := m.handleSlashCommand(input)
	mm, ok := next.(*model)
	if !ok {
		t.Fatalf("handleSlashCommand returned %T, want *model", next)
	}
	return mm
}

// /model with no argument opens the models popup seeded from the declared
// candidates; with an argument the switch happens as before, no popup.
func TestModelCommandNoArgsOpensModelsPopup(t *testing.T) {
	m, switched := popupTestModel(t)

	m = submit(t, m, "/model")
	if m.searchPopup == nil {
		t.Fatal("/model did not open a popup")
	}
	if m.searchPopup.mode != searchModeModels {
		t.Fatalf("popup mode = %q, want %q", m.searchPopup.mode, searchModeModels)
	}
	if len(m.searchPopup.filtered) != 2 {
		t.Fatalf("popup shows %d entries, want 2", len(m.searchPopup.filtered))
	}
	if len(*switched) != 0 {
		t.Errorf("opening the popup switched models: %v", *switched)
	}

	// Esc closes without touching anything.
	m = press(t, m, tea.KeyEsc)
	if m.searchPopup != nil {
		t.Fatal("Esc did not close the models popup")
	}

	// With an argument the old switch path runs and no popup opens.
	m = submit(t, m, "/model zai-coding-plan/glm-5.3")
	if m.searchPopup != nil {
		t.Fatal("/model with an argument opened a popup")
	}
	if len(*switched) != 1 || (*switched)[0] != "zai-coding-plan/glm-5.3" {
		t.Fatalf("switcher got %v, want [zai-coding-plan/glm-5.3]", *switched)
	}
}

// Enter in the models popup executes the selected candidate through the
// existing /model handler — a role candidate resolves through the role, so
// the switcher sees the role's model.
func TestModelsPopupEnterSwitchesModel(t *testing.T) {
	m, switched := popupTestModel(t)
	m = submit(t, m, "/model")
	m.searchPopup.selected = 1 // the role candidate

	m = press(t, m, tea.KeyEnter)

	if m.searchPopup != nil {
		t.Fatal("Enter did not close the models popup")
	}
	if len(*switched) != 1 || (*switched)[0] != "glm-5.3" {
		t.Fatalf("switcher got %v, want [glm-5.3] (resolved from the default role)", *switched)
	}
	// The role resolved through the handler, so the transcript carries the
	// usual "Switched model" confirmation.
	if len(m.chatModel.Messages) == 0 || !strings.Contains(m.chatModel.Messages[0].content, "Switched model") {
		t.Fatalf("expected the switch confirmation, got %+v", m.chatModel.Messages)
	}
}

// Enter on a model while a primary agent is active takes the override branch:
// session-only override, no role write.
func TestModelsPopupEnterWithActiveAgentOverrides(t *testing.T) {
	m, switched := popupTestModel(t)
	m.activeAgent = "pm"
	m = submit(t, m, "/model")

	m = press(t, m, tea.KeyEnter)

	if got := m.agentModelOverride("pm"); got != "zai-coding-plan/glm-5.3" {
		t.Errorf("override for pm = %q, want zai-coding-plan/glm-5.3", got)
	}
	if m.cfg.ActiveRole != "" {
		t.Errorf("ActiveRole moved to %q; the override branch must not touch role state", m.cfg.ActiveRole)
	}
	if len(*switched) != 1 || (*switched)[0] != "zai-coding-plan/glm-5.3" {
		t.Fatalf("switcher got %v, want [zai-coding-plan/glm-5.3]", *switched)
	}
}

// The models popup refuses nothing itself: the refusal while running comes
// from the /model handler the Enter path routes through.
func TestModelsPopupEnterRefusedWhileRunning(t *testing.T) {
	m, switched := popupTestModel(t)
	m.running = true
	m = submit(t, m, "/model")
	if m.searchPopup == nil {
		t.Fatal("/model did not open the popup while running")
	}

	m = press(t, m, tea.KeyEnter)

	if len(*switched) != 0 {
		t.Errorf("switcher was called while a response is running: %v", *switched)
	}
	if m.searchPopup != nil {
		t.Fatal("Enter did not close the popup")
	}
}

// The background refresh replaces the open popup's entries; a closed popup
// drops the result, and an empty result changes nothing.
func TestModelsPopupBackgroundRefresh(t *testing.T) {
	m, _ := popupTestModel(t)

	// Opening with a refresh source arms exactly one fetch.
	var calls int
	m.cfg.ModelCandidatesRefresh = func(context.Context) []SearchItem {
		calls++
		return []SearchItem{
			{Text: "zai-coding-plan/glm-5.3", Description: "zai-coding-plan"},
			{Text: "opencode-go/glm-5.3-flash", Description: "opencode-go"},
			{Text: "default", Description: "role · glm-5.3"},
		}
	}
	next, cmd := m.handleSlashCommand("/model")
	m = next.(*model)
	if m.searchPopup == nil {
		t.Fatal("popup did not open")
	}
	if cmd == nil {
		t.Fatal("a configured refresh source produced no command")
	}
	msg := cmd()
	got, ok := msg.(modelCandidatesMsg)
	if !ok {
		t.Fatalf("refresh cmd returned %T, want modelCandidatesMsg", msg)
	}
	if calls != 1 {
		t.Errorf("refresh called %d times, want 1 (the armed cmd)", calls)
	}

	// Applying it to the open popup replaces the entries and keeps the query.
	m.searchPopup.search = "flash"
	m.searchPopup.filterSearch()
	next, _, _ = m.updateSession(modelCandidatesMsg{items: got.items})
	m = next.(*model)
	if got := m.searchPopup.filtered; len(got) != 1 || got[0].Text != "opencode-go/glm-5.3-flash" {
		t.Fatalf("filtered after refresh = %+v, want the one flash model", got)
	}

	// Same message with the popup closed: dropped, no state change.
	m.searchPopup = nil
	next, _, _ = m.updateSession(modelCandidatesMsg{items: got.items})
	m = next.(*model)
	if m.searchPopup != nil {
		t.Error("a refresh must not reopen a closed popup")
	}

	// Empty result: the open popup keeps its list (reopened here, so the seed
	// list of two).
	m = submit(t, m, "/model")
	next, _, _ = m.updateSession(modelCandidatesMsg{items: nil})
	m = next.(*model)
	if len(m.searchPopup.filtered) != 2 {
		t.Errorf("empty refresh changed the popup: %+v", m.searchPopup.filtered)
	}
}

// A nil refresh source still opens the popup, just without the fetch.
func TestModelsPopupNilRefreshOpensWithoutFetch(t *testing.T) {
	m, _ := popupTestModel(t)
	m.cfg.ModelCandidatesRefresh = nil

	m = submit(t, m, "/model")
	if m.searchPopup == nil || m.searchPopup.mode != searchModeModels {
		t.Fatal("popup did not open with a nil refresh source")
	}
	if cmd := m.openModelsPopup(); cmd != nil {
		t.Error("a nil refresh source must produce no command")
	}
}

// Nothing declared and no refresh source: no popup at all.
func TestModelsPopupSkippedWhenEmpty(t *testing.T) {
	m, _ := popupTestModel(t)
	m.cfg.ModelCandidates = nil
	m.cfg.ModelCandidatesRefresh = nil

	m = submit(t, m, "/model")
	if m.searchPopup != nil {
		t.Fatal("a popup with no candidates and no source must not open")
	}
}

// /agent with no argument opens the agents popup; Enter applies the selected
// agent through the same path as /agent <name>; Esc closes without switching.
func TestAgentsPopupOpenEnterEscape(t *testing.T) {
	var asked []string
	m := newSwitchTestModel(t, &asked)

	m = submit(t, m, "/agent")
	if m.searchPopup == nil || m.searchPopup.mode != searchModeAgents {
		t.Fatal("/agent did not open the agents popup")
	}
	if got := agentSearchEntriesTexts(m); len(got) != 3 || got[0] != "default" || got[1] != "build" || got[2] != "pm" {
		t.Fatalf("popup entries = %v, want [default build pm]", got)
	}

	// Down to "build", Enter applies it.
	m = press(t, m, tea.KeyDown)
	m = press(t, m, tea.KeyEnter)
	if m.searchPopup != nil {
		t.Fatal("Enter did not close the agents popup")
	}
	if m.activeAgent != "build" || len(asked) != 1 || asked[0] != "build/" {
		t.Fatalf("after Enter: active=%q asked=%v, want build applied once", m.activeAgent, asked)
	}

	// Reopening marks build active; Esc changes nothing.
	m = submit(t, m, "/agent")
	descs := make(map[string]string)
	for _, it := range m.searchPopup.filtered {
		descs[it.Text] = it.Description
	}
	if !strings.Contains(descs["build"], "active") || strings.Contains(descs["default"], "active") {
		t.Errorf("active marker wrong: default=%q build=%q", descs["default"], descs["build"])
	}
	m = press(t, m, tea.KeyEsc)
	if m.searchPopup != nil || m.activeAgent != "build" {
		t.Fatalf("Esc must close without switching (popup=%v active=%q)", m.searchPopup, m.activeAgent)
	}
}

// Filtering the agents popup uses the same substring search as the other
// modes.
func TestAgentsPopupFilterSearch(t *testing.T) {
	var asked []string
	m := newSwitchTestModel(t, &asked)
	m = submit(t, m, "/agent")

	m.searchPopup.search = "orch"
	m.searchPopup.filterSearch()
	if got := agentSearchEntriesTexts(m); len(got) != 1 || got[0] != "pm" {
		t.Fatalf("filter %q matched %v, want [pm]", "orch", got)
	}
}

func agentSearchEntriesTexts(m *model) []string {
	if m.searchPopup == nil {
		return nil
	}
	out := make([]string, 0, len(m.searchPopup.filtered))
	for _, it := range m.searchPopup.filtered {
		out = append(out, it.Text)
	}
	return out
}

// The popup renders in the agents and models modes: header, item text and the
// description column, and the per-mode no-match line.
func TestModelsAgentsPopupRender(t *testing.T) {
	m, _ := popupTestModel(t)
	m.width, m.height = 100, 40

	m = submit(t, m, "/model")
	view := m.renderSearchPopup(100)
	for _, want := range []string{"Models", "zai-coding-plan/glm-5.3", "role · glm-5.3 [zai-coding-plan]"} {
		if !strings.Contains(view, want) {
			t.Errorf("models popup view missing %q", want)
		}
	}

	// An open popup with no entries renders the per-mode no-match line
	// (candidates removed after the popup opened, refresh pending).
	m.searchPopup.entries = nil
	m.searchPopup.filterSearch()
	view = m.renderSearchPopup(100)
	if !strings.Contains(view, "No matching models") {
		t.Errorf("empty models popup view missing the no-match line: %q", view)
	}

	m = submit(t, m, "/agent")
	view = m.renderSearchPopup(100)
	if !strings.Contains(view, "Agents") || !strings.Contains(view, "default") {
		t.Errorf("agents popup view = %q", view)
	}
	// The agents list is never empty (default is always there), so the
	// no-match line only ever shows for models; filterSearch falls back to
	// the full list on a miss.
	m.searchPopup.search = "zzz"
	m.searchPopup.filterSearch()
	if got := agentSearchEntriesTexts(m); len(got) != 2 {
		t.Errorf("a missed filter must show all entries, got %v", got)
	}
}
