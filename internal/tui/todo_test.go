package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"

	"github.com/spa-skyson/pi-rate/internal/extension"
	"github.com/spa-skyson/pi-rate/internal/tools"
)

func TestSidebarTodoSection(t *testing.T) {
	t.Parallel()

	render := func(state *tools.TodoState) string {
		return plain(sidebarTodoLines(SidebarRenderInput{
			TodoState: state,
		}, 40, testSidebarStyles()))
	}

	t.Run("hidden without state", func(t *testing.T) {
		t.Parallel()
		if out := render(nil); strings.Contains(out, "Plan") {
			t.Errorf("no todo state should render no section:\n%s", out)
		}
	})

	t.Run("hidden with aggregate only", func(t *testing.T) {
		t.Parallel()
		// Total without items is not a list to display; the section hides
		// rather than draw an empty checklist.
		out := render(&tools.TodoState{Total: 3, Done: 1})
		if strings.Contains(out, "Plan") || strings.Contains(out, "[ ]") {
			t.Errorf("aggregate-only state should render no section:\n%s", out)
		}
	})

	t.Run("renders every status", func(t *testing.T) {
		t.Parallel()
		out := render(&tools.TodoState{
			Total: 3, Done: 1, InProgress: "current step",
			Items: []tools.TodoItem{
				{Content: "already done", Status: "completed"},
				{Content: "current step", Status: "in_progress"},
				{Content: "later step", Status: "pending"},
			},
		})
		for _, want := range []string{
			"Plan — 1/3",
			"[x] already done",
			"[~] current step",
			"[ ] later step",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("plan section missing %q:\n%s", want, out)
			}
		}
	})

	t.Run("marks the all-done state", func(t *testing.T) {
		t.Parallel()
		out := render(&tools.TodoState{
			Total: 2, Done: 2,
			Items: []tools.TodoItem{
				{Content: "a", Status: "completed"},
				{Content: "b", Status: "completed"},
			},
		})
		if !strings.Contains(out, "Plan — 2/2 done") {
			t.Errorf("all-done heading missing:\n%s", out)
		}
	})

	t.Run("wraps long content with continuation indent", func(t *testing.T) {
		t.Parallel()
		long := "write a very long item title that definitely does not fit on one sidebar line at all"
		out := render(&tools.TodoState{
			Total: 1,
			Items: []tools.TodoItem{{Content: long, Status: "in_progress"}},
		})
		if !strings.Contains(out, "\n      ") {
			t.Errorf("long item did not wrap with a continuation indent:\n%s", out)
		}
	})

	t.Run("renders the full list", func(t *testing.T) {
		t.Parallel()
		// Full expansion is the owner's requirement; realistic plans
		// (5–20 steps) render whole, no "+N more".
		items := make([]tools.TodoItem, 0, 12)
		for i := 0; i < 12; i++ {
			items = append(items, tools.TodoItem{Content: fmt.Sprintf("step %d", i+1), Status: "pending"})
		}
		out := render(&tools.TodoState{Total: len(items), Items: items})
		for i := 1; i <= 12; i++ {
			if !strings.Contains(out, fmt.Sprintf("[ ] step %d", i)) {
				t.Errorf("step %d missing from the full list:\n%s", i, out)
			}
		}
		if strings.Contains(out, "more") {
			t.Errorf("a 12-step plan must not truncate:\n%s", out)
		}
	})

	t.Run("collapses only at the extreme", func(t *testing.T) {
		t.Parallel()
		items := make([]tools.TodoItem, 0, maxTodoItems+5)
		for i := 0; i < maxTodoItems+5; i++ {
			items = append(items, tools.TodoItem{Content: fmt.Sprintf("step %d", i+1), Status: "pending"})
		}
		out := render(&tools.TodoState{Total: len(items), Items: items})
		for i := 1; i <= maxTodoItems; i++ {
			if !strings.Contains(out, fmt.Sprintf("[ ] step %d", i)) {
				t.Errorf("step %d missing:\n%s", i, out)
			}
		}
		if strings.Contains(out, fmt.Sprintf("[ ] step %d", maxTodoItems+1)) {
			t.Errorf("step past the extreme guard leaked:\n%s", out)
		}
		if !strings.Contains(out, "… +5 more — /todos") {
			t.Errorf("missing the single limit line:\n%s", out)
		}
	})
}

// TestSidebarTodoSectionKeepsMCP pins the placement invariant: the plan
// section sits below MCP Tools and grows downward, and the sidebar frame
// clips from the bottom — so a long plan can never evict the sections above
// it (Model … MCP Tools), only its own tail and the sections below.
func TestSidebarTodoSectionKeepsMCP(t *testing.T) {
	t.Parallel()

	items := make([]tools.TodoItem, 0, 20)
	for i := 0; i < 20; i++ {
		items = append(items, tools.TodoItem{Content: fmt.Sprintf("step %d", i+1), Status: "pending"})
	}
	out := RenderSidebar(SidebarRenderInput{
		Width:  40,
		Height: 24, // small terminal: the 21-row plan overflows the panel
		MCPTools: []extension.MCPToolEntry{
			{Server: "filesystem", Tool: "read_file"},
		},
		TodoState: &tools.TodoState{Total: len(items), Items: items},
	})
	if !strings.Contains(out, "MCP Tools [1]") {
		t.Error("MCP Tools section evicted by the long plan:\n" + out)
	}
	if !strings.Contains(out, "Plan — 0/20") {
		t.Error("plan section missing from the sidebar:\n" + out)
	}
	// Position: plan below MCP tools.
	if strings.Index(out, "Plan — 0/20") < strings.Index(out, "MCP Tools [1]") {
		t.Error("expected the plan section below MCP Tools")
	}
}

// TestSidebarTodoSectionWidth pins that wrapped plan rows stay inside the
// sidebar column — a long item must never push the panel wider or clip a
// neighbour.
func TestSidebarTodoSectionWidth(t *testing.T) {
	t.Parallel()
	const w = 30
	long := strings.Repeat("supercalifragilistic ", 8) // wraps and hard-cuts
	out := RenderSidebar(SidebarRenderInput{
		Width:  w,
		Height: 40,
		TodoState: &tools.TodoState{
			Total: 1,
			Items: []tools.TodoItem{{Content: long, Status: "pending"}},
		},
	})
	for i, line := range strings.Split(out, "\n") {
		if gw := runewidth.StringWidth(ansi.Strip(line)); gw > w {
			t.Errorf("line %d is %d cells wide (max %d): %q", i, gw, w, line)
		}
	}
}

// TestTodosSlashCommandOpensPopup pins the special case in handleSlashCommand:
// /todos takes no arguments, so trailing whitespace or stray text must not
// block the popup.
func TestTodosSlashCommandOpensPopup(t *testing.T) {
	t.Parallel()
	m := newTestModel(t)
	m.todoState = &tools.TodoState{
		Total: 1,
		Items: []tools.TodoItem{{Content: "step", Status: "pending"}},
	}
	for _, input := range []string{"/todos", "/todos ", "/todos  ", "/todos junk"} {
		m.searchPopup = nil
		m.handleSlashCommand(input)
		if m.searchPopup == nil {
			t.Errorf("handleSlashCommand(%q) did not open the todos popup", input)
			continue
		}
		if m.searchPopup.mode != searchModeTodos {
			t.Errorf("handleSlashCommand(%q) opened mode %q", input, m.searchPopup.mode)
		}
	}
}

// TestTodosCommandDiscoverable pins the autocomplete fix: /todos is listed
// with a description and completes by prefix.
func TestTodosCommandDiscoverable(t *testing.T) {
	t.Parallel()

	listed := false
	for _, cmd := range slashCommands {
		if cmd == "/todos" {
			listed = true
			break
		}
	}
	if !listed {
		t.Error("/todos missing from slashCommands — users cannot discover it")
	}
	if desc := slashCommandDesc("/todos"); desc == "" {
		t.Error("/todos has no autocomplete description")
	}

	res := Complete("/to", nil, "")
	found := false
	for _, c := range res.Candidates {
		if c.Text == "/todos" {
			found = true
		}
	}
	if !found {
		t.Errorf("Complete(/to) missing /todos: %+v", res.Candidates)
	}
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
