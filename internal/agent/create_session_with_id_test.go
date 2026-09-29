package agent

import (
	"context"
	"testing"
)

// A session created under a fixed ID must report exactly that ID: the
// ${SESSION_ID} header is baked into the LLM client before the session
// exists, so the creation call is what keeps the two from drifting apart.
func TestCreateSessionWithID(t *testing.T) {
	a, err := New(Config{Model: &mockLLM{name: "t", response: "ok"}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	sid, _, err := a.CreateSessionWithID(context.Background(), "fixed-session-id")
	if err != nil {
		t.Fatalf("CreateSessionWithID: %v", err)
	}
	if sid != "fixed-session-id" {
		t.Errorf("sid = %q, want %q", sid, "fixed-session-id")
	}

	// Empty ID keeps the generated-ID behavior of CreateSession.
	gen, _, err := a.CreateSessionWithID(context.Background(), "")
	if err != nil {
		t.Fatalf("CreateSessionWithID(empty): %v", err)
	}
	if gen == "" || gen == "fixed-session-id" {
		t.Errorf("empty id did not generate a fresh one: %q", gen)
	}
}
