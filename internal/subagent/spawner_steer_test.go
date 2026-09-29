package subagent

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// The steer channel: Process.Steer writes one line to the child's stdin, and a
// json-mode child turns it into a follow-up turn. These tests use a mock child
// that reads one line from stdin and echoes it back as a text_delta event.

func TestSpawner_SteerWritesLineToChild(t *testing.T) {
	script := `
read -r line
echo "{\"type\":\"text_delta\",\"delta\":\"steered: $line\"}"
`
	binary := mockPiScript(t, script)
	spawner := NewSpawner(binary)

	proc, err := spawner.Spawn(context.Background(), SpawnOpts{
		AgentID: "steer-1",
		Prompt:  "run",
	})
	if err != nil {
		t.Fatalf("spawn failed: %v", err)
	}

	if err := proc.Steer("follow up"); err != nil {
		t.Fatalf("steer: %v", err)
	}

	// The child echoes the steer back as a text_delta event.
	var got string
	for ev := range proc.Events() {
		if ev.Type == "text_delta" && strings.Contains(ev.Content, "follow up") {
			got = ev.Content
		}
	}
	if got == "" {
		t.Fatal("child never received the steer line")
	}
	if !strings.Contains(got, "steered: follow up") {
		t.Errorf("child echoed %q, want the steer text", got)
	}

	// Once the process is done, steering fails with ErrProcessDone.
	if _, err := proc.Wait(); err != nil {
		t.Fatalf("wait: %v", err)
	}
	if err := proc.Steer("too late"); !errors.Is(err, ErrProcessDone) {
		t.Errorf("steer after exit = %v, want ErrProcessDone", err)
	}
}

func TestProcess_SteerWithoutStdin(t *testing.T) {
	// ACP/codex runners build Process without the stdin channel.
	p := &Process{done: make(chan struct{})}
	err := p.Steer("x")
	if err == nil || !strings.Contains(err.Error(), "no stdin channel") {
		t.Errorf("steer without stdin = %v, want the no-stdin error", err)
	}
}

func TestProcess_SteerAfterDone(t *testing.T) {
	p := &Process{done: make(chan struct{})}
	close(p.done)
	if err := p.Steer("x"); !errors.Is(err, ErrProcessDone) {
		t.Errorf("steer on done process = %v, want ErrProcessDone", err)
	}
}

// buildCommand must leave the stdin pipe creatable — startChildProcess opens
// it before Start, so a spawn aborts cleanly if the pipe cannot be made. The
// end-to-end wiring is covered by TestSpawner_SteerWritesLineToChild; here we
// pin the failure path's shape by checking a spawned Process always has the
// writer attached.
func TestSpawner_ProcessHasSteerWriter(t *testing.T) {
	binary := mockPiScript(t, `echo '{"type":"message_end"}'`)
	spawner := NewSpawner(binary)

	proc, err := spawner.Spawn(context.Background(), SpawnOpts{AgentID: "stdin-1", Prompt: "run"})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	proc.mu.Lock()
	hasStdin := proc.stdin != nil
	proc.mu.Unlock()
	if !hasStdin {
		t.Error("spawned Process has no stdin writer")
	}
	_, _ = proc.Wait()
}
