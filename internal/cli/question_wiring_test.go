package cli

import (
	"context"
	"testing"
	"time"

	"google.golang.org/adk/v2/agent"
	adktool "google.golang.org/adk/v2/tool"

	"github.com/spa-skyson/pi-rate/internal/tools"
)

// TestDeferredInitCoreTools_QuestionWiring pins the interactive wiring: a
// question channel registers the question tool with a live notifier (the
// request reaches the channel, the dialog's answer reaches the tool), and a
// nil channel keeps the headless variant — the tool still registers, it just
// answers canceled immediately.
func TestDeferredInitCoreTools_QuestionWiring(t *testing.T) {
	t.Run("bridged with a channel", func(t *testing.T) {
		root := t.TempDir()
		res := &initResources{}
		t.Cleanup(res.cleanup)

		questionCh := make(chan tools.QuestionRequest, 1)
		core, err := deferredInitCoreTools(root, root, "sess-q", nil, questionCh, res)
		if err != nil {
			t.Fatalf("deferredInitCoreTools: %v", err)
		}

		var qt adktool.Tool
		for _, x := range core {
			if x.Name() == "question" {
				qt = x
			}
		}
		if qt == nil {
			t.Fatal("question tool not registered with a question channel")
		}

		// Invoke the tool; the request must land on the channel and the
		// dialog's answer must come back as the tool result.
		done := make(chan map[string]any, 1)
		go func() {
			r := qt.(interface {
				Run(agent.Context, any) (map[string]any, error)
			})
			out, err := r.Run(&cliToolCtx{Context: context.Background()}, map[string]any{
				"question": "Merge now?",
				"options":  []any{map[string]any{"label": "yes"}},
			})
			if err != nil {
				t.Errorf("Run error: %v", err)
			}
			done <- out
		}()

		select {
		case req := <-questionCh:
			req.Reply <- tools.QuestionAnswer{Selected: "option", Label: "yes", Index: 0}
		case <-time.After(2 * time.Second):
			t.Fatal("no request reached the question channel")
		}
		out := <-done
		if out["selected"] != "option" || out["label"] != "yes" {
			t.Errorf("output = %#v, want selected=option label=yes", out)
		}
	})

	t.Run("headless without a channel", func(t *testing.T) {
		root := t.TempDir()
		res := &initResources{}
		t.Cleanup(res.cleanup)

		core, err := deferredInitCoreTools(root, root, "sess-q", nil, nil, res)
		if err != nil {
			t.Fatalf("deferredInitCoreTools: %v", err)
		}

		var qt adktool.Tool
		for _, x := range core {
			if x.Name() == "question" {
				qt = x
			}
		}
		if qt == nil {
			t.Fatal("question tool must register even without a question channel")
		}
		r := qt.(interface {
			Run(agent.Context, any) (map[string]any, error)
		})
		out, err := r.Run(&cliToolCtx{Context: context.Background()}, map[string]any{
			"question": "Anyone there?",
			"options":  []any{map[string]any{"label": "no"}},
		})
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
		if out["selected"] != "canceled" {
			t.Errorf("output = %#v, want immediate canceled", out)
		}
	})
}
