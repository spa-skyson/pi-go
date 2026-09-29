package cli

import (
	"bytes"
	"context"
	"io"
	"iter"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// cliGatedLLM is a mock LLM whose first gateN GenerateContent calls block until
// release is closed, so a test can inject a steer while a turn is still
// running. Every request's user texts are recorded in order — request N is the
// Nth RunStreaming pass, and its contents are the conversation so far, which
// is how the test proves a steer ran in the same session.
type cliGatedLLM struct {
	name    string
	gateN   int
	release chan struct{}

	mu       sync.Mutex
	requests [][]string
}

func (m *cliGatedLLM) Name() string { return m.name }

func (m *cliGatedLLM) GenerateContent(_ context.Context, req *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	m.mu.Lock()
	n := len(m.requests)
	var users []string
	for _, c := range req.Contents {
		if c.Role != "user" {
			continue
		}
		for _, p := range c.Parts {
			if p.Text != "" {
				users = append(users, p.Text)
			}
		}
	}
	m.requests = append(m.requests, users)
	m.mu.Unlock()

	return func(yield func(*model.LLMResponse, error) bool) {
		if n < m.gateN {
			<-m.release
		}
		yield(&model.LLMResponse{
			Content: genai.NewContentFromText("turn done", genai.RoleModel),
		}, nil)
	}
}

func (m *cliGatedLLM) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.requests)
}

func (m *cliGatedLLM) userTexts(n int) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if n >= len(m.requests) {
		return ""
	}
	return strings.Join(m.requests[n], "\n")
}

// liveStdout swaps os.Stdout for a pipe drained concurrently, so a test can
// poll for output while runJSON is still running — captureStdout can only show
// what was written by the time fn returns.
func liveStdout(t *testing.T) (snapshot func() string, stop func()) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w

	var mu sync.Mutex
	var buf bytes.Buffer
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		chunk := make([]byte, 4096)
		for {
			n, err := r.Read(chunk)
			mu.Lock()
			buf.Write(chunk[:n])
			mu.Unlock()
			if err != nil {
				return
			}
		}
	}()

	return func() string {
			mu.Lock()
			defer mu.Unlock()
			return buf.String()
		}, func() {
			os.Stdout = orig
			_ = w.Close()
			<-drained
			_ = r.Close()
		}
}

// waitFor polls cond until it holds or the deadline passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestRunJSONSteer_RunsQueuedSteerInSameSession writes a steer while the first
// turn is still running: the steer must be announced with a steer_queued event
// and then run as a second full turn in the same conversation.
func TestRunJSONSteer_RunsQueuedSteerInSameSession(t *testing.T) {
	llm := &cliGatedLLM{name: "gated", gateN: 1, release: make(chan struct{})}
	ag, sessionID := newTestAgent(t, llm)

	pipeR, pipeW := io.Pipe()
	snapshot, stop := liveStdout(t)

	runErr := make(chan error, 1)
	go func() {
		runErr <- runJSON(context.Background(), ag, sessionID, "first", pipeR, nil)
	}()

	// Turn 1 is running (blocked in the mock). Write the steer; the io.Pipe
	// write returns only once the child's scanner consumed the line.
	waitFor(t, "first turn to start", func() bool { return llm.callCount() >= 1 })
	if _, err := pipeW.Write([]byte("second\n")); err != nil {
		t.Fatalf("steer write: %v", err)
	}
	// steer_queued is emitted after the line is queued, so seeing it here
	// proves the steer cannot be missed by the post-turn drain.
	waitFor(t, "steer_queued event", func() bool {
		return strings.Contains(snapshot(), `"type":"steer_queued"`)
	})
	close(llm.release)

	if err := <-runErr; err != nil {
		t.Fatalf("runJSON error: %v", err)
	}
	stop()

	// Two turns ran, and the second carried the steer on top of the first
	// prompt — same conversation, not a fresh one.
	if got := llm.callCount(); got != 2 {
		t.Fatalf("GenerateContent calls = %d, want 2", got)
	}
	if texts := llm.userTexts(1); !strings.Contains(texts, "first") || !strings.Contains(texts, "second") {
		t.Errorf("second request user texts = %q, want both %q and %q", texts, "first", "second")
	}

	out := snapshot()
	if n := strings.Count(out, `"type":"message_start"`); n != 2 {
		t.Errorf("message_start events = %d, want 2 (full cycle per turn)", n)
	}
	if n := strings.Count(out, `"type":"message_end"`); n != 2 {
		t.Errorf("message_end events = %d, want 2", n)
	}
}

// TestRunJSONSteer_EmptyStdinBehavesAsBefore covers the EOF-without-steers
// path: one turn, one full event cycle, no steer_queued.
func TestRunJSONSteer_EmptyStdinBehavesAsBefore(t *testing.T) {
	llm := &cliGatedLLM{name: "plain"}
	ag, sessionID := newTestAgent(t, llm)

	var out string
	func() {
		restore := captureStdout(t, func() {
			if err := runJSON(context.Background(), ag, sessionID, "hello", strings.NewReader(""), nil); err != nil {
				t.Errorf("runJSON error: %v", err)
			}
		})
		out = restore
	}()

	if strings.Contains(out, "steer_queued") {
		t.Error("unexpected steer_queued without any steer")
	}
	if n := strings.Count(out, `"type":"message_start"`); n != 1 {
		t.Errorf("message_start events = %d, want 1", n)
	}
	if n := strings.Count(out, `"type":"message_end"`); n != 1 {
		t.Errorf("message_end events = %d, want 1", n)
	}
	if llm.callCount() != 1 {
		t.Errorf("GenerateContent calls = %d, want 1", llm.callCount())
	}
}

// TestRunJSONSteer_CancelDuringSteerTurn cancels the context while the steer
// turn is running: runJSON must unwind cleanly.
func TestRunJSONSteer_CancelDuringSteerTurn(t *testing.T) {
	llm := &cliGatedLLM{name: "gated2", gateN: 2, release: make(chan struct{})}
	ag, sessionID := newTestAgent(t, llm)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// The mock gates two calls but the release channel can be closed once;
	// every gated call waits on the same close.
	releaseOnce := new(sync.Once)

	pipeR, pipeW := io.Pipe()
	snapshot, stop := liveStdout(t)

	runErr := make(chan error, 1)
	go func() {
		runErr <- runJSON(ctx, ag, sessionID, "first", pipeR, nil)
	}()

	waitFor(t, "first turn to start", func() bool { return llm.callCount() >= 1 })
	if _, err := pipeW.Write([]byte("follow-up\n")); err != nil {
		t.Fatalf("steer write: %v", err)
	}
	waitFor(t, "steer_queued event", func() bool {
		return strings.Contains(snapshot(), `"type":"steer_queued"`)
	})
	releaseOnce.Do(func() { close(llm.release) }) // finish turn 1; the steer turn starts and blocks

	waitFor(t, "steer turn to start", func() bool { return llm.callCount() >= 2 })
	cancel()                                      // cancel while the steer turn runs
	releaseOnce.Do(func() { close(llm.release) }) // unblock the mock so the loop can observe the cancel

	if err := <-runErr; err != nil {
		t.Fatalf("runJSON with canceled context returned error: %v", err)
	}
	stop()

	if got := llm.callCount(); got != 2 {
		t.Errorf("GenerateContent calls = %d, want 2", got)
	}
	if n := strings.Count(snapshot(), `"type":"message_end"`); n < 2 {
		t.Errorf("message_end events = %d, want at least 2", n)
	}
}
