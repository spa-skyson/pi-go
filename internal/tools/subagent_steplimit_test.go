package tools

import (
	"strings"
	"testing"

	"github.com/spa-skyson/pi-rate/internal/subagent"
)

// stepLimitStreamFixture is the merged-stream shape a step-budget death
// produces since the child emits the error event (issue #51): the partial
// report, the failing function response, the child's own error event, and —
// when the fallback chain is active — the orchestrator's terminal
// "subagent … stopped" wrap. run_done says "completed" because the child
// exited 0; the result status must stay failed anyway.
var stepLimitStreamFixture = []subagent.Event{
	{Type: "text_delta", Content: "partial "},
	{Type: "tool_result", Content: `{"error":"steps limit 150 reached"}`},
	{Type: "error", Error: "steps limit 150 reached"},
	{Type: "error", Error: "subagent ag-1 stopped: steps limit 150 reached"},
	{Type: "run_done", Status: "completed"},
}

// TestForwardSingleModeEvents_StepLimitMarksResultFailed is the contract from
// issue #51: a spawn attempt that ended on its step budget must surface as
// status != "completed" with the limit named in the error — the fields the
// subagent tool copies into results[], where the orchestrator reads them.
func TestForwardSingleModeEvents_StepLimitMarksResultFailed(t *testing.T) {
	text, status, errMsg, _ := forwardSingleModeEvents(
		fallbackEventChan(stepLimitStreamFixture...),
		func(SubagentEvent) {}, "ag-1", "pipe-1",
	)

	if status != "failed" {
		t.Errorf("status = %q, want failed — a step-budget death is not a completed run", status)
	}
	if !strings.Contains(errMsg, "steps limit") {
		t.Errorf("errMsg = %q, want it to name the steps limit", errMsg)
	}
	if text != "partial " {
		t.Errorf("text = %q, want the partial report to survive alongside the error", text)
	}
}

// TestForwardSingleModeEvents_PlainSuccessStaysCompleted pins the other side:
// a run that produced no error event keeps status "completed" and an empty
// error, so an ordinary result never gains a failure mark.
func TestForwardSingleModeEvents_PlainSuccessStaysCompleted(t *testing.T) {
	stream := fallbackEventChan(
		subagent.Event{Type: "text_delta", Content: "all done"},
		subagent.Event{Type: "message_end"},
		subagent.Event{Type: "run_done", Status: "completed"},
	)
	text, status, errMsg, _ := forwardSingleModeEvents(stream, func(SubagentEvent) {}, "ag-1", "pipe-1")

	if status != "completed" {
		t.Errorf("status = %q, want completed", status)
	}
	if errMsg != "" {
		t.Errorf("errMsg = %q, want empty", errMsg)
	}
	if text != "all done" {
		t.Errorf("text = %q, want the full report", text)
	}
}
