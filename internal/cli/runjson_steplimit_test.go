package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	adktool "google.golang.org/adk/v2/tool"
	"google.golang.org/genai"

	"github.com/spa-skyson/pi-rate/internal/agent"
	"github.com/spa-skyson/pi-rate/internal/extension"
	"github.com/spa-skyson/pi-rate/internal/logger"
	"github.com/spa-skyson/pi-rate/internal/tools"
)

// budgetBurnerLLM answers every model call with the same read call — the
// degenerate model from issue #51 that never stops of its own accord, so the
// run only ends when the step budget fires.
type budgetBurnerLLM struct {
	name string
	file string
}

func (m *budgetBurnerLLM) Name() string { return m.name }

func (m *budgetBurnerLLM) GenerateContent(ctx context.Context, _ *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		if err := ctx.Err(); err != nil {
			yield(nil, err)
			return
		}
		yield(&model.LLMResponse{
			Content: &genai.Content{
				Role: genai.RoleModel,
				Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{
					ID:   "c1",
					Name: "read",
					Args: map[string]any{"file_path": m.file},
				}}},
			},
		}, nil)
	}
}

// TestRunJSONTurn_StepLimitEmitsErrorEvent is the child half of issue #51: a
// turn ended by the step budget must carry an explicit `error` event on the
// JSON stream. Before the fix the only trace of the failure was the error
// field inside the last tool_result payload — consumers that accumulate
// text deltas read the partial output as a finished report.
//
// The exit protocol is part of the contract: the turn ends through the normal
// message_end and runJSONTurn returns nil, so the child exits 0. A non-zero
// exit would read as a crash to the parent's fallback chain and re-spawn the
// whole task the budget just stopped.
func TestRunJSONTurn_StepLimitEmitsErrorEvent(t *testing.T) {
	dir := t.TempDir()
	sb, err := tools.NewSandbox(dir)
	if err != nil {
		t.Fatalf("NewSandbox: %v", err)
	}
	t.Cleanup(func() { _ = sb.Close() })

	file := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	const limit = 2
	chain := extension.ComposeAfterToolChain([]llmagent.AfterToolCallback{
		agent.NewStepLimitCallback(limit),
	})
	a, err := agent.New(agent.Config{
		Model:              &budgetBurnerLLM{name: "burner", file: file},
		Tools:              mustCoreTools(t, sb),
		Instruction:        "t",
		AfterToolCallbacks: chain,
	})
	if err != nil {
		t.Fatal(err)
	}
	sid, _, err := a.CreateSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	lg, err := logger.New()
	if err != nil {
		t.Skipf("logger unavailable in test environment: %v", err)
	}

	var buf bytes.Buffer
	em := newJSONEmitter(json.NewEncoder(&buf), lg, false)

	// The turn must terminate on its own — an unbounded loop here means the
	// budget no longer ends the run.
	if err := runJSONTurn(context.Background(), a, sid, "go", em, agent.DefaultRetryConfig(), lg); err != nil {
		t.Fatalf("runJSONTurn = %v, want nil (the child must exit cleanly)", err)
	}

	var (
		errEvents []map[string]any
		sawFR     bool
		lastIsEnd bool
	)
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("non-JSON line on the stream: %q", line)
		}
		switch ev["type"] {
		case "error":
			errEvents = append(errEvents, ev)
		case "tool_result":
			if content, _ := ev["content"].(string); strings.Contains(content, "steps limit") {
				sawFR = true
			}
		case "message_end":
			lastIsEnd = true
		}
	}

	if !sawFR {
		t.Error("the failing function response never reached the stream as tool_result")
	}
	if !lastIsEnd {
		t.Error("the stream did not end with message_end — the turn did not close cleanly")
	}
	if len(errEvents) != 1 {
		t.Fatalf("error events = %d, want exactly 1; stream:\n%s", len(errEvents), buf.String())
	}
	if got, _ := errEvents[0]["error"].(string); !strings.Contains(got, "steps limit") {
		t.Errorf("error event text = %q, want it to name the steps limit", got)
	}
}

func mustCoreTools(t *testing.T, sb *tools.Sandbox) []adktool.Tool {
	t.Helper()
	ct, err := tools.CoreTools(sb)
	if err != nil {
		t.Fatalf("CoreTools: %v", err)
	}
	return ct
}
