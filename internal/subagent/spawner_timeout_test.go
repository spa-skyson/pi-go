package subagent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakePi writes a shell script that stands in for the child `pi --mode json`
// binary and returns its path. The script ignores its arguments; only what it
// writes to stdout/stderr and how long it takes matters here.
func fakePi(t *testing.T, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		// The stand-in agent is a #!/bin/bash script, and Spawner takes a single
		// binary path with nowhere to name an interpreter. Go's os/exec on
		// Windows starts either a real executable or a .bat/.cmd through
		// cmd.exe; wrapping the script in a .bat would cover the simple cases
		// but would put cmd.exe between the spawner and bash, so a kill would
		// land on the wrapper rather than the agent -- which is precisely what
		// the cancel and inactivity tests measure. Only the fixture is
		// POSIX-bound here; Spawner itself execs whatever path it is given.
		t.Skip("stand-in pi agent is a bash script; Windows cannot exec it directly")
	}
	path := filepath.Join(t.TempDir(), "fake-pi")
	body := "#!/bin/bash\n" + script + "\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("writing fake pi: %v", err)
	}
	return path
}

// drain consumes every event and returns once the process is finished.
func drain(t *testing.T, proc *Process) (string, error) {
	t.Helper()
	for range proc.Events() {
	}
	return proc.Wait()
}

// TestSpawn_SteadyOutputSurvivesInactivityWindow is the regression guard for the
// bug this fixes: an agent that keeps producing output must never be killed for
// inactivity, however long it runs in total.
func TestSpawn_SteadyOutputSurvivesInactivityWindow(t *testing.T) {
	t.Setenv("PI_SUBAGENT_TIMEOUT_MS", "20000") // generous absolute backstop
	t.Setenv("PI_SUBAGENT_INACTIVITY_MS", "400")

	// Emits a line every 100ms for ~1.2s — four times the inactivity window in
	// total, but never silent for more than 100ms.
	s := &Spawner{PiBinary: fakePi(t, `
for i in $(seq 1 12); do
  printf '{"type":"text_delta","delta":"tick "}\n'
  sleep 0.1
done
`)}

	proc, err := s.Spawn(context.Background(), SpawnOpts{Prompt: "go"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	result, err := drain(t, proc)
	if err != nil {
		t.Fatalf("a steadily-producing agent was killed: %v", err)
	}
	if got := strings.Count(result, "tick"); got != 12 {
		t.Errorf("got %d ticks, want 12 (result=%q)", got, result)
	}
}

func TestSpawn_SilentAgentIsKilledByInactivity(t *testing.T) {
	t.Setenv("PI_SUBAGENT_TIMEOUT_MS", "20000") // must NOT be what fires
	t.Setenv("PI_SUBAGENT_INACTIVITY_MS", "300")

	// One line, then silence well past the inactivity window.
	s := &Spawner{PiBinary: fakePi(t, `
printf '{"type":"text_delta","delta":"hello"}\n'
sleep 20
`)}

	start := time.Now()
	proc, err := s.Spawn(context.Background(), SpawnOpts{Prompt: "go"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	_, err = drain(t, proc)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("a silent agent was not killed")
	}
	if !errors.Is(err, ErrSubagentTimeout) {
		t.Errorf("error %v does not wrap ErrSubagentTimeout", err)
	}
	if !strings.Contains(err.Error(), "no output") {
		t.Errorf("error %q does not say the agent went silent", err)
	}
	// It must die on the inactivity limit, not sit until the absolute one.
	if elapsed > 10*time.Second {
		t.Errorf("took %v — looks like the absolute cap fired, not inactivity", elapsed)
	}
}

func TestSpawn_AbsoluteCapStillApplies(t *testing.T) {
	// Output flows constantly, so inactivity never fires; only the absolute cap
	// can stop this agent.
	t.Setenv("PI_SUBAGENT_TIMEOUT_MS", "700")
	t.Setenv("PI_SUBAGENT_INACTIVITY_MS", "500")

	s := &Spawner{PiBinary: fakePi(t, `
while true; do
  printf '{"type":"text_delta","delta":"x"}\n'
  sleep 0.05
done
`)}

	proc, err := s.Spawn(context.Background(), SpawnOpts{Prompt: "go"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	_, err = drain(t, proc)
	if err == nil {
		t.Fatal("the absolute cap did not stop a runaway agent")
	}
	if !errors.Is(err, ErrSubagentTimeout) {
		t.Errorf("error %v does not wrap ErrSubagentTimeout", err)
	}
	if !strings.Contains(err.Error(), "time limit") {
		t.Errorf("error %q does not name the absolute limit", err)
	}
}

// TestSpawn_TimeoutErrorNamesTheKnobs checks the message is actionable. The
// whole failure mode this fixes was an error that said only "signal: killed".
func TestSpawn_TimeoutErrorNamesTheKnobs(t *testing.T) {
	t.Setenv("PI_SUBAGENT_TIMEOUT_MS", "20000")
	t.Setenv("PI_SUBAGENT_INACTIVITY_MS", "250")

	s := &Spawner{PiBinary: fakePi(t, `sleep 20`)}
	proc, err := s.Spawn(context.Background(), SpawnOpts{Prompt: "go"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	_, err = drain(t, proc)
	if err == nil {
		t.Fatal("expected a timeout")
	}
	msg := err.Error()
	if strings.Contains(msg, "signal: killed") {
		t.Errorf("timeout still reported as a bare signal kill: %q", msg)
	}
	for _, want := range []string{"PI_SUBAGENT_TIMEOUT_MS", "timeout:"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not mention %q", msg, want)
		}
	}
}

// TestSpawn_LargeStderrDoesNotDeadlock covers the pipe deadlock: stdout and
// stderr have separate kernel buffers, so draining them in sequence wedges any
// child that writes more than a pipe buffer (~64KB) to stderr.
func TestSpawn_LargeStderrDoesNotDeadlock(t *testing.T) {
	t.Setenv("PI_SUBAGENT_TIMEOUT_MS", "15000")
	t.Setenv("PI_SUBAGENT_INACTIVITY_MS", "10000")

	// ~300KB of stderr, far past the pipe buffer, then a normal result.
	s := &Spawner{PiBinary: fakePi(t, `
for i in $(seq 1 3000); do
  printf 'noisy diagnostic line %d aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n' "$i" >&2
done
printf '{"type":"text_delta","delta":"done"}\n'
`)}

	done := make(chan struct{})
	var result string
	var spawnErr error
	go func() {
		defer close(done)
		proc, err := s.Spawn(context.Background(), SpawnOpts{Prompt: "go"})
		if err != nil {
			spawnErr = err
			return
		}
		result, spawnErr = drain(t, proc)
	}()

	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("spawn deadlocked on a child that wrote heavily to stderr")
	}

	if spawnErr != nil {
		t.Fatalf("unexpected error: %v", spawnErr)
	}
	if !strings.Contains(result, "done") {
		t.Errorf("result = %q, want the child's output", result)
	}
}

// TestSpawn_StderrIsReportedOnFailure guards the diagnostic value of stderr:
// capturing it concurrently must not lose it.
func TestSpawn_StderrIsReportedOnFailure(t *testing.T) {
	t.Setenv("PI_SUBAGENT_TIMEOUT_MS", "15000")
	t.Setenv("PI_SUBAGENT_INACTIVITY_MS", "10000")

	s := &Spawner{PiBinary: fakePi(t, `
echo "config file is malformed" >&2
exit 3
`)}

	proc, err := s.Spawn(context.Background(), SpawnOpts{Prompt: "go"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	_, err = drain(t, proc)
	if err == nil {
		t.Fatal("a failing child produced no error")
	}
	if !strings.Contains(err.Error(), "config file is malformed") {
		t.Errorf("error %q lost the child's stderr", err)
	}
}

func TestSpawn_CleanExitReportsNoError(t *testing.T) {
	t.Setenv("PI_SUBAGENT_TIMEOUT_MS", "15000")
	t.Setenv("PI_SUBAGENT_INACTIVITY_MS", "10000")

	s := &Spawner{PiBinary: fakePi(t, `printf '{"type":"text_delta","delta":"ok"}\n'`)}

	proc, err := s.Spawn(context.Background(), SpawnOpts{Prompt: "go"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	result, err := drain(t, proc)
	if err != nil {
		t.Fatalf("clean exit reported an error: %v", err)
	}
	if result != "ok" {
		t.Errorf("result = %q, want %q", result, "ok")
	}
}

func TestResolveTimeout_InactivityEnvOverride(t *testing.T) {
	t.Setenv("PI_SUBAGENT_INACTIVITY_MS", "5000")

	cfg := ResolveTimeout(0)
	if cfg.Inactivity != 5*time.Second {
		t.Errorf("Inactivity = %v, want 5s", cfg.Inactivity)
	}
	if cfg.Absolute != DefaultAbsoluteTimeout {
		t.Errorf("Absolute = %v, want the default %v", cfg.Absolute, DefaultAbsoluteTimeout)
	}
}

func TestResolveTimeout_InactivityStillClampedToAbsolute(t *testing.T) {
	t.Setenv("PI_SUBAGENT_INACTIVITY_MS", "60000")

	cfg := ResolveTimeout(1000) // 1s absolute from frontmatter
	if cfg.Inactivity > cfg.Absolute {
		t.Errorf("Inactivity %v exceeds Absolute %v", cfg.Inactivity, cfg.Absolute)
	}
}

func TestDefaultAbsoluteTimeoutIsGenerous(t *testing.T) {
	// A five-minute cap killed productive review agents mid-answer. The
	// inactivity timer is the working limit; this one is only a backstop.
	if DefaultAbsoluteTimeout < 10*time.Minute {
		t.Errorf("DefaultAbsoluteTimeout = %v, want at least 10m", DefaultAbsoluteTimeout)
	}
}

// TestSpawn_KeepAliveSurvivesInactivityWindow is the parent half of the
// issue #37 heartbeat: a child emitting keep-alive lines while it waits on
// the LLM must survive an inactivity window many times the longest silence,
// and the keep-alive itself must never surface in the event stream — the
// parent's watchdog resets on the bare line; nobody else sees it.
//
// Ratios mirror the production shape (child in LLM wait far past the 5m
// window) at test speed: 1.2s of keep-alive output against a 400ms window.
// The child half — that a real LLM wait emits those lines — is covered by
// TestStreamHeartbeatTicksWhileWaiting in internal/provider and the
// WithStreamHeartbeat wiring in runJSONTurn.
func TestSpawn_KeepAliveSurvivesInactivityWindow(t *testing.T) {
	t.Setenv("PI_SUBAGENT_TIMEOUT_MS", "20000") // generous absolute backstop
	t.Setenv("PI_SUBAGENT_INACTIVITY_MS", "400")

	s := &Spawner{PiBinary: fakePi(t, `
for i in $(seq 1 12); do
  printf '{"type":"keepalive"}\n'
  sleep 0.1
done
`)}

	proc, err := s.Spawn(context.Background(), SpawnOpts{Prompt: "go"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	var events []Event
	for ev := range proc.Events() {
		events = append(events, ev)
	}
	result, err := proc.Wait()
	if err != nil {
		t.Fatalf("a keep-alive child was killed: %v", err)
	}
	if result != "" {
		t.Errorf("keep-alive lines leaked into the result text: %q", result)
	}
	for _, ev := range events {
		if ev.Type == "keepalive" {
			t.Error("keep-alive events reached the event stream; the spawner must swallow them")
			break
		}
	}
}

// A silent child — no keep-alive, no output — must still die on the
// inactivity limit: the heartbeat covers a live child in LLM wait, not a
// wedged one. The wedge may sit inside the LLM read where the provider watch
// cannot tick; five minutes of nothing is still the end.
func TestSpawn_DeadChildStillKilledDespiteHeartbeatMechanism(t *testing.T) {
	t.Setenv("PI_SUBAGENT_TIMEOUT_MS", "20000")
	t.Setenv("PI_SUBAGENT_INACTIVITY_MS", "300")

	s := &Spawner{PiBinary: fakePi(t, `sleep 20`)}
	proc, err := s.Spawn(context.Background(), SpawnOpts{Prompt: "go"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	_, err = drain(t, proc)
	if err == nil || !errors.Is(err, ErrSubagentTimeout) {
		t.Fatalf("a silent child survived: err=%v, want ErrSubagentTimeout", err)
	}
}
