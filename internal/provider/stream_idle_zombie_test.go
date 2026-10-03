package provider

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// zombieSSERequest is a complete Anthropic messages SSE turn: the prelude from
// stream_cancel_paths_test.go plus the closing events, so a streamed request
// can run to TurnComplete against the fixture server. The body carries no
// Content-Length and no chunked framing, so its end is signaled by the
// connection's write side closing — EOF terminates the SSE stream cleanly.
const zombieSSERequest = antSSEPrelude + `event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}

event: message_stop
data: {"type":"message_stop"}

`

// hijacker takes the fixture server over at the socket level: each hijacked
// connection is served by exactly one handler invocation, so a handler count
// doubles as a connection count — keep-alive reuse never re-enters ServeHTTP.
func hijacker(w http.ResponseWriter) (net.Conn, error) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		return nil, fmt.Errorf("server does not support hijacking")
	}
	conn, _, err := hj.Hijack()
	return conn, err
}

// holdSilent writes a streaming response head plus prelude and then goes
// silent — the zombie of issue #45: a gateway that accepted the request and
// stopped talking. It unblocks only when the client tears the connection down
// (the idle abort cancels the request context, and closing the in-flight
// socket is what the side read reports) or on a safety timer, so a broken
// abort fails the test instead of hanging it.
func holdSilent(t *testing.T, conn net.Conn, prelude string) {
	t.Helper()
	fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\n\r\n%s", prelude)
	gone := make(chan struct{})
	go func() {
		defer close(gone)
		var sink [64]byte
		for {
			if _, err := conn.Read(sink[:]); err != nil {
				return
			}
		}
	}()
	select {
	case <-gone:
	case <-time.After(5 * time.Second):
	}
	conn.Close()
}

// serveComplete writes a full SSE turn and half-closes: the client sees EOF
// after the last event and finishes the stream successfully.
func serveComplete(conn net.Conn, body string) {
	fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\n\r\n%s", body)
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.CloseWrite()
	}
	conn.Close()
}

// TestStreamIdleAbortDropsZombieConnectionBeforeRetry is the incident from
// issue #45, end to end on a real HTTP client: the gateway (here: httptest)
// goes silent mid-stream, the idleStreamModel watch aborts with "llm stream
// idle" (classified transient — this is what a retry budget consumes), and
// closeIdle drops the pooled socket the dead stream was parked on, so the
// retry dials a fresh connection.
func TestStreamIdleAbortDropsZombieConnectionBeforeRetry(t *testing.T) {
	var conns atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := hijacker(w)
		if err != nil {
			return
		}
		defer conn.Close()
		switch n := conns.Add(1); n {
		case 1:
			holdSilent(t, conn, antSSEPrelude) // the zombie
		default:
			serveComplete(conn, zombieSSERequest) // the retry, answered
		}
	}))
	t.Cleanup(srv.Close)

	opts := &LLMOptions{InsecureSkipTLS: true}
	m, err := NewAnthropic(context.Background(), "claude-sonnet-4-6", "sk-test", srv.URL, "none", opts)
	if err != nil {
		t.Fatalf("NewAnthropic: %v", err)
	}
	// The clone branch ran for InsecureSkipTLS, so NewAnthropic must have
	// captured the pool closer on opts and NewLLM must have threaded it to
	// the watch — this assert is what makes the test fail if the plumbing
	// between BuildTransport and idleStreamModel is ever dropped.
	if opts.closeIdle == nil {
		t.Fatal("NewAnthropic left opts.closeIdle unset; the idle watch would have nothing to close")
	}
	watched := idleStreamModel{inner: m, timeout: 250 * time.Millisecond, tick: 0, closeIdle: opts.closeIdle}

	req := &model.LLMRequest{Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "Hi"}}}}}

	// Attempt 1: the zombie. Must yield exactly the idle failure, and the
	// failure must close the pool before the retry runs.
	_, errs := drainWatch(watched.GenerateContent(context.Background(), req, true))
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "llm stream idle") {
		t.Fatalf("attempt 1: want exactly the idle failure, got %v", errs)
	}

	// Attempt 2: the retry. With the zombie socket dropped from the pool,
	// this must dial a new connection — and complete normally.
	done := make(chan struct{})
	go func() {
		defer close(done)
		drainWatch(watched.GenerateContent(context.Background(), req, true))
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("retry after the idle abort did not complete: the pool handed the dead connection back")
	}
	if got := conns.Load(); got != 2 {
		t.Errorf("server saw %d connections, want exactly 2 (original + retry on a fresh socket)", got)
	}
}

// The same scenario without a captured transport closer: behavior must be
// unchanged from before issue #45 — the same idle failure, no panic on a nil
// closeIdle. (Whether the pool then reuses a socket is the transport's call;
// that is exactly the pre-fix status quo this package leaves alone.)
func TestStreamIdleAbortNilCloseIdleIsSafe(t *testing.T) {
	var conns atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conns.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)

	m, err := NewAnthropic(context.Background(), "claude-sonnet-4-6", "sk-test", srv.URL, "none", nil)
	if err != nil {
		t.Fatalf("NewAnthropic: %v", err)
	}
	watched := idleStreamModel{inner: m, timeout: 150 * time.Millisecond, tick: 0}

	req := &model.LLMRequest{Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "Hi"}}}}}
	_, errs := drainWatch(watched.GenerateContent(context.Background(), req, true))
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "llm stream idle") {
		t.Fatalf("want exactly the idle failure with no closeIdle wired, got %v", errs)
	}
}

// TestCloseIdleConnectionsClearsClientPool isolates what the forwarding is
// for, at the http.Client level: after an idle socket has settled in the
// pool, CloseIdleConnections — reached through a wrapper stack of the same
// shape BuildTransport assembles (header injection, trace, rate limiting) —
// evicts it, so the next request dials fresh. Without the close, the same
// request reuses the pooled socket and the dial count does not move.
func TestCloseIdleConnectionsClearsClientPool(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("mode") == "silent" {
			// A zombie endpoint: accept, respond with a head, never speak.
			w.WriteHeader(http.StatusOK)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			<-r.Context().Done()
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(srv.Close)

	var dials atomic.Int64
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		dials.Add(1)
		var d net.Dialer
		return d.DialContext(ctx, network, addr)
	}
	client := &http.Client{Transport: &headerTransport{
		base: &traceTransport{
			base: &rateLimitShim{Base: tr},
		},
		headers: map[string]string{"X-Test": "1"},
	}}

	// Park one request on the silent endpoint and let another complete, so
	// the pool holds one idle socket.
	silentDone := make(chan struct{})
	go func() {
		defer close(silentDone)
		resp, err := client.Get(srv.URL + "/?mode=silent")
		if err == nil {
			_ = resp.Body.Close()
		}
	}()
	resp, err := client.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	_ = resp.Body.Close()
	if got := dials.Load(); got != 2 {
		t.Fatalf("got %d dials before the close, want 2 (silent + ok)", got)
	}

	client.CloseIdleConnections()
	<-silentDone

	// The pool must be empty now: the next request dials a third connection.
	resp, err = client.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET after close: %v", err)
	}
	_ = resp.Body.Close()
	if got := dials.Load(); got != 3 {
		t.Errorf("got %d dials after CloseIdleConnections, want 3 — the pool still handed back a socket", got)
	}

	// Control: a second close is harmless.
	client.CloseIdleConnections()
}

// rateLimitShim is a minimal RoundTripper wrapper standing in for
// ratelimit.Transport (which lives in another package): it exercises the same
// type-assert-and-forward CloseIdleConnections pattern the real transport
// implements, so the client-level test covers a three-layer stack.
type rateLimitShim struct{ Base http.RoundTripper }

func (t *rateLimitShim) RoundTrip(req *http.Request) (*http.Response, error) {
	return t.Base.RoundTrip(req)
}

func (t *rateLimitShim) CloseIdleConnections() {
	if ci, ok := t.Base.(interface{ CloseIdleConnections() }); ok {
		ci.CloseIdleConnections()
	}
}
