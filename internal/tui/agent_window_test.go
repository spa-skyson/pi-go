package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// agentCardLines returns the gutter lines of a rendered subagent card — the
// output window itself, excluding the "● agent[...]" header.
func agentCardLines(t *testing.T, msg message, width int) []string {
	t.Helper()
	td := &ToolDisplayModel{Width: width}

	var out []string
	for _, line := range strings.Split(td.RenderToolMessage(msg), "\n") {
		if strings.Contains(line, "│ ") {
			out = append(out, line)
		}
	}
	return out
}

// The subagent output window is 8 lines: the pinned thought plus the newest
// tool rows.
func TestAgentOutputWindowIsEightLines(t *testing.T) {
	if maxAgentOutputLines != 8 {
		t.Fatalf("maxAgentOutputLines = %d, want 8", maxAgentOutputLines)
	}
}

// The bug from the screenshot: a subagent's final analysis arrives as ONE "text"
// event carrying thousands of characters. Capping the number of events caps
// nothing — that single event soft-wrapped into a screenful (74 lines,
// measured) and buried the chat. The collapsed card pins the latest thought to
// a single row, clipped with an ellipsis; the full text lives in the monitor's
// viewer and in the result summary.
func TestAgentOutputWindowCapsAHugeSingleEvent(t *testing.T) {
	flood := strings.Repeat(
		"Here is the analysis: the provider package resolves models by prefix. ", 80)

	msg := message{
		role: "tool", tool: "agent", agentType: "pi", agentTitle: "Analyze internal/subagent",
		agentEvents: []agentEv{{kind: "text", content: flood}},
	}

	lines := agentCardLines(t, msg, 100)

	// One thought row, however long the analysis was.
	if len(lines) != 1 {
		t.Fatalf("one huge event rendered %d gutter lines, want exactly 1", len(lines))
	}

	// Clipping without a mark would read as if that were all the agent said.
	if !strings.Contains(ansi.Strip(lines[0]), "...") {
		t.Error("the clipped thought carries no ellipsis marking the cut")
	}
}

// Many events must also fit the window, and the newest ones are the ones kept —
// the card is a live progress view, so the latest activity is what matters.
func TestAgentOutputWindowKeepsNewestAcrossManyEvents(t *testing.T) {
	var events []agentEv
	for i := range 40 {
		events = append(events, agentEv{kind: "tool_call", content: "step-" + string(rune('a'+i%26))})
	}
	events = append(events, agentEv{kind: "text", content: "NEWEST-OUTPUT-MARKER"})

	msg := message{
		role: "tool", tool: "agent", agentType: "pi",
		agentEvents: events,
	}

	rendered := (&ToolDisplayModel{Width: 100}).RenderToolMessage(msg)
	lines := agentCardLines(t, msg, 100)

	// The "... N earlier events" note is one of the gutter lines, so the window
	// itself is bounded by maxAgentOutputLines and the note sits above it.
	if len(lines) > maxAgentOutputLines+1 {
		t.Fatalf("card rendered %d gutter lines, want at most %d output lines plus one note",
			len(lines), maxAgentOutputLines)
	}
	if !strings.Contains(rendered, "NEWEST-OUTPUT-MARKER") {
		t.Error("newest event was dropped; the window must keep the latest activity")
	}
	if !strings.Contains(rendered, "earlier events") {
		t.Error("no note that earlier events were hidden")
	}
}

// A short stream is shown in full, with no truncation note.
func TestAgentOutputWindowLeavesShortStreamsAlone(t *testing.T) {
	msg := message{
		role: "tool", tool: "agent", agentType: "pi",
		agentEvents: []agentEv{
			{kind: "tool_call", content: "read server.go"},
			{kind: "tool_result", content: "ok"},
		},
	}

	rendered := (&ToolDisplayModel{Width: 100}).RenderToolMessage(msg)

	if strings.Contains(rendered, "earlier events") {
		t.Error("truncation note shown for a stream that fits")
	}
	if !strings.Contains(rendered, "read server.go") {
		t.Error("a short stream must render in full")
	}
}

// A subagent's thinking_delta events render with the 💭 marker, not a raw
// "thinking_delta:" label.
func TestAgentOutputWindowRendersThinkingDeltaWithIcon(t *testing.T) {
	msg := message{
		role: "tool", tool: "agent", agentType: "pi",
		agentEvents: []agentEv{{kind: "thinking_delta", content: "weighing the options"}},
	}

	rendered := (&ToolDisplayModel{Width: 100}).RenderToolMessage(msg)

	if !strings.Contains(rendered, "💭") {
		t.Errorf("thinking_delta rendered without the 💭 icon: %q", rendered)
	}
	if strings.Contains(rendered, "thinking_delta:") {
		t.Errorf("thinking_delta rendered with the raw kind label: %q", rendered)
	}
	if !strings.Contains(rendered, "weighing the options") {
		t.Errorf("thinking_delta content missing from card: %q", rendered)
	}
}

// Clipping must not split a multi-byte rune into a replacement character.
func TestTruncateRunesIsUTF8Safe(t *testing.T) {
	s := strings.Repeat("é", 50)
	got := truncateRunes(s, 10)

	if strings.Contains(got, "�") {
		t.Fatalf("truncateRunes split a rune: %q", got)
	}
	if n := len([]rune(got)); n != 10 {
		t.Errorf("got %d runes, want 10", n)
	}
}
