package tui

import (
	"context"
	"strings"
	"testing"
)

// Queue-during-turn: a prompt submitted while a turn runs is queued without
// canceling the running turn. It starts when the current turn completes.
//
// The design rule these pin is that queuing must not create a second turn racing
// the one in progress, and the running turn must not be interrupted.

func TestQueueWhileRunning_DoesNotCancelAndQueuesText(t *testing.T) {
	m := newTestModel(t)
	m.running = true
	m.agentCh = make(chan agentMsg, 4)
	ctx, cancel := context.WithCancel(context.Background())
	m.agentCancel = cancel

	updated, cmd, _ := m.handleInputSubmit(InputSubmitMsg{Text: "actually, do X instead"})
	m = updated.(*model)

	if cmd == nil {
		t.Fatal("queued prompt returned no command; the turn is still running, so nothing would start it")
	}
	if ctx.Err() != nil {
		t.Fatal("queuing canceled the running turn — it must be left running")
	}
	if !m.running {
		t.Fatal("queuing cleared running; the UI would look idle while the old turn unwinds")
	}
	if got := len(m.pendingPrompts); got != 1 {
		t.Fatalf("pending = %d, want 1", got)
	}
}

// When the running turn completes, the queued prompt starts.
func TestQueueWhileRunning_StartsOnDone(t *testing.T) {
	m := newTestModel(t)
	m.running = true
	m.agentCh = make(chan agentMsg, 4)

	if _, cmd := m.enqueuePrompt("queued while running", nil); cmd != nil {
		t.Fatal("queued prompt started while a turn was running")
	}

	// The running turn completes normally.
	close(m.agentCh)
	m.agentCh = nil
	_, cmd := m.Update(agentDoneMsg{})
	if cmd == nil {
		t.Fatal("the turn's done did not start the queued prompt")
	}
	if !m.running {
		t.Fatal("queued prompt did not start after done")
	}
	if got := len(m.pendingPrompts); got != 0 {
		t.Fatalf("pending = %d after start, want 0", got)
	}
	if got := m.chatModel.Messages[len(m.chatModel.Messages)-2].content; got != "queued while running" {
		t.Fatalf("queued prompt text = %q, want %q", got, "queued while running")
	}
}

// A full queue refuses the new prompt and leaves the running turn alone.
func TestQueueWhileRunning_FullQueueLeavesTurnRunning(t *testing.T) {
	m := newTestModel(t)
	m.running = true
	m.agentCh = make(chan agentMsg, 4)
	ctx, cancel := context.WithCancel(context.Background())
	m.agentCancel = cancel
	m.pendingPrompts = make([]queuedPrompt, maxPendingPrompts)

	updated, cmd, _ := m.handleInputSubmit(InputSubmitMsg{Text: "won't fit"})
	m = updated.(*model)

	if cmd != nil {
		t.Fatal("enqueue returned a command with a full queue")
	}
	if ctx.Err() != nil {
		t.Error("enqueue canceled the running turn with nowhere for the replacement to go")
	}
	if !m.running {
		t.Error("enqueue stopped the turn instead of refusing")
	}
	if m.flash != "Prompt queue full" {
		t.Errorf("flash = %q, want the queue-full notice", m.flash)
	}
}

// Multiple queued prompts pile up and start in order.
func TestQueueWhileRunning_MultipleQueueAndDrainInOrder(t *testing.T) {
	m := newTestModel(t)
	m.running = true
	m.agentCh = make(chan agentMsg, 4)

	if _, cmd := m.enqueuePrompt("first", nil); cmd != nil {
		t.Fatal("first queued prompt started while running")
	}
	if _, cmd := m.enqueuePrompt("second", nil); cmd != nil {
		t.Fatal("second queued prompt started while running")
	}
	if _, cmd := m.enqueuePrompt("third", nil); cmd != nil {
		t.Fatal("third queued prompt started while running")
	}
	if got := len(m.pendingPrompts); got != 3 {
		t.Fatalf("pending = %d, want 3", got)
	}

	// First done starts the first queued prompt.
	close(m.agentCh)
	m.agentCh = nil
	m.Update(agentDoneMsg{})

	if !m.running {
		t.Fatal("first queued prompt did not start after done")
	}
	if got := len(m.pendingPrompts); got != 2 {
		t.Fatalf("pending = %d after first drain, want 2", got)
	}
	firstContent := m.chatModel.Messages[len(m.chatModel.Messages)-2].content
	if firstContent != "first" {
		t.Fatalf("first queued prompt text = %q, want %q", firstContent, "first")
	}
}

// Esc still cancels and starts the next queued prompt.
func TestQueue_EscCancelsAndStartsQueuedPrompt(t *testing.T) {
	m := newTestModel(t)
	m.running = true
	m.agentCh = make(chan agentMsg, 4)

	if _, cmd := m.enqueuePrompt("queued while running", nil); cmd != nil {
		t.Fatal("queued prompt started while a turn was running")
	}
	m.cancelAgent()

	if len(m.pendingPrompts) != 0 {
		t.Error("pending prompt stranded after Esc: nothing will ever start it")
	}
	if !m.running {
		t.Error("queued prompt did not start after Esc")
	}
}

// Esc-canceled turn still reports its error (no steer flag to suppress it).
func TestQueue_EscCancelStillReportsFailure(t *testing.T) {
	m := newTestModel(t)
	m.running = true
	m.agentCh = make(chan agentMsg, 4)
	m.lastPrompt = "do something long"

	m.Update(agentDoneMsg{err: context.Canceled})

	if !m.lastPromptFailed {
		t.Error("an Esc cancel stopped offering the retry it used to offer")
	}
	// Verify the cancellation error shows in the transcript.
	found := false
	for _, msg := range m.chatModel.Messages {
		if msg.role == "assistant" && strings.Contains(msg.content, "context canceled") {
			found = true
		}
	}
	if !found {
		t.Error("Esc cancellation did not print the error in the transcript")
	}
}

// handleInputSubmit with running queues the prompt and flashes "Queued".
func TestQueueWhileRunning_ShowsQueuedFlash(t *testing.T) {
	m := newTestModel(t)
	m.running = true
	m.agentCh = make(chan agentMsg, 4)

	updated, _, _ := m.handleInputSubmit(InputSubmitMsg{Text: "do something else"})
	m = updated.(*model)
	if m.flash != "Queued" {
		t.Errorf("flash = %q, want %q", m.flash, "Queued")
	}
}
