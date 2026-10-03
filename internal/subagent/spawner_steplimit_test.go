package subagent

import (
	"context"
	"strings"
	"testing"
)

// TestSpawn_StepLimitErrorLineSurfacesAsErrorEvent pins the parent half of
// issue #51's pipe: a child that announces its step-budget death with an
// explicit `error` JSONL line (emitted by runJSONTurn since the fix) must
// reach the caller as an error Event — not vanish behind the accumulated
// text. The child exits 0 by protocol: Wait reports no process error, so the
// fallback chain classifies the run as attemptStopped (final, no re-spawn)
// rather than as a crash to retry.
func TestSpawn_StepLimitErrorLineSurfacesAsErrorEvent(t *testing.T) {
	t.Setenv("PI_SUBAGENT_TIMEOUT_MS", "20000")
	s := &Spawner{PiBinary: fakePi(t, `
printf '{"type":"text_delta","delta":"partial "}\n'
printf '{"type":"tool_result","content":"report so far"}\n'
printf '{"type":"error","error":"steps limit 150 reached"}\n'
`)}

	proc, err := s.Spawn(context.Background(), SpawnOpts{Prompt: "go"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	var errEvents []Event
	for ev := range proc.Events() {
		if ev.Type == "error" {
			errEvents = append(errEvents, ev)
		}
	}
	result, waitErr := proc.Wait()

	if waitErr != nil {
		t.Errorf("Wait() error = %v, want nil: a step-budget death exits 0, a crash would be re-spawned", waitErr)
	}
	if result != "partial " {
		t.Errorf("result = %q, want the partial text", result)
	}
	if len(errEvents) != 1 {
		t.Fatalf("error events = %d, want 1", len(errEvents))
	}
	if !strings.Contains(errEvents[0].Error, "steps limit") {
		t.Errorf("error event text = %q, want it to name the steps limit", errEvents[0].Error)
	}
}
