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

// toolThenAnswerLLM is a scripted model: within one run it issues exactly
// `tools` function calls and then a text answer, which ends the run. It
// records the model round-trips of every completed run, so a test can assert
// how much work each turn actually did.
type toolThenAnswerLLM struct {
	name  string
	call  *genai.FunctionCall
	tools int

	mu     sync.Mutex
	calls  int
	inRun  int
	rounds []int // model round-trips per completed run, in completion order
}

func (m *toolThenAnswerLLM) Name() string { return m.name }

func (m *toolThenAnswerLLM) GenerateContent(ctx context.Context, _ *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	m.mu.Lock()
	m.calls++
	m.inRun++
	n := m.inRun
	tools := m.tools
	m.mu.Unlock()
	return func(yield func(*model.LLMResponse, error) bool) {
		if err := ctx.Err(); err != nil {
			yield(nil, err)
			return
		}
		if n <= tools {
			yield(&model.LLMResponse{
				Content: &genai.Content{
					Role:  genai.RoleModel,
					Parts: []*genai.Part{{FunctionCall: m.call}},
				},
			}, nil)
			return
		}
		// The text answer ends the run; log this run's round-trip count.
		m.mu.Lock()
		m.rounds = append(m.rounds, n)
		m.inRun = 0
		m.mu.Unlock()
		yield(&model.LLMResponse{
			Content: &genai.Content{
				Role:  genai.RoleModel,
				Parts: []*genai.Part{{Text: "done"}},
			},
		}, nil)
	}
}

func (m *toolThenAnswerLLM) completedRuns() []int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]int(nil), m.rounds...)
}

// TestStepBudgetResetsOnEachUserTurn drives the TUI's exact path: the
// interactive front-end builds the step-limit callback once and reuses it for
// every RunStreaming call, one per user turn. Two sequential turns of exactly
// `limit` tools each must both finish clean — the second turn restarts from
// zero instead of inheriting the first turn's count (the session-lifetime
// counter cut a turn off mid-work after enough earlier turns). Within a
// single turn the budget still bites: that guard is pinned by
// TestStepLimitTerminatesTurnWhenModelKeepsCallingTools.
func TestStepBudgetResetsOnEachUserTurn(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "x")

	ct, err := tools.CoreTools(testSandbox(t, dir))
	if err != nil {
		t.Fatal(err)
	}

	const limit = 3
	llm := &toolThenAnswerLLM{
		name:  "scripted",
		tools: limit,
		call:  &genai.FunctionCall{ID: "c1", Name: "read", Args: map[string]any{"file_path": dir + "/f.txt"}},
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
		runErr   error
		limitHit []string
	)
	runTurn := func(turn int) {
		for ev, err := range a.RunStreaming(ctx, sid, "go") {
			if err != nil {
				runErr = fmt.Errorf("turn %d: %w", turn, err)
				return
			}
			if ev == nil || ev.Content == nil {
				continue
			}
			for _, p := range ev.Content.Parts {
				if p.FunctionResponse == nil {
					continue
				}
				if msg := fmt.Sprint(p.FunctionResponse.Response["error"]); strings.Contains(msg, "steps limit") {
					limitHit = append(limitHit, fmt.Sprintf("turn %d: %s", turn, msg))
				}
			}
		}
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		runTurn(1)
		runTurn(2)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		cancel()
		t.Fatal("turns did not terminate — the loop is spinning")
	}
	if runErr != nil {
		t.Fatalf("run error: %v", runErr)
	}
	for _, msg := range limitHit {
		t.Errorf("tool response carries a steps-limit error — the budget leaked across turns: %s", msg)
	}

	// Each turn ran to its text answer: limit tool round-trips + 1. A leaked
	// budget ends turn 2 on its first tool result, so this also pins that
	// both turns did the full scripted work.
	runs := llm.completedRuns()
	if len(runs) != 2 || runs[0] != limit+1 || runs[1] != limit+1 {
		t.Fatalf("model round-trips per completed run = %v, want [%d %d]", runs, limit+1, limit+1)
	}
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
