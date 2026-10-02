package subagent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	if !strings.Contains(err.Error(), "crashed after 3 attempts") {
		t.Errorf("error %q should name the exhausted crash budget", err)
	}
	calls := readArgvLog(t, logPath)
	// The crash produces a synthetic error event with the process exit text,
	// which is not a provider wall: the run ends through the crash path, and
	// every attempt stays on the primary model.
	if len(calls) != 3 {
		t.Fatalf("spawn attempts = %d, want 3 (crash retries on the same model)", len(calls))
	}
	for i, c := range calls {
		if strings.Contains(c, "fb-") {
			t.Errorf("attempt %d argv %q must not use a fallback model", i, c)
		}
	}
}

// TestSpawnWithRetry_CrashNotSteeredToFallback pins the crash/provider split:
// a child that crashes without a provider error must run through the crash
// path even when a fallback chain is configured — the chain is for dead
// accounts, not for dead processes, and it must still be unused afterwards.
func TestSpawnWithRetry_CrashNotSteeredToFallback(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "argv.log")
	script := `#!/bin/bash
echo "$*" >> "` + logPath + `"
echo "segmentation fault" >&2
exit 139
`
	binary := mockPiScript(t, script)

	cfg := testConfig()
	cfg.Roles["smol"] = configRoleWithFallbacks("primary-model", "fb-one", "fb-two")

	orch := NewOrchestrator(cfg, "", nil)
	defer orch.Shutdown()
	orch.SetPiBinary(binary)

	_, _, err := orch.SpawnWithRetry(context.Background(), SpawnInput{
		Agent:      AgentConfig{Name: "fbagent", Role: "smol", Model: "primary-model"},
		Prompt:     "hi",
		MaxRetries: 1,
	})
	if err == nil {
		t.Fatal("expected a crash failure after retries")
	}
	calls := readArgvLog(t, logPath)
	if len(calls) != 2 {
		t.Fatalf("spawn attempts = %d, want 2 (crash retries on the primary model)", len(calls))
	}
	for i, c := range calls {
		if !strings.Contains(c, "primary-model") || strings.Contains(c, "fb-") {
			t.Errorf("attempt %d argv %q must stay on the primary model", i, c)
		}
	}
}

// TestSpawnWithRetry_TimeoutNotSteeredToFallback pins the other half of the
// split: an attempt stopped by its own time limit is final — neither a crash
// worth re-spawning nor a model problem — so the chain stays untouched and
// the error names the stop.
func TestSpawnWithRetry_TimeoutNotSteeredToFallback(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "argv.log")
	script := `#!/bin/bash
echo "$*" >> "` + logPath + `"
sleep 5
`
	binary := mockPiScript(t, script)

	cfg := testConfig()
	cfg.Roles["smol"] = configRoleWithFallbacks("primary-model", "fb-one")

	orch := NewOrchestrator(cfg, "", nil)
	defer orch.Shutdown()
	orch.SetPiBinary(binary)

	start := time.Now()
	_, _, err := orch.SpawnWithRetry(context.Background(), SpawnInput{
		Agent:      AgentConfig{Name: "sleepy", Role: "smol", Model: "primary-model", Timeout: 250},
		Prompt:     "hi",
		MaxRetries: 1,
	})
	if err == nil {
		t.Fatal("expected the timeout to surface as a failure")
	}
	if !strings.Contains(err.Error(), "stopped") {
		t.Errorf("error %q should name the stop, not a provider failure", err)
	}
	if calls := readArgvLog(t, logPath); len(calls) != 1 {
		t.Fatalf("spawn attempts = %d, want 1 (a timeout must not burn the fallback chain)", len(calls))
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("run took %v; the 250ms agent timeout did not bound it", elapsed)
	}
}

// TestSpawnWithRetry_FallbackBudgetBoundsRestart pins the chain budget: the
// fallback restart runs under the agent's declared timeout, not an unbounded
// default — the restart is killed and the run ends once the budget is spent.
func TestSpawnWithRetry_FallbackBudgetBoundsRestart(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "argv.log")
	script := `#!/bin/bash
echo "$*" >> "` + logPath + `"
if [[ "$*" == *"fb-one"* ]]; then
  sleep 10
fi
echo '{"type":"error","error":"402 Payment Required"}'
exit 1
`
	binary := mockPiScript(t, script)

	cfg := testConfig()
	cfg.Roles["smol"] = configRoleWithFallbacks("primary-model", "fb-one")

	orch := NewOrchestrator(cfg, "", nil)
	defer orch.Shutdown()
	orch.SetPiBinary(binary)

	start := time.Now()
	_, _, err := orch.SpawnWithRetry(context.Background(), SpawnInput{
		// The primary attempt fails fast; the fallback hangs. The 800ms
		// agent timeout must bound the restart instead of letting it run to
		// the 20-minute default.
		Agent:      AgentConfig{Name: "fbagent", Role: "smol", Model: "primary-model", Timeout: 800},
		Prompt:     "hi",
		MaxRetries: 1,
	})
	if err == nil {
		t.Fatal("expected the hung fallback to be killed by the chain budget")
	}
	if calls := readArgvLog(t, logPath); len(calls) != 2 {
		t.Fatalf("spawn attempts = %d, want 2 (primary, then the killed fallback)", len(calls))
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("run took %v; the 800ms chain budget did not bound the restart", elapsed)
	}
}

// TestChainBudgetMatchesSpawnTimeout pins the wiring the chain budget rests
// on: the budget is derived from the same effective timeout Spawn gives the
// child, so a restart's ceiling equals the attempt it replaces — an agent's
// frontmatter timeout must not silently become the 20-minute default on the
// second try.
func TestChainBudgetMatchesSpawnTimeout(t *testing.T) {
	agent := AgentConfig{Name: "a", Timeout: 1234}
	input := SpawnInput{Agent: agent}
	if got := effectiveSpawnTimeout(agent, input); got != 1234 {
		t.Errorf("effectiveSpawnTimeout = %d, want the agent's 1234", got)
	}
	input.Timeout = 5000
	if got := effectiveSpawnTimeout(agent, input); got != 5000 {
		t.Errorf("effectiveSpawnTimeout = %d, want the explicit 5000 override", got)
	}

	var b chainBudget
	b.start(context.Background(), 1234)
	defer b.cancel()
	deadline, ok := b.ctx.Deadline()
	if !ok {
		t.Fatal("chain budget context carries no deadline")
	}
	if d := time.Until(deadline); d <= 0 || d > 1234*time.Millisecond {
		t.Errorf("chain budget deadline in %v, want at most 1234ms", d)
	}
}

// TestChainBudgetCoversFirstAttempt pins the chain budget's total: the
// deadline is taken at the first spawn, so the first attempt's time comes out
// of the restart's share — the attempts together never exceed one
// effectiveSpawnTimeout (plus a small overhead allowance). A budget taken at
// the first restart would grant the chain the agent timeout twice over.
func TestChainBudgetCoversFirstAttempt(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "argv.log")
	// The primary attempt burns 700ms of the 1500ms budget before its
	// provider error; the fallback then hangs. The chain must cut it off
	// ~800ms into the restart, not grant the restart a fresh 1500ms.
	script := `#!/bin/bash
echo "$*" >> "` + logPath + `"
if [[ "$*" == *"fb-one"* ]]; then
  sleep 10
  echo '{"type":"error","error":"402 Payment Required"}'
  exit 1
fi
sleep 0.7
echo '{"type":"error","error":"402 Payment Required"}'
exit 1
`
	binary := mockPiScript(t, script)

	cfg := testConfig()
	cfg.Roles["smol"] = configRoleWithFallbacks("primary-model", "fb-one")

	orch := NewOrchestrator(cfg, "", nil)
	defer orch.Shutdown()
	orch.SetPiBinary(binary)

	const budget = 1500 * time.Millisecond
	start := time.Now()
	_, _, err := orch.SpawnWithRetry(context.Background(), SpawnInput{
		Agent:      AgentConfig{Name: "fbagent", Role: "smol", Model: "primary-model", Timeout: int(budget / time.Millisecond)},
		Prompt:     "hi",
		MaxRetries: 1,
	})
	if err == nil {
		t.Fatal("expected the hung fallback to be killed by the chain budget")
	}
	if calls := readArgvLog(t, logPath); len(calls) != 2 {
		t.Fatalf("spawn attempts = %d, want 2 (primary, then the killed fallback)", len(calls))
	}
	// The sum of the attempts must stay within one budget; a restart that
	// carries its own fresh budget lands near 2*budget with the 700ms first
	// attempt and cannot come in under this bound.
	if elapsed := time.Since(start); elapsed > budget+450*time.Millisecond {
		t.Errorf("chain ran %v; want first attempt + restart bounded by the %v budget plus overhead", elapsed, budget)
	}
}

// TestChainBudgetExpiryEndsTheChain pins the spent-budget edge: once the
// chain budget is gone the run ends with the timeout as the final error, on
// the attempts already made — no restart is granted a deadline that has
// already passed.
func TestChainBudgetExpiryEndsTheChain(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "argv.log")
	// The first attempt hangs and the budget kills it mid-flight: no
	// provider error is ever classified, and no fallback restart follows.
	script := `#!/bin/bash
echo "$*" >> "` + logPath + `"
sleep 10
`
	binary := mockPiScript(t, script)

	cfg := testConfig()
	cfg.Roles["smol"] = configRoleWithFallbacks("primary-model", "fb-one")

	orch := NewOrchestrator(cfg, "", nil)
	defer orch.Shutdown()
	orch.SetPiBinary(binary)

	start := time.Now()
	_, _, err := orch.SpawnWithRetry(context.Background(), SpawnInput{
		Agent:      AgentConfig{Name: "fbagent", Role: "smol", Model: "primary-model", Timeout: 400},
		Prompt:     "hi",
		MaxRetries: 1,
	})
	if err == nil {
		t.Fatal("expected the spent budget to end the chain with an error")
	}
	if !strings.Contains(err.Error(), "stopped") {
		t.Errorf("error %q should name the timeout stop, not a restart failure", err)
	}
	if calls := readArgvLog(t, logPath); len(calls) != 1 {
		t.Fatalf("spawn attempts = %d, want 1 (the timeout ended the chain; no restart may follow)", len(calls))
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("run took %v; the 400ms chain budget did not end it", elapsed)
	}
}

// streamScriptPath builds a mock pi binary whose primary attempt streams a
// text delta before the provider error, and whose fallback attempt recovers.
func streamScriptPath(t *testing.T, logPath string) string {
	t.Helper()
	script := `#!/bin/bash
echo "$*" >> "` + logPath + `"
if [[ "$*" == *"fb-one"* ]]; then
  echo '{"type":"text_delta","delta":"recovered"}'
  echo '{"type":"message_end"}'
  exit 0
fi
echo '{"type":"text_delta","delta":"partial "}'
echo '{"type":"error","error":"402 Payment Required"}'
exit 1
`
	return mockPiScript(t, script)
}

// TestSpawnWithInputFallbackStreamPreserved pins the production contract: the
// merged stream carries the failed attempt's events, the restart notice, and
// the retry's events in order, with exactly one run_done — the final
// attempt's. A failed attempt's trailing noise (the spawner's synthetic exit
// error, its own run_done) must stay off the stream: a subscriber seeing it
// would mistake the abandoned attempt for the end.
func TestSpawnWithInputFallbackStreamPreserved(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "argv.log")
	binary := streamScriptPath(t, logPath)

	cfg := testConfig()
	cfg.Roles["smol"] = configRoleWithFallbacks("primary-model", "fb-one")

	orch := NewOrchestrator(cfg, "", nil)
	defer orch.Shutdown()
	orch.SetPiBinary(binary)
	orch.RegisterAgents([]AgentConfig{
		{Name: "fbagent", Role: "smol", Model: "primary-model"},
	})

	events, agentID, err := orch.SpawnWithInputFallback(context.Background(), AgentInput{
		Type:   "fbagent",
		Prompt: "hi",
	})
	if err != nil {
		t.Fatalf("SpawnWithInputFallback: %v", err)
	}
	if agentID == "" {
		t.Fatal("expected a non-empty agent id")
	}

	type got struct {
		typ     string
		content string
		err     string
		status  string
	}
	evv := func(ev Event) got {
		c := ev.Content
		if ev.Type == "error" && c == "" {
			c = ev.Error
		}
		return got{ev.Type, c, ev.Error, ev.Status}
	}
	var seen []got
	for ev := range events {
		seen = append(seen, evv(ev))
	}

	want := []got{
		{"text_delta", "partial ", "", ""},
		{"error", "402 Payment Required", "402 Payment Required", ""},
		{"fallback", "model primary-model failed (402 Payment Required); restarting on fb-one", "", ""},
		{"text_delta", "recovered", "", ""},
		{"message_end", "", "", ""},
		{"run_done", "", "", "completed"},
	}
	if len(seen) != len(want) {
		t.Fatalf("stream = %+v, want exactly %d events", seen, len(want))
	}
	for i, g := range want {
		if seen[i] != g {
			t.Errorf("event %d = %+v, want %+v", i, seen[i], g)
		}
	}
}

// TestSpawnWithInputFallbackExhaustedSurfacesOnError pins the exhausted-chain
// contract on the merged stream: the spawn itself succeeds — the stream is
// live — and the failure arrives as the stream's terminal error event, after
// the last attempt's own error and the notices.
func TestSpawnWithInputFallbackExhaustedSurfacesOnError(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "argv.log")
	// No argv ever matches the marker, so every attempt emits the 402 error.
	binary := fallbackScriptPath(t, logPath, "never-used", "402 Payment Required")

	cfg := testConfig()
	cfg.Roles["smol"] = configRoleWithFallbacks("primary-model", "fb-one")

	orch := NewOrchestrator(cfg, "", nil)
	defer orch.Shutdown()
	orch.SetPiBinary(binary)
	orch.RegisterAgents([]AgentConfig{
		{Name: "fbagent", Role: "smol", Model: "primary-model"},
	})

	events, _, err := orch.SpawnWithInputFallback(context.Background(), AgentInput{
		Type:   "fbagent",
		Prompt: "hi",
	})
	if err != nil {
		t.Fatalf("SpawnWithInputFallback: %v", err)
	}

	var types []string
	var last Event
	for ev := range events {
		types = append(types, ev.Type)
		last = ev
	}
	// The last attempt's provider error, then the wrapper's terminal failure
	// event — the stream's own end of story.
	want := []string{"error", "fallback", "error", "error"}
	if len(types) != len(want) {
		t.Fatalf("stream types = %v, want %v", types, want)
	}
	for i := range want {
		if types[i] != want[i] {
			t.Fatalf("stream types = %v, want %v", types, want)
		}
	}
	if !strings.Contains(last.Error, "402") {
		t.Errorf("terminal event error %q should carry the provider failure", last.Error)
	}
	if calls := readArgvLog(t, logPath); len(calls) != 2 {
		t.Errorf("spawn attempts = %d, want 2 (primary then the single fallback)", len(calls))
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
