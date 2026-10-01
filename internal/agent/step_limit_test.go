package agent

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/session"
)

// actionsCtx is a minimal adkagent.Context whose only live method is
// Actions: exactly what the step-limit callback may touch. Everything else
// panics via StrictContextMock, so an unexpected call fails loudly.
type actionsCtx struct {
	agent.StrictContextMock
	actions *session.EventActions
}

func newActionsCtx() *actionsCtx {
	return &actionsCtx{
		StrictContextMock: agent.NewStrictContextMock(context.Background()),
		actions:           &session.EventActions{},
	}
}

func (c *actionsCtx) Actions() *session.EventActions { return c.actions }

// TestStepLimitCallback pins the subagent iteration budget: the first maxSteps
// calls pass through untouched, the next one fails with the limit named AND
// marks the event final so ADK ends the turn instead of looping the error back
// to the model, and zero disables the guard. The callback must never mutate
// the result it is handed — it is a control-flow guard, not a transform.
func TestStepLimitCallback(t *testing.T) {
	t.Run("zero disables the limit", func(t *testing.T) {
		cb := NewStepLimitCallback(0)
		ctx := newActionsCtx()
		for i := range 10 {
			if _, err := cb(ctx, nil, nil, nil, nil); err != nil {
				t.Fatalf("call %d: unexpected error %v", i+1, err)
			}
			if ctx.actions.SkipSummarization {
				t.Fatalf("call %d: SkipSummarization set under a disabled limit", i+1)
			}
		}
	})

	t.Run("within limit passes, exceeding fails", func(t *testing.T) {
		cb := NewStepLimitCallback(3)
		ctx := newActionsCtx()
		for i := range 3 {
			if _, err := cb(ctx, nil, nil, nil, nil); err != nil {
				t.Fatalf("call %d: unexpected error %v", i+1, err)
			}
		}
		_, err := cb(ctx, nil, nil, nil, nil)
		if err == nil {
			t.Fatal("4th call: expected steps limit error, got nil")
		}
		if !strings.Contains(err.Error(), "steps limit 3 reached") {
			t.Fatalf("error = %v, want containing %q", err, "steps limit 3 reached")
		}
	})

	t.Run("exceeding call marks the event final", func(t *testing.T) {
		// The termination mechanism: SkipSummarization on the event actions
		// makes the function-response event ADK's final response, which is
		// what ends the turn. Without it ADK would feed the error back to the
		// model and the loop would spin forever.
		cb := NewStepLimitCallback(2)
		ctx := newActionsCtx()
		for range 2 {
			_, _ = cb(ctx, nil, nil, nil, nil)
		}
		if ctx.actions.SkipSummarization {
			t.Fatal("SkipSummarization set while still under the limit")
		}
		_, _ = cb(ctx, nil, nil, nil, nil)
		if !ctx.actions.SkipSummarization {
			t.Fatal("over-budget call did not set SkipSummarization; the turn would not terminate")
		}
	})

	t.Run("result is never mutated or replaced", func(t *testing.T) {
		cb := NewStepLimitCallback(1)
		result := map[string]any{"stdout": "keep me", "truncated": true}
		got, err := cb(nil, nil, nil, result, nil)
		if err != nil {
			t.Fatalf("unexpected error %v", err)
		}
		if got != nil {
			t.Fatalf("callback returned a replacement result %v, want nil", got)
		}
		if len(result) != 2 || result["stdout"] != "keep me" || result["truncated"] != true {
			t.Fatalf("result was mutated: %v", result)
		}
	})
}
