package tui

import (
	"context"
	"errors"
	"iter"
	"strings"
	"sync"
	"testing"
	"time"

	llmmodel "google.golang.org/adk/v2/model"
)

// seqLLM scripts each GenerateContent call as an ordered sequence of
// LLMResponses, yielded with a nil Go error — the shape a provider produces
// when a stream sends content and then dies with a STREAM_ERROR response
// mid-run (see internal/provider/xai.go). Deterministic and sleep-free: the
// only wait in these tests is midturnRetryPause, stubbed to near zero.
type seqLLM struct {
	name string
	mu   sync.Mutex
	n    int
	gen  func(call int) []*llmmodel.LLMResponse
}

func (l *seqLLM) Name() string { return l.name }

func (l *seqLLM) GenerateContent(_ context.Context, _ *llmmodel.LLMRequest, _ bool) iter.Seq2[*llmmodel.LLMResponse, error] {
	l.mu.Lock()
	call := l.n
	l.n++
	l.mu.Unlock()
	return func(yield func(*llmmodel.LLMResponse, error) bool) {
		for _, resp := range l.gen(call) {
			if !yield(resp, nil) {
				return
			}
		}
	}
}

// calls reports how many times the model was asked, the observable behind
// every "did it retry?" assertion here.
func (l *seqLLM) calls() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.n
}

// streamErrResp reproduces the provider shape for a mid-stream failure: a
// content-less response carrying STREAM_ERROR, yielded with a nil Go error.
func streamErrResp(msg string) *llmmodel.LLMResponse {
	return &llmmodel.LLMResponse{ErrorCode: "STREAM_ERROR", ErrorMessage: msg}
}

const stallReason = "llm stream idle timeout after 90s"

// stubMidturnRetryPause swaps the inter-attempt pause for the duration of a
// test and returns its restore func.
func stubMidturnRetryPause(d time.Duration) func() {
	old := midturnRetryPause
	midturnRetryPause = d
	return func() { midturnRetryPause = old }
}

// stallWarnings filters the warning stream down to the mid-turn retry
// announcements, leaving out unrelated warnings (truncation, loop recovery).
func stallWarnings(warnings []string) []string {
	var out []string
	for _, w := range warnings {
		if strings.Contains(w, "stream stalled") {
			out = append(out, w)
		}
	}
	return out
}

// TestRunAgentLoop_MidturnTransientRetriesOnce is the issue #41 scenario: a
// partial answer is on screen, the stream dies with a transient failure, and
// the turn replays instead of dying. The replayed attempt must arrive as a
// fresh message — the partial block stays closed behind the warning, and no
// text is emitted twice.
func TestRunAgentLoop_MidturnTransientRetriesOnce(t *testing.T) {
	restore := stubMidturnRetryPause(time.Millisecond)
	defer restore()

	llm := &seqLLM{name: "stall-once", gen: func(call int) []*llmmodel.LLMResponse {
		if call == 0 {
			// Partial text reaches the user, then the stream stalls.
			return []*llmmodel.LLMResponse{textResp("partial answer "), streamErrResp(stallReason)}
		}
		return []*llmmodel.LLMResponse{textResp("complete answer 42")}
	}}
	a, sid := newRunTestAgent(t, llm)

	res, warnings := driveRunLoopWithWarnings(t, a, sid, "explain something")

	if res.doneErr != nil {
		t.Fatalf("retried turn ended with an error: %v", res.doneErr)
	}
	if got := llm.calls(); got != 2 {
		t.Fatalf("model was called %d times, want 2 (one stall, one replay)", got)
	}
	got := stallWarnings(warnings)
	if len(got) != 1 {
		t.Fatalf("got %d stall warnings, want 1:\n%s", len(got), strings.Join(warnings, "\n"))
	}
	if !strings.Contains(got[0], "retrying 2/3") || !strings.Contains(got[0], stallReason) {
		t.Fatalf("warning = %q, want \"retrying 2/3\" carrying the reason", got[0])
	}
	// Exactly two text emissions, each once: the partial block from the
	// failed attempt, then the replay as a new message. A duplicated
	// partial (replay re-streaming it) or an aggregate re-send fails here.
	want := []string{"partial answer ", "complete answer 42"}
	if strings.Join(res.texts, "|") != strings.Join(want, "|") {
		t.Fatalf("texts = %q, want %q (no duplicated partial, no aggregate re-send)", res.texts, want)
	}
}

// TestRunAgentLoop_MidturnRetriesExhaustAndOfferRetry pins the budget: three
// straight stalls run the turn exactly three times, warn between attempts,
// and end through the ordinary error path whose handler appends the
// "Retry with Ctrl+R or /retry" hint.
func TestRunAgentLoop_MidturnRetriesExhaustAndOfferRetry(t *testing.T) {
	restore := stubMidturnRetryPause(time.Millisecond)
	defer restore()

	llm := &seqLLM{name: "always-stall", gen: func(int) []*llmmodel.LLMResponse {
		return []*llmmodel.LLMResponse{textResp("partial"), streamErrResp(stallReason)}
	}}
	a, sid := newRunTestAgent(t, llm)

	res, warnings := driveRunLoopWithWarnings(t, a, sid, "explain something")

	if res.doneErr == nil || !strings.Contains(res.doneErr.Error(), stallReason) {
		t.Fatalf("final error = %v, want it to carry the stall reason", res.doneErr)
	}
	if got := llm.calls(); got != 3 {
		t.Fatalf("model was called %d times, want exactly 3 (the whole budget)", got)
	}
	if got := stallWarnings(warnings); len(got) != 2 {
		t.Fatalf("got %d stall warnings, want 2 (between the three attempts):\n%s",
			len(got), strings.Join(warnings, "\n"))
	}

	// Exhaustion lands on the existing failed-turn UI: the error block plus
	// the /retry hint.
	m := &model{running: true, lastPrompt: "explain something"}
	m.chatModel = NewChatModel(nil)
	m.handleAgentDone(agentDoneMsg{err: res.doneErr})
	var sawError, sawHint bool
	for _, msg := range m.chatModel.Messages {
		if msg.isError && strings.Contains(msg.content, stallReason) {
			sawError = true
		}
		if msg.isMeta && strings.Contains(msg.content, "Retry with Ctrl+R or /retry") {
			sawHint = true
		}
	}
	if !sawError || !sawHint {
		t.Fatalf("transcript after exhaustion: error=%v hint=%v", sawError, sawHint)
	}
}

// TestRunAgentLoop_NoRetryAfterToolTraffic pins the replay guard: once an
// attempt has shown a FunctionCall card, a transient failure must end the
// turn — replaying would execute the tool a second time.
func TestRunAgentLoop_NoRetryAfterToolTraffic(t *testing.T) {
	restore := stubMidturnRetryPause(time.Millisecond)
	defer restore()

	llm := &seqLLM{name: "stall-after-call", gen: func(call int) []*llmmodel.LLMResponse {
		if call == 0 {
			// The model calls a tool and the stream dies right behind
			// the call card.
			return []*llmmodel.LLMResponse{
				callResp("bash", map[string]any{"command": "echo hi"}),
				streamErrResp(stallReason),
			}
		}
		return []*llmmodel.LLMResponse{streamErrResp(stallReason)}
	}}
	a, sid := newRunTestAgent(t, llm)

	res, warnings := driveRunLoopWithWarnings(t, a, sid, "run a command")

	if res.doneErr == nil || !strings.Contains(res.doneErr.Error(), stallReason) {
		t.Fatalf("error = %v, want the stall surfaced without a replay", res.doneErr)
	}
	if got := stallWarnings(warnings); len(got) != 0 {
		t.Fatalf("replayed after tool traffic: %q", got)
	}
	if len(res.toolCalls) == 0 {
		t.Fatal("tool call never surfaced; the scenario did not exercise the guard")
	}
	if len(res.toolCalls) != 1 {
		t.Fatalf("got %d tool call cards, want 1 (the replay would show a second)", len(res.toolCalls))
	}
}

// TestRunAgentLoop_NoRetryOnTerminalError pins the classification: a 402 will
// not clear however often the request is re-sent, so the turn fails on the
// first attempt without spending the budget.
func TestRunAgentLoop_NoRetryOnTerminalError(t *testing.T) {
	restore := stubMidturnRetryPause(time.Millisecond)
	defer restore()

	const reason = "402 Payment Required: plan limit reached"
	llm := &seqLLM{name: "payment", gen: func(int) []*llmmodel.LLMResponse {
		return []*llmmodel.LLMResponse{textResp("partial answer"), streamErrResp(reason)}
	}}
	a, sid := newRunTestAgent(t, llm)

	res, warnings := driveRunLoopWithWarnings(t, a, sid, "explain something")

	if res.doneErr == nil || !strings.Contains(res.doneErr.Error(), "402") {
		t.Fatalf("error = %v, want the 402 surfaced", res.doneErr)
	}
	if got := llm.calls(); got != 1 {
		t.Fatalf("model was called %d times, want 1 (terminal errors are not retried)", got)
	}
	if got := stallWarnings(warnings); len(got) != 0 {
		t.Fatalf("retried a terminal error: %q", got)
	}
}

// TestRunAgentLoop_CancelDuringRetryPause pins that Esc during the
// between-attempts pause breaks the wait: the turn ends with context.Canceled
// well before the full pause elapses, and no further attempt starts.
func TestRunAgentLoop_CancelDuringRetryPause(t *testing.T) {
	restore := stubMidturnRetryPause(2 * time.Second)
	defer restore()

	llm := &seqLLM{name: "stall", gen: func(int) []*llmmodel.LLMResponse {
		return []*llmmodel.LLMResponse{streamErrResp(stallReason)}
	}}
	a, sid := newRunTestAgent(t, llm)

	m := &model{cfg: Config{Agent: a, SessionID: sid}, ctx: context.Background()}
	m.agentCh = make(chan agentMsg, 64)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	var doneErr error
	go func() {
		defer close(done)
		for msg := range m.agentCh {
			if _, ok := msg.(agentWarningMsg); ok {
				cancel() // the user's second Esc, mid-pause
				continue
			}
			if d, ok := msg.(agentDoneMsg); ok {
				doneErr = d.err
			}
		}
	}()

	start := time.Now()
	go m.runAgentLoop(ctx, "hello", m.agentCh, m.agentRun())
	<-done
	elapsed := time.Since(start)

	if !errors.Is(doneErr, context.Canceled) {
		t.Fatalf("cancel during the pause must fail the turn with context.Canceled, got %v", doneErr)
	}
	// The broken behavior sleeps the full pause before noticing; the fixed
	// one ends within a fraction of it. The pause itself is 2s.
	if elapsed >= time.Second {
		t.Fatalf("the pause ignored cancelation: turn ended after %s", elapsed)
	}
	if got := llm.calls(); got != 1 {
		t.Fatalf("model was called %d times after cancel, want 1 (no attempt past the pause)", got)
	}
}
