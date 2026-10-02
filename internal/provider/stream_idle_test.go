package provider

import (
	"context"
	"iter"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/spa-skyson/pi-rate/internal/retry"
)

// stallModel is a stand-in for a provider model: it yields n chunks spaced
// delay apart, honoring context cancellation the way a real HTTP stream does
// (the read unblocks when the context dies). canceled records that the watch
// actually aborted the request rather than just giving up on it.
type stallModel struct {
	chunks   int
	delay    time.Duration
	canceled atomic.Bool
}

func (m *stallModel) Name() string { return "stall" }

func (m *stallModel) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		for i := 0; i < m.chunks; i++ {
			select {
			case <-ctx.Done():
				m.canceled.Store(true)
				yield(nil, ctx.Err())
				return
			case <-time.After(m.delay):
			}
			if !yield(&model.LLMResponse{
				Partial: true,
				Content: &genai.Content{Role: string(genai.RoleModel), Parts: []*genai.Part{{Text: "x"}}},
			}, nil) {
				return
			}
		}
	}
}

// collect drains one GenerateContent call, separating responses from errors.
func drainWatch(seq iter.Seq2[*model.LLMResponse, error]) ([]*model.LLMResponse, []error) {
	var (
		resps [](*model.LLMResponse)
		errs  []error
	)
	for resp, err := range seq {
		if err != nil {
			errs = append(errs, err)
			continue
		}
		resps = append(resps, resp)
	}
	return resps, errs
}

// The scenario from issue #37, scaled 1s:1ms — chunks 120ms apart against a
// 60ms idle budget must abort with the idle failure, and the abort must
// cancel the underlying request (that is what frees the parked stream read).
func TestStreamIdleAbortsSilentStream(t *testing.T) {
	inner := &stallModel{chunks: 3, delay: 120 * time.Millisecond}
	watched := idleStreamModel{inner: inner, timeout: 60 * time.Millisecond, tick: 20 * time.Millisecond}

	start := time.Now()
	resps, errs := drainWatch(watched.GenerateContent(context.Background(), nil, true))
	elapsed := time.Since(start)

	if len(resps) != 0 {
		t.Errorf("got %d responses before the idle abort, want 0", len(resps))
	}
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want exactly the idle failure: %v", len(errs), errs)
	}
	if !strings.Contains(errs[0].Error(), "llm stream idle") {
		t.Errorf("error %q does not name the idle abort", errs[0])
	}
	// The wording is load-bearing: it is what retry.IsTransient classifies.
	if !retry.IsTransient(errs[0]) {
		t.Errorf("idle failure %q is not transient; no retry budget would pick it up", errs[0])
	}
	if retry.IsTerminal(errs[0]) {
		t.Errorf("idle failure %q classified terminal", errs[0])
	}
	// The watch cancels the request context and returns at once; the pump
	// goroutine observes the cancellation on the scheduler's schedule, so
	// wait for the flag instead of asserting on state another goroutine may
	// not have set yet (this race was a real flake under -count and load).
	deadline := time.Now().Add(2 * time.Second)
	for !inner.canceled.Load() && time.Now().Before(deadline) {
		time.Sleep(500 * time.Microsecond)
	}
	if !inner.canceled.Load() {
		t.Error("the stalled request context was not canceled")
	}
	if elapsed >= 120*time.Millisecond {
		t.Errorf("abort took %v — the first chunk's 120ms delay elapsed, so this watched nothing", elapsed)
	}
}

// A stream that keeps producing must pass through untouched: same chunks,
// reset budget.
func TestStreamIdleHealthyStreamPasses(t *testing.T) {
	inner := &stallModel{chunks: 3, delay: 10 * time.Millisecond}
	watched := idleStreamModel{inner: inner, timeout: 1 * time.Second, tick: 50 * time.Millisecond}

	resps, errs := drainWatch(watched.GenerateContent(context.Background(), nil, true))
	if len(errs) != 0 {
		t.Fatalf("healthy stream produced errors: %v", errs)
	}
	if len(resps) != 3 {
		t.Errorf("got %d responses, want 3", len(resps))
	}
}

// timeout 0 means the abort is off — a stall that outlives any budget is
// delivered as before. This is the config escape hatch.
func TestStreamIdleDisabledPassesThrough(t *testing.T) {
	inner := &stallModel{chunks: 1, delay: 150 * time.Millisecond}
	watched := idleStreamModel{inner: inner, timeout: 0, tick: 0}

	resps, errs := drainWatch(watched.GenerateContent(context.Background(), nil, true))
	if len(errs) != 0 {
		t.Fatalf("disabled watch produced errors: %v", errs)
	}
	if len(resps) != 1 {
		t.Errorf("got %d responses, want 1", len(resps))
	}
}

// The heartbeat alone (idle abort off) must tick while the stream is silent,
// and stop when it is not. Ticks are what become keep-alive lines in the
// child, and keep-alive lines are what keep the parent's watchdog quiet.
func TestStreamHeartbeatTicksWhileWaiting(t *testing.T) {
	var ticks atomic.Int64
	ctx := WithStreamHeartbeat(context.Background(), func() { ticks.Add(1) })

	inner := &stallModel{chunks: 1, delay: 90 * time.Millisecond}
	watched := idleStreamModel{inner: inner, timeout: 0, tick: 25 * time.Millisecond}

	resps, errs := drainWatch(watched.GenerateContent(ctx, nil, true))
	if len(errs) != 0 || len(resps) != 1 {
		t.Fatalf("heartbeat watch disturbed delivery: %d responses, %v", len(resps), errs)
	}
	if got := ticks.Load(); got < 2 {
		t.Errorf("got %d keep-alive ticks during a 90ms silent wait at a 25ms interval, want at least 2", got)
	}
}

// Chunks must restart the tick countdown: a stream flowing faster than the
// interval never ticks, so keep-alive lines only cover real silences.
func TestStreamHeartbeatResetsOnChunks(t *testing.T) {
	var ticks atomic.Int64
	ctx := WithStreamHeartbeat(context.Background(), func() { ticks.Add(1) })

	// 5 chunks 30ms apart (150ms total) against a 45ms interval: without a
	// reset the counter would fire ~3 times.
	inner := &stallModel{chunks: 5, delay: 30 * time.Millisecond}
	watched := idleStreamModel{inner: inner, timeout: 0, tick: 45 * time.Millisecond}

	drainWatch(watched.GenerateContent(ctx, nil, true))
	if got := ticks.Load(); got != 0 {
		t.Errorf("got %d ticks while chunks flowed every 30ms against a 45ms interval, want 0 (reset broken)", got)
	}
}

// Heartbeat and idle abort work together: ticks cover the silence until the
// budget fires.
func TestStreamHeartbeatAndIdleTogether(t *testing.T) {
	var ticks atomic.Int64
	ctx := WithStreamHeartbeat(context.Background(), func() { ticks.Add(1) })

	inner := &stallModel{chunks: 3, delay: 120 * time.Millisecond}
	watched := idleStreamModel{inner: inner, timeout: 60 * time.Millisecond, tick: 20 * time.Millisecond}

	_, errs := drainWatch(watched.GenerateContent(ctx, nil, true))
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "llm stream idle") {
		t.Fatalf("want exactly the idle failure, got %v", errs)
	}
	if got := ticks.Load(); got < 2 {
		t.Errorf("got %d ticks before the abort, want at least 2 (60ms budget, 20ms interval)", got)
	}
}

func TestHeartbeatIntervalFor(t *testing.T) {
	tests := []struct {
		timeout time.Duration
		want    time.Duration
	}{
		{0, DefaultStreamHeartbeatInterval},
		{-1, DefaultStreamHeartbeatInterval},
		{90 * time.Second, DefaultStreamHeartbeatInterval},
		{10 * time.Minute, DefaultStreamHeartbeatInterval}, // capped, not /3
		{9 * time.Second, 3 * time.Second},
		// A tiny budget still gets a positive interval: NewTicker panics on 0.
		{3 * time.Millisecond, time.Millisecond},
	}
	for _, tt := range tests {
		if got := heartbeatIntervalFor(tt.timeout); got != tt.want {
			t.Errorf("heartbeatIntervalFor(%v) = %v, want %v", tt.timeout, got, tt.want)
		}
	}
}

// Consumer stops ranging early: the watch must return without leaking the
// pump (it exits via done) — verified as "no deadlock", the leak's visible
// symptom at this layer.
func TestStreamIdleConsumerStopEndsCleanly(t *testing.T) {
	inner := &stallModel{chunks: 20, delay: 5 * time.Millisecond}
	watched := idleStreamModel{inner: inner, timeout: 1 * time.Second, tick: 0}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for resp, err := range watched.GenerateContent(context.Background(), nil, true) {
			if err != nil || resp == nil {
				return
			}
			return // stop after the first chunk
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("watch did not return after the consumer stopped ranging")
	}
}
