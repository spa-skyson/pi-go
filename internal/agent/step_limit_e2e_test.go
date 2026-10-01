package agent

import (
	"context"
	"fmt"
	"iter"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/spa-skyson/pi-rate/internal/extension"
	"github.com/spa-skyson/pi-rate/internal/tools"
)

// stubbornLLM answers every model call with the same function call, ignoring
// whatever tool results came back — the degenerate model from the steps-limit
// bug report that keeps calling tools after the budget is gone. It watches
// ctx so a runaway loop (the pre-fix behavior) can be stopped by canceling.
type stubbornLLM struct {
	name  string
	call  *genai.FunctionCall
	mu    sync.Mutex
	calls int
}

func (m *stubbornLLM) Name() string { return m.name }

func (m *stubbornLLM) GenerateContent(ctx context.Context, _ *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	m.mu.Lock()
	m.calls++
	m.mu.Unlock()
	return func(yield func(*model.LLMResponse, error) bool) {
		if err := ctx.Err(); err != nil {
			yield(nil, err)
			return
		}
		yield(&model.LLMResponse{
			Content: &genai.Content{
				Role:  genai.RoleModel,
				Parts: []*genai.Part{{FunctionCall: m.call}},
			},
		}, nil)
	}
}

func (m *stubbornLLM) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

// TestStepLimitTerminatesTurnWhenModelKeepsCallingTools is the end-to-end
// guard for the steps budget. The model calls a tool on every turn and never
// stops on its own; without the termination flag in NewStepLimitCallback this
// run never ends — every over-budget call fails with "steps limit N reached",
// the model ignores it and calls again, burning tokens forever. With it, the
// turn must close after exactly limit+1 tool calls, the last function
// response carrying the limit marker.
func TestStepLimitTerminatesTurnWhenModelKeepsCallingTools(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "x")

	ct, err := tools.CoreTools(testSandbox(t, dir))
	if err != nil {
		t.Fatal(err)
	}

	const limit = 3
	llm := &stubbornLLM{
		name: "stubborn",
		call: &genai.FunctionCall{ID: "c1", Name: "read", Args: map[string]any{"file_path": dir + "/f.txt"}},
	}
	chain := extension.ComposeAfterToolChain([]llmagent.AfterToolCallback{NewStepLimitCallback(limit)})
	a, err := New(Config{Model: llm, Tools: ct, Instruction: "t", AfterToolCallbacks: chain})
	if err != nil {
		t.Fatal(err)
	}
	sid, _, err := a.CreateSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var (
		runErr error
		done   = make(chan struct{})
	)
	var lastSkipFinal bool
	var lastFr *genai.FunctionResponse
	go func() {
		defer close(done)
		for ev, err := range a.Run(ctx, sid, "go") {
			if err != nil {
				runErr = err
				return
			}
			if ev == nil || ev.Content == nil {
				continue
			}
			lastSkipFinal = ev.Actions.SkipSummarization
			for _, p := range ev.Content.Parts {
				if p.FunctionResponse != nil {
					lastFr = p.FunctionResponse
				}
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		cancel()
		t.Fatal("turn did not terminate after the steps limit — the loop is spinning")
	}
	if runErr != nil {
		t.Fatalf("run error: %v", runErr)
	}

	// The over-budget call runs (after-tool), then the turn ends: one model
	// call per tool call, so limit+1 in total and nothing after.
	if got := llm.callCount(); got != limit+1 {
		t.Fatalf("model called %d times, want exactly %d (limit+1)", got, limit+1)
	}
	if !lastSkipFinal {
		t.Error("final event does not carry SkipSummarization; ADK would not have stopped")
	}
	if lastFr == nil {
		t.Fatal("no function response seen; expected the limit marker on the last tool result")
	}
	if got := fmt.Sprint(lastFr.Response["error"]); !strings.Contains(got, "steps limit 3 reached") {
		t.Fatalf("last tool response error = %q, want containing %q", got, "steps limit 3 reached")
	}
}
