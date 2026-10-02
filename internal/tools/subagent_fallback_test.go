package tools

import (
	"testing"

	"github.com/spa-skyson/pi-rate/internal/subagent"
)

// fallbackEventChan builds a buffered, closed event channel from the given
// events — the shape the orchestrator's merged fallback stream arrives in.
func fallbackEventChan(evs ...subagent.Event) <-chan subagent.Event {
	ch := make(chan subagent.Event, len(evs))
	for _, ev := range evs {
		ch <- ev
	}
	close(ch)
	return ch
}

// fallbackStreamFixture is one attempt failing on a dead provider, the
// restart notice, and the retry succeeding — the real event sequence of
// SpawnWithInputFallback between two attempts.
var fallbackStreamFixture = []subagent.Event{
	{Type: "text_delta", Content: "partial "},
	{Type: "error", Error: "402 Payment Required"},
	{Type: subagent.EventFallback, Content: "model primary-model failed (402 Payment Required); restarting on fb-one"},
	{Type: "text_delta", Content: "done"},
	{Type: "message_end"},
	{Type: "run_done", Status: "completed"},
}

// TestForwardSingleModeEventsFallbackRecovers pins the single-mode forwarder
// on a merged fallback stream: every event reaches the TUI in order, and the
// reported status is the recovered one — an earlier attempt's provider error
// must not fail a result the retry completed.
func TestForwardSingleModeEventsFallbackRecovers(t *testing.T) {
	var seen []SubagentEvent
	text, status, errMsg, _ := forwardSingleModeEvents(
		fallbackEventChan(fallbackStreamFixture...),
		func(ev SubagentEvent) { seen = append(seen, ev) },
		"agent-1", "pipe-1",
	)

	if text != "partial done" {
		t.Errorf("text = %q, want both attempts' deltas", text)
	}
	if status != "completed" {
		t.Errorf("status = %q, want completed (the restart recovered)", status)
	}
	if errMsg != "" {
		t.Errorf("errMsg = %q, want empty: the recovered failure is not the outcome", errMsg)
	}
	if len(seen) != len(fallbackStreamFixture) {
		t.Fatalf("forwarded %d events, want %d", len(seen), len(fallbackStreamFixture))
	}
	for i, ev := range seen {
		if ev.Kind != fallbackStreamFixture[i].Type {
			t.Errorf("event %d kind = %q, want %q", i, ev.Kind, fallbackStreamFixture[i].Type)
		}
	}
}

// TestForwardSubagentEventsFallbackRecovers pins the same rule for the
// parallel/chain forwarder: the notice resets the failure the previous
// attempt left behind.
func TestForwardSubagentEventsFallbackRecovers(t *testing.T) {
	meta := subagentStepMeta{PipelineID: "pipe-1", Mode: "chain", Step: 2, Total: 3}
	var seen []SubagentEvent
	_, status, errMsg, _ := forwardSubagentEvents(
		fallbackEventChan(fallbackStreamFixture...),
		func(ev SubagentEvent) { seen = append(seen, ev) },
		"agent-1", meta,
	)

	if status != "completed" || errMsg != "" {
		t.Errorf("status/err = %q/%q, want completed/empty after the restart", status, errMsg)
	}
	if len(seen) != len(fallbackStreamFixture) {
		t.Fatalf("forwarded %d events, want %d", len(seen), len(fallbackStreamFixture))
	}
}
