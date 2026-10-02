package agent

import (
	"fmt"
	"sync"

	adkagent "google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
)

// NewStepLimitCallback returns an AfterToolCallback that caps a run at
// maxSteps tool calls — max tool-call iterations per run, as the --steps flag
// documents. The count is per run, not per session: the callback tracks the
// ADK invocation id and restarts from zero whenever it changes, so every user
// turn (one Run/RunStreaming call) gets a fresh budget even when the callback
// instance is built once and kept alive for a whole interactive session.
// A resumed run (human-in-the-loop) reuses the paused run's invocation id and
// continues its budget, so pausing for a confirmation cannot bypass the limit.
//
// The (maxSteps+1)-th call still runs — the callback is after-tool — but that
// call's callback then sets SkipSummarization on the event actions and fails
// with "steps limit %d reached". SkipSummarization is the ADK-sanctioned way
// to stop the loop from inside a tool call (RequestConfirmation sets the same
// flag): the function-response event carrying it reports IsFinalResponse()
// true, so ADK's Flow.Run ends the turn after yielding that event and no
// further model round-trip happens. Without the flag the error would only
// travel back to the model as {"error": ...} function-response text — ADK
// turns every tool-callback error into a result, never into a run error — so
// the loop kept spinning, every later call failing with the same message
// while tokens burned.
//
// Zero or negative maxSteps disables the limit. Under budget the callback
// never touches the result: it is a control-flow guard, not a transform.
func NewStepLimitCallback(maxSteps int) AfterToolCallback {
	// calls and runID change together, so a plain mutex guards them rather
	// than two independent atomics: a reset paired with the wrong increment
	// would silently widen or narrow the budget.
	var mu sync.Mutex
	var runID string
	var calls int
	return func(ctx adkagent.Context, _ tool.Tool, _, _ map[string]any, _ error) (map[string]any, error) {
		if maxSteps <= 0 {
			return nil, nil
		}
		mu.Lock()
		// A new invocation id is a new user turn: the budget restarts. Nil
		// ctx (test doubles) skips the detection and keeps counting against
		// the current run, exactly as before the reset existed.
		if ctx != nil {
			if id := ctx.InvocationID(); id != runID {
				runID = id
				calls = 0
			}
		}
		calls++
		over := calls > maxSteps
		mu.Unlock()
		if !over {
			return nil, nil
		}
		// Mark the function-response event as the turn's final response, so
		// ADK's loop stops here instead of feeding the error back to a model
		// that will keep calling tools. Nil-safe: not every context carries
		// event actions (test doubles, non-tool callback wrappers).
		if ctx != nil {
			if actions := ctx.Actions(); actions != nil {
				actions.SkipSummarization = true
			}
		}
		return nil, fmt.Errorf("steps limit %d reached", maxSteps)
	}
}
