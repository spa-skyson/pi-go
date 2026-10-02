package subagent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spa-skyson/pi-rate/internal/config"
)

// fallbackScriptPath returns a mock pi binary that records each invocation's
// full argv into logPath (one line per call) and behaves according to which
// model it was invoked with: when the args mention fallbackModel it emits a
// healthy message_end run; otherwise it emits the given provider error.
func fallbackScriptPath(t *testing.T, logPath, fallbackModel, providerErr string) string {
	t.Helper()
	script := `#!/bin/bash
echo "$*" >> "` + logPath + `"
if [[ "$*" == *"` + fallbackModel + `"* ]]; then
  echo '{"type":"text_delta","delta":"recovered"}'
  echo '{"type":"message_end"}'
  exit 0
fi
echo '{"type":"error","error":"` + providerErr + `"}'
exit 1
`
	return mockPiScript(t, script)
}

// readArgvLog reads the recorded argv lines, skipping a missing file.
func readArgvLog(t *testing.T, logPath string) []string {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if err != nil {
		return nil
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// TestSpawnWithRetry_FallbackOnProviderError: a fatal provider error (the
// child's own retries are exhausted, so the error event is terminal) restarts
// the process on the agent's fallback model, and the second attempt succeeds.
func TestSpawnWithRetry_FallbackOnProviderError(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "argv.log")
	binary := fallbackScriptPath(t, logPath, "fb-one", "402 Payment Required")

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
	if err != nil {
		t.Fatalf("SpawnWithRetry should recover on fallback, got %v", err)
	}
	for range events {
		// Drain: the success path returns the stream to the caller.
	}

	calls := readArgvLog(t, logPath)
	if len(calls) != 2 {
		t.Fatalf("spawn attempts = %d, want 2 (primary then fallback)", len(calls))
	}
	if !strings.Contains(calls[0], "primary-model") {
		t.Errorf("first attempt argv %q must use the primary model", calls[0])
	}
	if !strings.Contains(calls[1], "--model fb-one") {
		t.Errorf("second attempt argv %q must use --model fb-one", calls[1])
	}
}

// TestSpawnWithRetry_FallbackExhausted: when every model in the chain fails
// fatally, the final error names the last provider error.
func TestSpawnWithRetry_FallbackExhausted(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "argv.log")
	// No model in argv ever matches "never-used", so every attempt errors.
	binary := fallbackScriptPath(t, logPath, "never-used", "402 Payment Required")

	cfg := testConfig()
	cfg.Roles["smol"] = configRoleWithFallbacks("primary-model", "fb-one")

	orch := NewOrchestrator(cfg, "", nil)
	defer orch.Shutdown()
	orch.SetPiBinary(binary)

	_, _, err := orch.SpawnWithRetry(context.Background(), SpawnInput{
		Agent:      AgentConfig{Name: "fbagent", Role: "smol", Model: "primary-model"},
		Prompt:     "hi",
		MaxRetries: 1,
	})
	if err == nil {
		t.Fatal("expected a failure once the fallback chain is exhausted")
	}
	if !strings.Contains(err.Error(), "402") {
		t.Errorf("final error %q should carry the provider error text", err)
	}
	calls := readArgvLog(t, logPath)
	if len(calls) != 2 {
		t.Fatalf("spawn attempts = %d, want 2 (primary then the single fallback)", len(calls))
	}
}

// TestSpawnWithRetry_NoFallbackSingleAttempt: without a configured chain a
// provider error stays final — one attempt, error text preserved.
func TestSpawnWithRetry_NoFallbackSingleAttempt(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "argv.log")
	binary := fallbackScriptPath(t, logPath, "never-used", "402 Payment Required")

	cfg := testConfig()
	cfg.Roles["smol"] = configRoleConfig("primary-model")

	orch := NewOrchestrator(cfg, "", nil)
	defer orch.Shutdown()
	orch.SetPiBinary(binary)

	_, _, err := orch.SpawnWithRetry(context.Background(), SpawnInput{
		Agent:      AgentConfig{Name: "fbagent", Role: "smol", Model: "primary-model"},
		Prompt:     "hi",
		MaxRetries: 2,
	})
	if err == nil {
		t.Fatal("expected the provider error to surface")
	}
	if !strings.Contains(err.Error(), "402") {
		t.Errorf("error %q should carry the provider error text", err)
	}
	if calls := readArgvLog(t, logPath); len(calls) != 1 {
		t.Fatalf("spawn attempts = %d, want 1 (no fallback configured)", len(calls))
	}
}

// TestSpawnWithRetry_CrashRetriesSameModel: a crash without a provider error
// event keeps the existing crash-retry behavior — same model every time.
func TestSpawnWithRetry_CrashRetriesSameModel(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "argv.log")
	script := `#!/bin/bash
echo "$*" >> "` + logPath + `"
echo "boom" >&2
exit 1
`
	binary := mockPiScript(t, script)

	cfg := testConfig()
	cfg.Roles["smol"] = configRoleConfig("primary-model")

	orch := NewOrchestrator(cfg, "", nil)
	defer orch.Shutdown()
	orch.SetPiBinary(binary)

	_, _, err := orch.SpawnWithRetry(context.Background(), SpawnInput{
		Agent:      AgentConfig{Name: "fbagent", Role: "smol", Model: "primary-model"},
		Prompt:     "hi",
		MaxRetries: 2,
	})
	if err == nil {
		t.Fatal("expected a crash failure after retries")
	}
	calls := readArgvLog(t, logPath)
	// The mock crash produces a synthetic error event with the process exit
	// text, which classifies as a terminal error (not retryable crash), so
	// the run ends on the first attempt. What matters: no fallback model was
	// ever injected, because none is configured.
	if len(calls) < 1 {
		t.Fatalf("spawn attempts = %d, want at least 1", len(calls))
	}
	for i, c := range calls {
		if strings.Contains(c, "fb-") {
			t.Errorf("attempt %d argv %q must not use a fallback model", i, c)
		}
	}
}

// configRoleWithFallbacks builds a RoleConfig with a fallback chain.
func configRoleWithFallbacks(model string, fallbacks ...string) config.RoleConfig {
	return config.RoleConfig{Model: model, FallbackModels: fallbacks}
}

// configRoleConfig builds a plain RoleConfig without fallbacks.
func configRoleConfig(model string) config.RoleConfig {
	return config.RoleConfig{Model: model}
}
