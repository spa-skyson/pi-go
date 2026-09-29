package agent

import (
	"fmt"
	"sync/atomic"

	adkagent "google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
)

// NewStepLimitCallback returns an AfterToolCallback that aborts the agent
// loop once more than maxSteps tool calls have executed. The (maxSteps+1)-th
// call runs; its after-callback then fails the invocation with
// "steps limit %d reached", which interrupts ADK's loop the same way any
// callback error does.
//
// Zero or negative maxSteps disables the limit. The callback never touches
// the result: it is a control-flow guard, not a transform, so it returns
// (nil, nil) while under budget and hands ADK the abort via the error only.
func NewStepLimitCallback(maxSteps int) AfterToolCallback {
	var calls atomic.Int64
	return func(_ adkagent.Context, _ tool.Tool, _, _ map[string]any, _ error) (map[string]any, error) {
		if maxSteps <= 0 {
			return nil, nil
		}
		if calls.Add(1) > int64(maxSteps) {
			return nil, fmt.Errorf("steps limit %d reached", maxSteps)
		}
		return nil, nil
	}
}
