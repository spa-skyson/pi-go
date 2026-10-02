package tui

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// strip strips ANSI from every line of a rendered card for assertion purposes.
func stripLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = ansi.Strip(l)
	}
	return out
}

// The collapsed card the issue asked for: the latest thought pinned on the
// first row, then one row per tool call with its own result folded in — read
// shows what came back, bash the output's first line, write the path and size,
// a failed result turns ✗, and a call still awaiting its result shows bare.
func TestAgentOutputWindowShowsThoughtAndToolResults(t *testing.T) {
	events := []agentEv{
		{kind: "text", content: "Scanning the auth flow"},
		{kind: "tool_call", content: "read"},
		{kind: "tool_result", content: `{"content":"package main","total_lines":120}`},
		{kind: "tool_call", content: "bash"},
		{kind: "tool_result", content: `{"exit_code":0,"stdout":"ok\n","stderr":""}`},
		{kind: "tool_call", content: "write"},
		{kind: "tool_result", content: `{"path":"a.go","bytes_written":412}`},
		{kind: "tool_call", content: "grep"},
		{kind: "tool_result", content: `{"error":"boom"}`},
		{kind: "tool_call", content: "bash2"},
		{kind: "text", content: "Now summarizing the findings"},
	}
	msg := message{role: "tool", tool: "agent", agentType: "pi", agentEvents: events}

	lines := stripLines(agentCardLines(t, msg, 120))

	// The note (when present) renders above the window; drop it for indexing.
	var rows []string
	for _, l := range lines {
		if !strings.Contains(l, "earlier events") {
			rows = append(rows, l)
		}
	}
	joined := strings.Join(lines, "\n")

	if len(rows) > maxAgentOutputLines {
		t.Fatalf("got %d window rows, want at most %d", len(rows), maxAgentOutputLines)
	}
	if !strings.Contains(rows[0], "» Now summarizing the findings") {
		t.Errorf("the latest thought must be pinned first, got %q", rows[0])
	}
	if strings.Contains(joined, "Scanning the auth flow") {
		t.Error("an older thought must be superseded by the pinned latest one")
	}
	for _, want := range []string{
		"⚙ read ✓ package main",
		"⚙ bash ✓ ok",
		"⚙ write ✓ a.go (412 bytes)",
		"⚙ grep ✗ error: boom",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("tool row %q missing from card:\n%s", want, joined)
		}
	}
	for _, l := range rows {
		if strings.Contains(l, "⚙ bash2") && strings.Contains(l, "✓") {
			t.Errorf("a pending call must show no result mark, got %q", l)
		}
	}
}

// The tool trail keeps the newest rows: with more tools than the window holds,
// the pinned thought survives and the oldest tools fold into the note.
func TestAgentOutputWindowKeepsNewestToolsWithPinnedThought(t *testing.T) {
	var events []agentEv
	for i := range 20 {
		events = append(events, agentEv{kind: "tool_call", content: "step-" + string(rune('a'+i))})
	}
	events = append(events, agentEv{kind: "text", content: "the conclusion"})

	msg := message{role: "tool", tool: "agent", agentType: "pi", agentEvents: events}
	lines := stripLines(agentCardLines(t, msg, 120))

	if len(lines) != maxAgentOutputLines+1 { // note line + window
		t.Fatalf("got %d gutter lines, want %d plus the note", len(lines), maxAgentOutputLines)
	}
	if !strings.Contains(lines[0], "... 13 earlier events") {
		t.Errorf("the note must sit above the window, got %q", lines[0])
	}
	if !strings.Contains(lines[1], "» the conclusion") {
		t.Errorf("the thought must stay pinned above the tool tail, got %q", lines[1])
	}
	if !strings.Contains(lines[2], "⚙ step-n") { // 20-7: the newest 7 tools, n..t
		t.Errorf("expected the newest 7 tools after the thought, got %q", lines[2])
	}
}

// A running card's header carries the live clock; it grows as the seconds
// pass and sits between the label and the title. Unknown agent types collapse
// through agentBracketLabel, so the label here is "pi".
func TestAgentCardHeaderShowsRunningClock(t *testing.T) {
	td := ToolDisplayModel{Width: 120}
	msg := message{
		role: "tool", tool: "agent", agentType: "golang-pro",
		agentTitle:   "fix the race",
		agentStarted: time.Now().Add(-86 * time.Second),
	}

	got := ansi.Strip(td.agentCardHeader(msg, paletteOrDark(td.Palette)))
	if !strings.Contains(got, "agent[pi] · running 1m26s") {
		t.Errorf("header = %q, want the live clock", got)
	}

	// A second later the clock reads a second more — that is the whole point
	// of folding the second into the render key.
	msg.agentStarted = time.Now().Add(-87 * time.Second)
	got = ansi.Strip(td.agentCardHeader(msg, paletteOrDark(td.Palette)))
	if !strings.Contains(got, "· running 1m27s") {
		t.Errorf("header = %q, want the clock to grow across the tick", got)
	}
}

// The done stamp freezes the clock at the final duration: no "running", no
// growth, no blinking — the card is finished.
func TestAgentCardHeaderFreezesClockOnDone(t *testing.T) {
	td := ToolDisplayModel{Width: 120}
	msg := message{
		role: "tool", tool: "agent",
		agentStarted: time.Now().Add(-90 * time.Second),
		agentEnded:   time.Now().Add(-30 * time.Second),
	}

	got := ansi.Strip(td.agentCardHeader(msg, paletteOrDark(td.Palette)))
	if !strings.Contains(got, "· 1m0s") {
		t.Errorf("header = %q, want the frozen final duration", got)
	}
	if strings.Contains(got, "running") {
		t.Errorf("a finished card must not say running: %q", got)
	}
}

// No spawn stamp, no clock: restored cards and stamp-less events render the
// plain header.
func TestAgentCardHeaderNoClockWithoutStamps(t *testing.T) {
	td := ToolDisplayModel{Width: 120}
	got := ansi.Strip(td.agentCardHeader(message{role: "tool", tool: "agent"}, paletteOrDark(td.Palette)))
	if strings.Contains(got, "·") {
		t.Errorf("a stamp-less card must have no clock, got %q", got)
	}
}

// The card header names the model between the label and the clock — dim, like
// the clock, so the agent name stays the loudest thing in the row. After the
// done stamp the same layout carries the frozen duration. Without a model the
// header is unchanged.
func TestAgentCardHeaderShowsModelBetweenLabelAndClock(t *testing.T) {
	td := ToolDisplayModel{Width: 120}
	pal := paletteOrDark(td.Palette)
	msg := message{
		role: "tool", tool: "agent", agentType: "golang-pro",
		agentTitle:   "fix the race",
		agentModel:   "glm-5.3-flash",
		agentStarted: time.Now().Add(-86 * time.Second),
	}

	raw := td.agentCardHeader(msg, pal)
	got := ansi.Strip(raw)
	if !strings.Contains(got, "agent[pi] · glm-5.3-flash · running 1m26s") {
		t.Errorf("header = %q, want the model between label and clock", got)
	}
	wantDim := lipgloss.NewStyle().Foreground(pal.Faint).Render("· glm-5.3-flash")
	if !strings.Contains(raw, wantDim) {
		t.Errorf("model must render dim; missing %q in %q", wantDim, raw)
	}

	// After done: model, then the frozen final duration.
	msg.agentEnded = time.Now().Add(-26 * time.Second)
	got = ansi.Strip(td.agentCardHeader(msg, pal))
	if !strings.Contains(got, "glm-5.3-flash · 1m0s") {
		t.Errorf("done header = %q, want model then frozen duration", got)
	}

	// No model: exactly the old header, no stray separator.
	msg.agentModel = ""
	got = ansi.Strip(td.agentCardHeader(msg, pal))
	if strings.Contains(got, "glm-5.3-flash") {
		t.Errorf("header = %q, model must be omitted when unknown", got)
	}
	if !strings.Contains(got, "agent[pi] · 1m0s") {
		t.Errorf("header without model = %q, want the plain pre-model form", got)
	}
}

// agentRunning stops at the result even when the done event was dropped by the
// event channel's non-blocking send — the clock and the tick must not run on.
func TestAgentRunningStopsOnResultWithoutDone(t *testing.T) {
	msg := message{role: "tool", tool: "agent", agentStarted: time.Now()}
	if !msg.agentRunning() {
		t.Fatal("a stamped card without result is running")
	}
	msg.content = `{"result":"done"}`
	if msg.agentRunning() {
		t.Fatal("a card whose result landed is not running, even without a done stamp")
	}
}

// The render key carries the clock's second only for a running card: it
// changes across a second boundary while running, and is wall-clock-free once
// finished — a finished card's cache never invalidates again.
func TestAgentCardRenderKeyTicksOnlyWhileRunning(t *testing.T) {
	key := func(m message) uint64 {
		return m.renderKey(100, false, false, false, 0, false, "")
	}

	running := message{role: "tool", tool: "agent", agentStarted: time.Now().Add(-50 * time.Millisecond)}
	k1 := key(running)
	running.agentStarted = time.Now().Add(-1500 * time.Millisecond)
	if k2 := key(running); k1 == k2 {
		t.Error("a running card's key must change across a second boundary, or the clock freezes in the cache")
	}

	done := message{
		role: "tool", tool: "agent",
		agentStarted: time.Now().Add(-90 * time.Second),
		agentEnded:   time.Now().Add(-30 * time.Second),
		content:      "result",
	}
	if k1, k2 := key(done), key(done); k1 != k2 {
		t.Error("a finished card's key must be wall-clock stable")
	}
}

// The card tick arms only when a card is live outside a running turn: while
// the turn runs the sea tick repaints, and once nothing is live nothing ticks.
func TestArmCardTickOnlyForLiveCardsOutsideTurn(t *testing.T) {
	m := &model{chatModel: ChatModel{Messages: []message{
		{role: "tool", tool: "agent", agentStarted: time.Now()},
	}}}

	if m.running {
		t.Fatal("test model must start idle")
	}
	if m.armCardTick() == nil {
		t.Error("a live card with the turn over must arm the card tick")
	}

	m.running = true
	if m.armCardTick() != nil {
		t.Error("while the turn runs the sea tick drives frames; no extra tick")
	}

	m.running = false
	m.chatModel.Messages[0].agentEnded = time.Now()
	if m.armCardTick() != nil {
		t.Error("no tick once the card is done")
	}
}

// The status bar's anchor field is silent at zero and counts otherwise.
func TestStatusBarShowsSubagentAnchor(t *testing.T) {
	s := StatusModel{}
	out := s.Render(StatusRenderInput{Palette: paletteOrDark(Palette{}), RunningAgents: 2})
	if !strings.Contains(ansi.Strip(out), "⚓ 2") {
		t.Errorf("status bar = %q, want the subagent counter", ansi.Strip(out))
	}
	out = s.Render(StatusRenderInput{Palette: paletteOrDark(Palette{})})
	if strings.Contains(ansi.Strip(out), "⚓") {
		t.Errorf("status bar = %q, want no anchor with nothing running", ansi.Strip(out))
	}
}

// runningSubagentCount reads the same merged view the monitor shows.
func TestRunningSubagentCount(t *testing.T) {
	m := monitorTestModel(t)
	if got := m.runningSubagentCount(); got != 1 {
		t.Errorf("runningSubagentCount = %d, want 1 (the explore agent)", got)
	}
}
