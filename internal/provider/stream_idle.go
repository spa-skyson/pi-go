package provider

import (
	"context"
	"fmt"
	"iter"
	"time"

	"google.golang.org/adk/v2/model"
)

// DefaultStreamHeartbeatInterval is how often a streaming wait emits a
// keep-alive tick when the hook is installed (see WithStreamHeartbeat). It
// exists so a subagent child sitting out a long time-to-first-token keeps
// emitting output lines, which is what keeps the parent's 5-minute
// inactivity watchdog from killing it (issue #37). 30s is well inside that
// window even against a disabled idle timeout, and far above any real
// inter-chunk gap — a healthy stream delivers chunks more often than once a
// second.
const DefaultStreamHeartbeatInterval = 30 * time.Second

// idleStreamModel wraps a model.LLM and watches every streaming call for
// silence.
//
// Two independent behaviors, one watch loop:
//
//   - Idle timeout: when no chunk arrives for opts.StreamIdleTimeout, the
//     watch cancels the request context — which is the only way to unblock a
//     stream read parked in the HTTP layer — and reports the failure as
//     "llm stream idle". Gateways routinely sit 150-300s+ before the first
//     token (issue #37); today that silence is endured passively until the
//     subagent watchdog kills the whole child at 5 minutes.
//   - Heartbeat: while waiting, it ticks the hook from WithStreamHeartbeat
//     every DefaultStreamHeartbeatInterval (or timeout/3, whichever is
//     shorter). The --mode json child installs a hook that writes a
//     keep-alive line, so "waiting for the first token" stops being
//     indistinguishable from "wedged" to the parent's inactivity watchdog.
//
// Scope (v1, deliberate): the idle failure is a plain transient error, so the
// retry budget that picks it up is the per-run one in internal/agent — which
// only replays a run that has yielded nothing yet. An idle abort after the
// turn's first event ends the turn as any provider failure would; per-request
// replay of mid-run failures would need the idle timer inside each provider's
// retryStream attempt, which is the upgrade path if the first-token window
// alone proves too narrow.
type idleStreamModel struct {
	inner model.LLM
	// timeout is the stream-silence budget. Zero disables the abort; the
	// heartbeat keeps working in that case.
	timeout time.Duration
	// tick is the heartbeat interval; zero disables ticking.
	tick time.Duration
}

var _ model.LLM = idleStreamModel{}

func (m idleStreamModel) Name() string { return m.inner.Name() }

// chunk is one (response, error) pair moved from the pump goroutine to the
// watch loop.
type chunk struct {
	resp *model.LLMResponse
	err  error
}

// GenerateContent forwards to the wrapped model, watching streaming calls
// for silence. Non-streaming calls pass through untouched at any timeout:
// with no chunk cadence a budget would degenerate into a whole-call cap,
// which is wrong for callers that run without a retry budget — autocompact
// and summarize (internal/session) would silently degrade on slow gateways,
// and issue #37 is about streams, not whole calls. A caller that leaves
// StreamIdleTimeout zero passes straight through with no wrapper overhead.
func (m idleStreamModel) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	if !stream {
		return m.inner.GenerateContent(ctx, req, stream)
	}
	heartbeat := streamHeartbeatFromContext(ctx)
	if m.timeout <= 0 && heartbeat == nil {
		return m.inner.GenerateContent(ctx, req, stream)
	}
	return func(yield func(*model.LLMResponse, error) bool) {
		// The watch owns a child context so firing it can unblock the stream
		// read; canceling anything broader would kill the caller's turn.
		ictx, cancel := context.WithCancel(ctx)
		defer cancel()

		// Pump the pull iterator into a channel so the watch can select
		// "a chunk arrived" against the timers. done makes the pump exit
		// when the consumer stops ranging (or after the idle abort), so no
		// goroutine is left parked on a send.
		chunks := make(chan chunk, 8)
		done := make(chan struct{})
		go func() {
			defer close(chunks)
			for resp, err := range m.inner.GenerateContent(ictx, req, stream) {
				select {
				case chunks <- chunk{resp, err}:
				case <-done:
					return
				}
			}
		}()
		defer close(done)

		var timer *time.Timer
		if m.timeout > 0 {
			timer = time.NewTimer(m.timeout)
			defer timer.Stop()
		}
		var ticker *time.Ticker
		if heartbeat != nil && m.tick > 0 {
			ticker = time.NewTicker(m.tick)
			defer ticker.Stop()
		}

		// deliver hands one chunk to the consumer and rearms the timers.
		// It reports whether to keep watching: false when the stream has
		// ended (chunks closed) or the consumer stopped ranging.
		deliver := func(c chunk, ok bool) bool {
			if !ok {
				return false
			}
			if timer != nil {
				resetTimer(timer, m.timeout)
			}
			if ticker != nil {
				ticker.Reset(m.tick)
			}
			return yield(c.resp, c.err)
		}

		for {
			select {
			case c, ok := <-chunks:
				if !deliver(c, ok) {
					return
				}
			case <-tickerC(ticker):
				heartbeat()
			case <-timerC(timer):
				// The budget and a chunk can go ready together; select
				// picks between ready cases at random, so a live stream
				// would be torn down on a coin flip and its in-flight
				// chunk dropped. One non-blocking read settles it: a
				// chunk in the buffer means the stream is alive —
				// deliver it and keep watching.
				select {
				case c, ok := <-chunks:
					if !deliver(c, ok) {
						return
					}
					continue
				default:
				}
				// Unblocks the stream read; the pump then drains and exits
				// via done. Everything it still delivers is discarded — the
				// failure below replaces whatever a canceled request was
				// going to say, including a provider's "context canceled"
				// and the clean-looking canceledResponse its retry wrapper
				// yields on a dead context.
				cancel()
				_ = yield(nil, idleStreamError(m.timeout))
				return
			}
		}
	}
}

// timerC / tickerC return the signal channel of a possibly-nil timer/ticker.
// A nil channel in a select never fires, which is how the absent timer or
// ticker opts out.
func timerC(t *time.Timer) <-chan time.Time {
	if t == nil {
		return nil
	}
	return t.C
}

func tickerC(t *time.Ticker) <-chan time.Time {
	if t == nil {
		return nil
	}
	return t.C
}

// resetTimer restarts a timer between chunks. Called only from the watch
// loop, so Stop/Reset race with nothing.
func resetTimer(t *time.Timer, d time.Duration) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
	t.Reset(d)
}

// idleStreamError builds the failure a stalled stream is reported with. The
// wording is load-bearing: retry.IsTransient classifies "llm stream idle" as
// transient (see transientPatterns in internal/retry), which is what hands
// this to the retry budgets.
func idleStreamError(d time.Duration) error {
	return fmt.Errorf("llm stream idle: no stream data for %s", d)
}

// heartbeatIntervalFor picks the tick interval for a given idle timeout: a
// fraction of it (so a shorter timeout still gets ticks), capped at the
// default; the default itself when the timeout is off.
func heartbeatIntervalFor(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return DefaultStreamHeartbeatInterval
	}
	if tick := timeout / 3; tick > 0 && tick < DefaultStreamHeartbeatInterval {
		return tick
	}
	return DefaultStreamHeartbeatInterval
}

// heartbeatKey carries the keep-alive hook through the request context, the
// only thing that travels from a front-end down to the provider call — the
// same route retry.Notifier takes.
type heartbeatKey struct{}

// WithStreamHeartbeat returns a context that carries fn to every streaming
// model call run under it. The hook is called while a stream is being waited
// on; in --mode json it writes the keep-alive line that feeds the parent's
// inactivity watchdog. Callers without a stream to protect (the TUI, print
// mode) install nothing, and the wrapper then never calls anything.
func WithStreamHeartbeat(ctx context.Context, fn func()) context.Context {
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, heartbeatKey{}, fn)
}

func streamHeartbeatFromContext(ctx context.Context) func() {
	if ctx == nil {
		return nil
	}
	fn, _ := ctx.Value(heartbeatKey{}).(func())
	return fn
}
