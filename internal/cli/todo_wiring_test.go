package cli

import (
	"testing"

	adktool "google.golang.org/adk/v2/tool"
)

// todoToolNames reports whether the todo tools are among ts.
func todoToolNames(ts []adktool.Tool) (write, read bool) {
	for _, x := range ts {
		switch x.Name() {
		case "todo_write":
			write = true
		case "todo_read":
			read = true
		}
	}
	return write, read
}

// TestDeferredInitCoreTools_TodoWiring pins the interactive wiring: a session
// ID registers the todo tools (nil todoCh included — the notifier is optional),
// and no session ID registers none.
func TestDeferredInitCoreTools_TodoWiring(t *testing.T) {
	t.Run("registered with a session ID", func(t *testing.T) {
		root := t.TempDir()
		res := &initResources{}
		t.Cleanup(res.cleanup)

		core, err := deferredInitCoreTools(root, root, "sess-cli", nil, res)
		if err != nil {
			t.Fatalf("deferredInitCoreTools: %v", err)
		}
		write, read := todoToolNames(core)
		if !write || !read {
			t.Errorf("todo tools missing (write=%v read=%v) with a session ID", write, read)
		}
		if res.sandbox == nil || res.bashSup == nil {
			t.Error("sandbox/supervisor not recorded on res for cleanup")
		}
	})

	t.Run("absent without a session ID", func(t *testing.T) {
		root := t.TempDir()
		res := &initResources{}
		t.Cleanup(res.cleanup)

		core, err := deferredInitCoreTools(root, root, "", nil, res)
		if err != nil {
			t.Fatalf("deferredInitCoreTools: %v", err)
		}
		if write, read := todoToolNames(core); write || read {
			t.Errorf("todo tools registered without a session ID (write=%v read=%v)", write, read)
		}
	})
}
