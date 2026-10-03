package subagent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// TestSpawnWithRetry_StepLimitStopIsFinal pins the retry half of issue #51: a
// child that announces its step-budget death with an error event and exits 0
// is classified as attemptStopped — the chain ends there. It must NOT be
// re-spawned on the same model (the crash path) or on a fallback model (the
// provider-error path): the budget stopped the task on purpose, and re-running
// it would spend hours redoing work only to hit the same wall.
func TestSpawnWithRetry_StepLimitStopIsFinal(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "argv.log")
	binary := mockPiScript(t, `#!/bin/bash
echo "$*" >> "`+logPath+`"
echo '{"type":"text_delta","delta":"partial "}'
echo '{"type":"error","error":"steps limit 150 reached"}'
exit 0
`)

	cfg := testConfig()
	cfg.Roles["smol"] = configRoleWithFallbacks("primary-model", "fb-one")

	orch := NewOrchestrator(cfg, "", nil)
	defer orch.Shutdown()
	orch.SetPiBinary(binary)

	events, _, err := orch.SpawnWithRetry(context.Background(), SpawnInput{
		Agent:      AgentConfig{Name: "fbagent", Role: "smol", Model: "primary-model"},
		Prompt:     "hi",
		MaxRetries: 1,
	})
	if err == nil {
		for range events { // a success here means the stop was misclassified
		}
		t.Fatal("SpawnWithRetry returned no error — a step-budget stop must be terminal")
	}
	if !strings.Contains(err.Error(), "steps limit") {
		t.Errorf("err = %v, want it to name the steps limit", err)
	}

	calls := readArgvLog(t, logPath)
	if len(calls) != 1 {
		t.Fatalf("spawn attempts = %d, want exactly 1 — the child must not be re-spawned: %v", len(calls), calls)
	}
}
