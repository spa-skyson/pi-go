package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/dimetron/pi-go/internal/subagent"
)

// monitorTestModel builds a model for the subagent monitor tests: two agent
// cards in the transcript (one live, one finished) and orchestrator statuses
// that overlap them — the running one stitched by agentID, plus statuses with
// no card (timeout) and a card with no status (done-5555).
func monitorTestModel(t *testing.T) *model {
	t.Helper()
	now := time.Now()
	// A stream comfortably taller than any test viewport, so scrolling has
	// somewhere to go: renderable events beyond the card's 3-line window.
	events := []agentEv{
		{kind: "spawn", content: "explore"},
		{kind: "tool_call", content: "grep Authori"},
		{kind: "tool_result", content: `{"matches":[]}`},
		{kind: "text", content: "the middleware lives in"},
		{kind: "text", content: "internal/auth/middleware.go"},
		{kind: "message_start", content: "noise"},
	}
	for i := range 40 {
		events = append(events, agentEv{kind: "text", content: fmt.Sprintf("streamed line %d of the scan", i)})
	}
	return &model{
		ctx:         context.Background(),
		width:       120,
		height:      40,
		inputModel:  NewInputModel(nil, nil, nil, ""),
		statusModel: StatusModel{},
		chatModel: ChatModel{Messages: []message{
			{
				role: "tool", tool: "agent",
				agentID:      "explore-1727000000000000000",
				agentType:    "explore",
				agentTitle:   "scan the auth flow",
				agentEvents:  events,
				pipelineID:   "p1",
				pipelineMode: "chain",
				pipelineStep: 2, pipelineTotal: 3,
			},
			{
				role: "tool", tool: "subagent",
				agentID:     "done-5555",
				agentType:   "task",
				agentTitle:  "write the missing test",
				agentEvents: []agentEv{{kind: "text", content: "done"}},
				content:     `{"result":"ok"}`,
			},
		}},
		cfg: Config{
			SubagentStatuses: func() []subagent.AgentStatus {
				return []subagent.AgentStatus{
					{AgentID: "done-5555", Type: "task", Status: "completed", Prompt: "write the missing test", Duration: "2m"},
					{AgentID: "explore-1727000000000000000", Type: "explore", Status: "running", Prompt: "scan the auth flow", StartedAt: now.Add(-90 * time.Second)},
					{AgentID: "plan-4444", Type: "plan", Status: "timeout", Prompt: "plan the migration"},
				}
			},
		},
	}
}

// selectRow moves the monitor's selection to the row with the given agentID.
func selectRow(t *testing.T, m *model, agentID string) {
	t.Helper()
	for i, it := range m.searchPopup.filtered {
		if it.ID == agentID {
			m.searchPopup.selected = i
			return
		}
	}
	t.Fatalf("row %q is not in the monitor", agentID)
}

// viewportBlock is a message-area block of exactly the height the View path
// hands the viewer overlay, so render windows and scroll bounds agree.
func viewportBlock(m *model) string {
	return strings.Repeat("x\n", max(1, m.messageViewportHeight())-1) + "x"
}

// pressKey routes a full tea.Key (chords included) through the overlay chain.
func pressKey(t *testing.T, m *model, key tea.Key) *model {
	t.Helper()
	next, _ := m.handleKey(tea.KeyPressMsg(key))
	mm, ok := next.(*model)
	if !ok {
		t.Fatalf("handleKey returned %T, want *model", next)
	}
	return mm
}

func monitorItemByID(m *model, id string) (SearchItem, bool) {
	if m.searchPopup == nil {
		return SearchItem{}, false
	}
	for _, it := range m.searchPopup.filtered {
		if it.ID == id {
			return it, true
		}
	}
	return SearchItem{}, false
}

// /subagents opens the monitor popup with both sources stitched by agentID:
// the running card carries its status; the status-less card is done by its
// result; the card-less status still shows, without a stream.
func TestSubagentsCommandOpensMonitor(t *testing.T) {
	m := monitorTestModel(t)

	m = submit(t, m, "/subagents")

	if m.searchPopup == nil || m.searchPopup.mode != searchModeSubagents {
		t.Fatalf("/subagents did not open the monitor: %+v", m.searchPopup)
	}
	if got := len(m.searchPopup.filtered); got != 3 {
		t.Fatalf("monitor shows %d rows, want 3 (explore, done-5555, plan-4444)", got)
	}

	explore, ok := monitorItemByID(m, "explore-1727000000000000000")
	if !ok {
		t.Fatal("the running agent is missing from the monitor")
	}
	if explore.Text != "scan the auth flow" {
		t.Errorf("running row title = %q, want the card's stored title", explore.Text)
	}
	for _, want := range []string{"●", "running", "explore", "1m", "chain 2/3"} {
		if !strings.Contains(explore.Description, want) {
			t.Errorf("running row description %q missing %q", explore.Description, want)
		}
	}

	done, ok := monitorItemByID(m, "done-5555")
	if !ok {
		t.Fatal("the status-less card is missing from the monitor")
	}
	if !strings.Contains(done.Description, "✓ completed") {
		t.Errorf("card without a status must read done by its result, got %q", done.Description)
	}
	if done.Description != "✓ completed · task · 2m" {
		t.Errorf("done row description = %q", done.Description)
	}

	plan, ok := monitorItemByID(m, "plan-4444")
	if !ok {
		t.Fatal("the card-less status is missing from the monitor")
	}
	if !strings.Contains(plan.Description, "⏱ timeout") {
		t.Errorf("timeout row description = %q", plan.Description)
	}
}

// Enter on a row opens the fullscreen viewer with the FULL stream — every
// renderable event, not the card's 3-line window — and the monitor stays open
// behind it. Esc steps back to the monitor; a second Esc closes it.
func TestSubagentsEnterOpensViewerAndEscReturns(t *testing.T) {
	m := monitorTestModel(t)
	m = submit(t, m, "/subagents")
	selectRow(t, m, "explore-1727000000000000000")
	m = pressKey(t, m, tea.Key{Code: tea.KeyEnter})

	if m.subagentViewer == nil {
		t.Fatal("Enter did not open the stream viewer")
	}
	if m.subagentViewer.agentID != "explore-1727000000000000000" {
		t.Fatalf("viewer agent = %q, want the explore agent", m.subagentViewer.agentID)
	}
	if m.searchPopup == nil || m.searchPopup.mode != searchModeSubagents {
		t.Fatal("the monitor must stay open behind the viewer")
	}

	// Full stream: 44 renderable events (spawn is structural, message_start
	// is filtered), far past the card's collapsed 3-line window.
	body := m.viewerBody()
	if len(body) <= maxAgentOutputLines {
		t.Fatalf("viewer body has %d lines, want more than the card window (%d)", len(body), maxAgentOutputLines)
	}
	// A fresh viewer sits at the newest output; the header carries type,
	// id tail, status and title.
	view := m.overlaySubagentViewer(viewportBlock(m), 90)
	for _, want := range []string{"streamed line 39", "agent[explore]", "scan the auth flow", "Esc back"} {
		if !strings.Contains(view, want) {
			t.Errorf("viewer view missing %q", want)
		}
	}
	// Scrolling to the top reaches the oldest output — the part the collapsed
	// card's window withholds first.
	m = pressKey(t, m, tea.Key{Code: tea.KeyHome})
	view = m.overlaySubagentViewer(viewportBlock(m), 90)
	for _, want := range []string{"the middleware lives in", "internal/auth/middleware.go", "lines 1–"} {
		if !strings.Contains(view, want) {
			t.Errorf("viewer at top missing %q", want)
		}
	}

	// Esc returns to the monitor, not out of both.
	m = pressKey(t, m, tea.Key{Code: tea.KeyEsc})
	if m.subagentViewer != nil {
		t.Fatal("Esc did not close the viewer")
	}
	if m.searchPopup == nil || m.searchPopup.mode != searchModeSubagents {
		t.Fatal("Esc did not return to the monitor")
	}

	// Second Esc closes the monitor.
	m = pressKey(t, m, tea.Key{Code: tea.KeyEsc})
	if m.searchPopup != nil {
		t.Fatal("second Esc did not close the monitor")
	}
}

// Enter on a status-only row (no transcript card) opens nothing — there is no
// stream to show — and keeps the monitor usable.
func TestSubagentsEnterOnStatusOnlyRow(t *testing.T) {
	m := monitorTestModel(t)
	m = submit(t, m, "/subagents")
	selectRow(t, m, "plan-4444")

	m = pressKey(t, m, tea.Key{Code: tea.KeyEnter})

	if m.subagentViewer != nil {
		t.Fatal("Enter on a card-less status opened a viewer with nothing to show")
	}
	if m.searchPopup == nil {
		t.Fatal("the monitor closed")
	}
}

// Viewer scrolling never leaves the body: PgUp clamps at the top, PgDn and
// End clamp at the bottom (the newest output), Home jumps to the top.
func TestSubagentsViewerScrollBounds(t *testing.T) {
	m := monitorTestModel(t)
	m.subagentViewer = &subagentViewerState{agentID: "explore-1727000000000000000"}

	total, viewport := m.viewerBounds()
	if total <= viewport {
		t.Fatalf("precondition: body %d must exceed viewport %d", total, viewport)
	}
	maxScroll := total - viewport

	// PgUp past the top clamps at maxScroll.
	for range 6 {
		m = pressKey(t, m, tea.Key{Code: tea.KeyPgUp})
	}
	if m.subagentViewer.scroll != maxScroll {
		t.Fatalf("after PgUps scroll = %d, want %d (clamped at the top)", m.subagentViewer.scroll, maxScroll)
	}

	// j walks down one line at a time, never below zero.
	m = pressKey(t, m, tea.Key{Code: 'j'})
	if got := maxScroll - m.subagentViewer.scroll; got != 1 {
		t.Fatalf("after j scroll moved %d, want 1", got)
	}
	for range 20 {
		m = pressKey(t, m, tea.Key{Code: 'j'})
	}
	if m.subagentViewer.scroll != 0 {
		t.Fatalf("after End-ish j run scroll = %d, want 0 (clamped at the bottom)", m.subagentViewer.scroll)
	}

	// k walks back up.
	m = pressKey(t, m, tea.Key{Code: 'k'})
	if m.subagentViewer.scroll != 1 {
		t.Fatalf("after k scroll = %d, want 1", m.subagentViewer.scroll)
	}

	// PgDn at the bottom stays at zero; Home jumps to the top again.
	m = pressKey(t, m, tea.Key{Code: tea.KeyPgDown})
	if m.subagentViewer.scroll != 0 {
		t.Fatalf("PgDn at the bottom moved scroll to %d, want 0", m.subagentViewer.scroll)
	}
	m = pressKey(t, m, tea.Key{Code: tea.KeyHome})
	if m.subagentViewer.scroll != maxScroll {
		t.Fatalf("Home scroll = %d, want %d", m.subagentViewer.scroll, maxScroll)
	}
	m = pressKey(t, m, tea.Key{Code: tea.KeyEnd})
	if m.subagentViewer.scroll != 0 {
		t.Fatalf("End scroll = %d, want 0", m.subagentViewer.scroll)
	}
}

// The viewer renders straight from the card: an event that lands on the card
// while the viewer is open shows up in the viewer — and the monitor's rows
// refresh along.
func TestSubagentsViewerLiveUpdate(t *testing.T) {
	m := monitorTestModel(t)
	m.subagentViewer = &subagentViewerState{agentID: "explore-1727000000000000000"}
	m = submit(t, m, "/subagents")

	if strings.Contains(strings.Join(m.viewerBody(), "\n"), "fresh tail") {
		t.Fatal("precondition: the new event is not on the card yet")
	}

	next, _ := m.handleAgentSubEvent(agentSubEventMsg{
		agentID: "explore-1727000000000000000",
		kind:    "text",
		content: "fresh tail",
	})
	m = next.(*model)

	if !strings.Contains(strings.Join(m.viewerBody(), "\n"), "fresh tail") {
		t.Error("the open viewer did not pick up the new event")
	}
	if len(m.searchPopup.filtered) != 3 {
		t.Errorf("monitor rows after the event = %d, want 3 (rebuilt, not reset)", len(m.searchPopup.filtered))
	}
}

// Ctrl+T opens the monitor while a response is running — popups are available
// mid-turn — and Esc closes it without canceling the turn. A second Ctrl+T
// toggles it closed again.
func TestSubagentMonitorHotkeyWhileRunning(t *testing.T) {
	m := monitorTestModel(t)
	m.running = true

	ctrlT := tea.Key{Code: 't', Mod: tea.ModCtrl}
	m = pressKey(t, m, ctrlT)
	if m.searchPopup == nil || m.searchPopup.mode != searchModeSubagents {
		t.Fatal("Ctrl+T did not open the monitor while running")
	}

	m = pressKey(t, m, tea.Key{Code: tea.KeyEsc})
	if m.searchPopup != nil {
		t.Fatal("Esc did not close the monitor")
	}
	if !m.running {
		t.Fatal("Esc canceled the running turn instead of closing the popup")
	}

	m = pressKey(t, m, ctrlT)
	if m.searchPopup == nil {
		t.Fatal("second Ctrl+T did not reopen the monitor")
	}
	m = pressKey(t, m, ctrlT)
	if m.searchPopup != nil {
		t.Fatal("Ctrl+T did not toggle the monitor closed")
	}
}

// Opening and rendering the viewer never invalidates the transcript's render
// cache: the viewer is an overlay, the cards underneath keep theirs.
func TestSubagentViewerKeepsCardRenderCache(t *testing.T) {
	m := monitorTestModel(t)
	m.chatModel.Width = 120

	// Warm the cache, then confirm the second render was served from it.
	m.chatModel.RenderMessages(false)
	for i := range m.chatModel.Messages {
		if !m.chatModel.Messages[i].renderCached {
			t.Fatalf("card %d did not cache a render", i)
		}
	}
	key := m.chatModel.Messages[0].renderCacheKey

	m.subagentViewer = &subagentViewerState{agentID: "explore-1727000000000000000"}
	m.overlaySubagentViewer(m.chatModel.RenderMessages(false), 90)

	if m.chatModel.Messages[0].renderCacheKey != key || !m.chatModel.Messages[0].renderCached {
		t.Error("rendering the viewer disturbed the card's render cache")
	}
}

// A nil SubagentStatuses source degrades the monitor to cards only: rows are
// still shown, with the state inferred from the card's result.
func TestSubagentsMonitorCardsOnlyWithoutProvider(t *testing.T) {
	m := monitorTestModel(t)
	m.cfg.SubagentStatuses = nil

	m = submit(t, m, "/subagents")

	if m.searchPopup == nil {
		t.Fatal("the monitor did not open without a status provider")
	}
	if got := len(m.searchPopup.filtered); got != 2 {
		t.Fatalf("cards-only monitor shows %d rows, want 2", got)
	}
	done, ok := monitorItemByID(m, "done-5555")
	if !ok || !strings.Contains(done.Description, "✓ completed") {
		t.Errorf("finished card row = %+v, want completed", done)
	}
	live, ok := monitorItemByID(m, "explore-1727000000000000000")
	if !ok || !strings.Contains(live.Description, "● running") {
		t.Errorf("card with no result row = %+v, want running", live)
	}
}

// Filtering the monitor narrows by the title text and keeps the machinery of
// the shared search popup.
func TestSubagentsMonitorFilterSearch(t *testing.T) {
	m := monitorTestModel(t)
	m = submit(t, m, "/subagents")

	m.searchPopup.search = "auth"
	m.searchPopup.filterSearch()

	if got := len(m.searchPopup.filtered); got != 1 || m.searchPopup.filtered[0].ID != "explore-1727000000000000000" {
		t.Fatalf("filtered rows = %+v, want only the explore agent", m.searchPopup.filtered)
	}
}
