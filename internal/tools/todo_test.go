package tools

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/adk/v2/tool"

	"github.com/dimetron/pi-go/internal/testenv"
)

func TestTodoSaveLoadRoundTrip(t *testing.T) {
	testenv.SetHome(t, t.TempDir())

	items := []TodoItem{
		{Content: "wire the channel", Status: "completed"},
		{Content: "write the tests", Status: "in_progress"},
		{Content: "run the suite", Status: "pending"},
	}
	if err := saveTodos("sess-rt", items); err != nil {
		t.Fatalf("saveTodos: %v", err)
	}

	got, err := loadTodos("sess-rt")
	if err != nil {
		t.Fatalf("loadTodos: %v", err)
	}
	if len(got) != len(items) {
		t.Fatalf("loaded %d items, want %d", len(got), len(items))
	}
	for i, it := range items {
		if got[i] != it {
			t.Errorf("item %d = %+v, want %+v", i, got[i], it)
		}
	}

	// A session with no file yet reads as empty, not as an error — that is
	// what makes todo_read useful before the first todo_write.
	empty, err := loadTodos("sess-without-file")
	if err != nil {
		t.Fatalf("loadTodos(missing): %v", err)
	}
	if empty != nil {
		t.Errorf("loadTodos(missing) = %+v, want nil", empty)
	}
}

func TestTodoSaveLeavesNoTempFiles(t *testing.T) {
	testenv.SetHome(t, t.TempDir())

	if err := saveTodos("sess-atomic", []TodoItem{{Content: "one", Status: "pending"}}); err != nil {
		t.Fatalf("saveTodos: %v", err)
	}
	leftovers, err := filepath.Glob(filepath.Join(todoSessionDir("sess-atomic"), "todos.*.tmp"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(leftovers) != 0 {
		t.Errorf("temp files left behind after rename: %v", leftovers)
	}
}

func TestValidateTodos(t *testing.T) {
	tests := []struct {
		name    string
		items   []TodoItem
		wantErr string // empty = valid
	}{
		{"valid", []TodoItem{{Content: "a", Status: "pending"}}, ""},
		{"empty content", []TodoItem{{Content: "  ", Status: "pending"}}, "content must not be empty"},
		{"bad status", []TodoItem{{Content: "a", Status: "done"}}, `invalid status "done"`},
		{"error names the item", []TodoItem{{Content: "ok", Status: "pending"}, {Content: "b", Status: "nope"}}, "item 2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateTodos(tt.items)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateTodos() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateTodos() = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestComputeTodoState(t *testing.T) {
	st := computeTodoState([]TodoItem{
		{Content: "done one", Status: "completed"},
		{Content: "current", Status: "in_progress"},
		{Content: "also current", Status: "in_progress"},
		{Content: "later", Status: "pending"},
	})
	if st.Total != 4 || st.Done != 1 {
		t.Errorf("Total/Done = %d/%d, want 4/1", st.Total, st.Done)
	}
	// The first in_progress item wins: the sidebar shows exactly one.
	if st.InProgress != "current" {
		t.Errorf("InProgress = %q, want %q", st.InProgress, "current")
	}
	if len(st.Items) != 4 {
		t.Errorf("Items = %d entries, want the full list", len(st.Items))
	}
}

// TestTodoWriteToolNotifier runs todo_write end to end and checks the three
// things the TUI depends on: the aggregated state reaches the notifier, the
// output carries the summary, and the list is persisted for todo_read.
func TestTodoWriteToolNotifier(t *testing.T) {
	testenv.SetHome(t, t.TempDir())

	var notified TodoState
	w, err := newTodoWriteTool("sess-notify", func(s TodoState) { notified = s })
	if err != nil {
		t.Fatalf("newTodoWriteTool: %v", err)
	}

	out := runTool(t, w, map[string]any{
		"todos": []any{
			map[string]any{"content": "first", "status": "completed"},
			map[string]any{"content": "second", "status": "in_progress"},
		},
	})
	if out["total"] != float64(2) || out["done"] != float64(1) {
		t.Errorf("output total/done = %v/%v, want 2/1", out["total"], out["done"])
	}
	if out["in_progress"] != "second" {
		t.Errorf("output in_progress = %v, want %q", out["in_progress"], "second")
	}
	if out["saved"] != true {
		t.Errorf("output saved = %v, want true", out["saved"])
	}

	if notified.Total != 2 || notified.Done != 1 || notified.InProgress != "second" || len(notified.Items) != 2 {
		t.Errorf("notified state = %+v, want total=2 done=1 in_progress=second items=2", notified)
	}

	items, err := loadTodos("sess-notify")
	if err != nil || len(items) != 2 {
		t.Fatalf("loadTodos after write: %v items, err %v", len(items), err)
	}

	// The write handler rejects an invalid list without notifying or saving.
	r, ok := w.(runnableTool)
	if !ok {
		t.Fatalf("todo_write %T does not implement Run", w)
	}
	if _, err := r.Run(mockToolCtx{Context: context.Background()}, map[string]any{
		"todos": []any{map[string]any{"content": "x", "status": "archived"}},
	}); err == nil {
		t.Error("invalid status was accepted")
	}
	if notified.Total != 2 {
		t.Errorf("notifier fired on a rejected write: %+v", notified)
	}
}

func TestCoreTools_TodoRegistration(t *testing.T) {
	sb := testSandbox(t, t.TempDir())

	hasTodo := func(ts []tool.Tool) bool {
		write, read := false, false
		for _, x := range ts {
			switch x.Name() {
			case "todo_write":
				write = true
			case "todo_read":
				read = true
			}
		}
		return write && read
	}

	base, err := CoreTools(sb)
	if err != nil {
		t.Fatalf("CoreTools: %v", err)
	}
	if hasTodo(base) {
		t.Error("todo tools registered without a session ID")
	}

	withID, err := CoreTools(sb, WithSessionID("sess-reg"), WithTodoNotifier(nil))
	if err != nil {
		t.Fatalf("CoreTools(WithSessionID): %v", err)
	}
	if !hasTodo(withID) {
		t.Error("todo tools not registered with a session ID")
	}
}
