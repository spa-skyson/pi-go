package agent

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/session"
)

// actionsCtx is a minimal adkagent.Context whose only live methods are
// Actions and InvocationID: exactly what the step-limit callback touches.
// Everything else panics via StrictContextMock, so an unexpected call fails
// loudly.
type actionsCtx struct {
	agent.StrictContextMock
	actions *session.EventActions
	invID   string
}

func newActionsCtx(invocationID string) *actionsCtx {
	return &actionsCtx{
		StrictContextMock: agent.NewStrictContextMock(context.Background()),
		actions:           &session.EventActions{},
		invID:             invocationID,
	}
}

func (c *actionsCtx) Actions() *session.EventActions { return c.actions }

func (c *actionsCtx) InvocationID() string { return c.invID }

// TestStepLimitCallback pins the subagent iteration budget: the first maxSteps
// calls pass through untouched, the next one fails with the limit named AND
// marks the event final so ADK ends the turn instead of looping the error back
// to the model, and zero disables the guard. The callback must never mutate
// the result it is handed — it is a control-flow guard, not a transform.
func TestStepLimitCallback(t *testing.T) {
	t.Run("zero disables the limit", func(t *testing.T) {
		cb := NewStepLimitCallback(0)
		ctx := newActionsCtx("e-run")
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
		ctx := newActionsCtx("e-run")
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

	t.Run("budget resets at each new run", func(t *testing.T) {
		// Two sequential runs of limit tools each must both fit: the second
		// run restarts from zero instead of inheriting the first run's count
		// (the session-lifetime counter cut a TUI turn off mid-work). The
		// (limit+1)-th call inside one run still trips the budget.
		const limit = 3
		cb := NewStepLimitCallback(limit)

		first := newActionsCtx("e-run-1")
		for i := range limit {
			if _, err := cb(first, nil, nil, nil, nil); err != nil {
				t.Fatalf("run 1, call %d: unexpected error %v", i+1, err)
			}
		}
		if first.actions.SkipSummarization {
			t.Fatal("run 1: SkipSummarization set while within the budget")
		}

		second := newActionsCtx("e-run-2")
		for i := range limit {
			if _, err := cb(second, nil, nil, nil, nil); err != nil {
				t.Fatalf("run 2, call %d: budget did not reset — %v", i+1, err)
			}
		}
		if second.actions.SkipSummarization {
			t.Fatal("run 2: SkipSummarization set while within the budget")
		}

		_, err := cb(second, nil, nil, nil, nil)
		if err == nil {
			t.Fatal("run 2, 4th call: expected steps limit error, got nil")
		}
		if !strings.Contains(err.Error(), "steps limit 3 reached") {
			t.Fatalf("error = %v, want containing %q", err, "steps limit 3 reached")
		}
		if !second.actions.SkipSummarization {
			t.Fatal("over-budget call did not set SkipSummarization")
		}
		if first.actions.SkipSummarization {
			t.Fatal("run 1 events must stay untouched by run 2's over-budget call")
		}
	})

	t.Run("parallel calls of one run share one budget", func(t *testing.T) {
		// A parallel tool batch of one turn shares one invocation id, so a
		// batch of N tools must eat N steps of that turn's single budget —
		// not N per tool and not free. Deterministic: limit+4 concurrent
		// calls against one budget fail exactly 4 of them. Each goroutine
		// carries its own context (its own event actions) — only the
		// invocation id and the callback's counter are shared, which is what
		// a real parallel batch shares too.
		const limit = 8
		cb := NewStepLimitCallback(limit)
		var over atomic.Int64
		var wg sync.WaitGroup
		for range limit + 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ctx := newActionsCtx("e-run")
				if _, err := cb(ctx, nil, nil, nil, nil); err != nil {
					over.Add(1)
				}
			}()
		}
		wg.Wait()
		if got := over.Load(); got != 4 {
			t.Fatalf("over-budget calls = %d, want exactly 4 (limit+4 concurrent calls, one shared budget)", got)
		}
	})

	t.Run("exceeding call marks the event final", func(t *testing.T) {
		// The termination mechanism: SkipSummarization on the event actions
		// makes the function-response event ADK's final response, which is
		// what ends the turn. Without it ADK would feed the error back to the
		// model and the loop would spin forever.
		cb := NewStepLimitCallback(2)
		ctx := newActionsCtx("e-run")
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
