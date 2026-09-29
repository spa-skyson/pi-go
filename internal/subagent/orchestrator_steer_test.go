package subagent

import (
	"bufio"
	"io"
	"testing"
)

// Steer finds the tracked process and writes through it; an unknown agent ID
// is an error.
func TestOrchestrator_Steer(t *testing.T) {
	cfg := testConfig()
	orch := NewOrchestrator(cfg, "", nil)

	if err := orch.Steer("missing", "hello"); err == nil {
		t.Error("expected error steering an unknown agent")
	}

	pr, pw := io.Pipe()
	// io.Pipe writes block until read, so the reader must be running before
	// Steer is called.
	lines := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(pr)
		if sc.Scan() {
			lines <- sc.Text()
		}
	}()

	orch.mu.Lock()
	orch.agents["a1"] = &agentState{
		ID:     "a1",
		Type:   "explore",
		Status: "running",
		Process: &Process{
			done:  make(chan struct{}),
			stdin: pw,
		},
	}
	orch.mu.Unlock()

	if err := orch.Steer("a1", "hello steer"); err != nil {
		t.Fatalf("steer: %v", err)
	}
	got := <-lines
	if got != "hello steer" {
		t.Errorf("pipe line = %q, want %q", got, "hello steer")
	}
	_ = pw.Close()
	_ = pr.Close()
}
