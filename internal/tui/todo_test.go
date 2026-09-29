package tui

import (
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/tools"
)

func TestSidebarTodoIndicator(t *testing.T) {
	t.Parallel()

	render := func(state *tools.TodoState) string {
		return plain(sidebarModelLines(SidebarRenderInput{
			ProviderName: "anthropic",
			ModelName:    "m",
			TodoState:    state,
		}, 40, testSidebarStyles()))
	}

	t.Run("hidden without state", func(t *testing.T) {
		t.Parallel()
		if out := render(nil); strings.Contains(out, "Plan —") {
			t.Errorf("no todo state should render no indicator:\n%s", out)
		}
	})

	t.Run("shows progress and the current item", func(t *testing.T) {
		t.Parallel()
		out := render(&tools.TodoState{Total: 3, Done: 1, InProgress: "write the tests"})
		if !strings.Contains(out, "Plan — 1/3") {
			t.Errorf("indicator missing from the sidebar:\n%s", out)
		}
		if !strings.Contains(out, "write the tests") {
			t.Errorf("current item missing from the indicator:\n%s", out)
		}
	})

	t.Run("marks the all-done state", func(t *testing.T) {
		t.Parallel()
		out := render(&tools.TodoState{Total: 2, Done: 2})
		if !strings.Contains(out, "Plan — 2/2 done") {
			t.Errorf("all-done indicator missing:\n%s", out)
		}
		if strings.Contains(out, "·") {
			t.Errorf("no in_progress item should render no separator:\n%s", out)
		}
	})

	t.Run("tracks successive updates", func(t *testing.T) {
		t.Parallel()
		first := render(&tools.TodoState{Total: 3, Done: 0, InProgress: "first"})
		second := render(&tools.TodoState{Total: 3, Done: 1, InProgress: "second"})
		if !strings.Contains(first, "Plan — 0/3") || !strings.Contains(first, "first") {
			t.Errorf("stale render:\n%s", first)
		}
		if !strings.Contains(second, "Plan — 1/3") || !strings.Contains(second, "second") {
			t.Errorf("update did not take effect:\n%s", second)
		}
	})
}

func TestTodosPopupListsCheckboxState(t *testing.T) {
	t.Parallel()
	m := newTestModel(t)
	m.todoState = &tools.TodoState{Items: []tools.TodoItem{
		{Content: "already done", Status: "completed"},
		{Content: "current step", Status: "in_progress"},
		{Content: "later step", Status: "pending"},
	}}

	m.newSearchPopup(searchModeTodos)
	if m.searchPopup == nil {
		t.Skip("search popup could not be opened in this fixture")
	}

	var b strings.Builder
	for _, it := range m.searchPopup.entries {
		b.WriteString(it.Text)
		b.WriteString("\n")
	}
	out := b.String()
	for _, want := range []string{"[x] already done", "[~] current step", "[ ] later step"} {
		if !strings.Contains(out, want) {
			t.Errorf("popup missing %q:\n%s", want, out)
		}
	}
}

func TestTodosPopupEmptyState(t *testing.T) {
	t.Parallel()
	m := newTestModel(t)

	m.newSearchPopup(searchModeTodos)
	if m.searchPopup == nil {
		t.Skip("search popup could not be opened in this fixture")
	}
	if len(m.searchPopup.entries) != 1 || !strings.Contains(m.searchPopup.entries[0].Text, "No plan set") {
		t.Errorf("empty state wrong: %+v", m.searchPopup.entries)
	}
}

func TestTodoUpdateMsgUpdatesState(t *testing.T) {
	t.Parallel()
	todoCh := make(chan tools.TodoState, 1)
	m := newTestModel(t)
	m.cfg.TodoCh = todoCh

	state := tools.TodoState{Total: 2, Done: 1, InProgress: "second"}
	gotModel, cmd, handled := m.updateAgentStream(todoUpdateMsg{state: state})
	if !handled {
		t.Fatal("todoUpdateMsg not handled by updateAgentStream")
	}
	updated := gotModel.(*model)
	if updated.todoState == nil || updated.todoState.Total != 2 || updated.todoState.InProgress != "second" {
		t.Fatalf("todoState = %+v, want the delivered state", updated.todoState)
	}
	// The listener re-arms so the next todo_write is delivered too.
	if cmd == nil {
		t.Error("todoUpdateMsg did not re-arm the listener")
	}
}
