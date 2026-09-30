package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// The subagent monitor: a popup listing the session's subagents and a
// fullscreen viewer for any one agent's full event stream.
//
// Two data sources are stitched by agentID. Statuses (running/duration) come
// from Config.SubagentStatuses — the cli's closure over Orchestrator.List(),
// which is the only component that knows whether a spawn is alive and when it
// ended. The event stream lives on the transcript cards (message.agentEvents),
// which is what the collapsed cards already render a 3-line window of. A row
// can have either half alone: a status whose card scrolled away, or a card
// from a session restored without the orchestrator.

// subagentRow is one monitor row: an orchestrator status stitched to the
// transcript card that carries the same agent's stream.
type subagentRow struct {
	agentID  string
	typ      string
	title    string
	status   string // "running", "completed", "failed", "timeout", "canceled", "killed"
	dur      string // finished runs, from the orchestrator; "" while running
	started  time.Time
	pipeline string // "chain 2/3"; "" outside a pipeline
	cardIdx  int    // index into chatModel.Messages; -1 when there is no card
}

// subagentStatusGlyph is the one-rune state marker shown per row.
func subagentStatusGlyph(status string) string {
	switch status {
	case "running":
		return "●"
	case "completed":
		return "✓"
	case "failed":
		return "✗"
	case "timeout":
		return "⏱"
	case "canceled":
		return "◼"
	case "killed":
		return "⚠"
	default:
		return "·"
	}
}

// subagentRows merges the orchestrator's statuses with the transcript's agent
// cards. Orchestrator entries come first (sorted by start time, so the order
// is stable where List alone is not); cards with no orchestrator entry follow
// in conversation order.
func (m *model) subagentRows() []subagentRow {
	var rows []subagentRow
	byID := map[string]int{}
	if m.cfg.SubagentStatuses != nil {
		statuses := m.cfg.SubagentStatuses()
		// List order is map order, so sort for a stable monitor: by start
		// time, agent ID as the tie-break for statuses that share one.
		sort.Slice(statuses, func(i, j int) bool {
			if !statuses[i].StartedAt.Equal(statuses[j].StartedAt) {
				return statuses[i].StartedAt.Before(statuses[j].StartedAt)
			}
			return statuses[i].AgentID < statuses[j].AgentID
		})
		for _, st := range statuses {
			rows = append(rows, subagentRow{
				agentID:  st.AgentID,
				typ:      st.Type,
				title:    collapseToSingleLine(st.Prompt),
				status:   st.Status,
				dur:      st.Duration,
				started:  st.StartedAt,
				pipeline: "",
				cardIdx:  -1,
			})
			byID[st.AgentID] = len(rows) - 1
		}
	}

	for i := range m.chatModel.Messages {
		msg := &m.chatModel.Messages[i]
		if (msg.tool != "agent" && msg.tool != "subagent") || msg.agentID == "" {
			continue
		}
		if j, ok := byID[msg.agentID]; ok {
			// Both halves: status from the orchestrator, stream and pipeline
			// from the card. The card's stored title is the prompt head the
			// card already shows, so prefer it.
			rows[j].cardIdx = i
			if msg.agentTitle != "" {
				rows[j].title = msg.agentTitle
			}
			rows[j].pipeline = pipelineLabel(msg)
			continue
		}
		// Card with no orchestrator entry: the provider is nil, the entry was
		// evicted, or the session was restored. Infer state from the card —
		// ponytail: content!="" counts as completed even when the run failed;
		// telling those apart from the card alone would mean parsing result
		// shapes. The orchestrator knows the real status when it is wired.
		status := "running"
		if msg.content != "" {
			status = "completed"
		}
		rows = append(rows, subagentRow{
			agentID:  msg.agentID,
			typ:      msg.agentType,
			title:    msg.agentTitle,
			status:   status,
			pipeline: pipelineLabel(msg),
			cardIdx:  i,
		})
	}
	return rows
}

// pipelineLabel renders the card's pipeline position, or "" for a single run.
func pipelineLabel(msg *message) string {
	if msg.pipelineTotal <= 1 {
		return ""
	}
	return fmt.Sprintf("%s %d/%d", msg.pipelineMode, msg.pipelineStep, msg.pipelineTotal)
}

// rowDuration is the elapsed time to show: the orchestrator's own duration
// for finished runs, otherwise live time-since-start (recomputed per render,
// so a monitor left open keeps counting).
func (r subagentRow) duration() string {
	if r.dur != "" {
		return r.dur
	}
	if r.status == "running" && !r.started.IsZero() {
		return time.Since(r.started).Truncate(time.Second).String()
	}
	return ""
}

// subagentSearchItems flattens the merged rows into popup entries. The title
// is the searchable text; the description carries icon, status, type,
// duration and pipeline info.
func (m *model) subagentSearchItems() []SearchItem {
	rows := m.subagentRows()
	items := make([]SearchItem, 0, len(rows))
	for _, r := range rows {
		title := r.title
		if title == "" {
			title = r.typ
		}
		desc := subagentStatusGlyph(r.status) + " " + r.status + " · " + r.typ
		if d := r.duration(); d != "" {
			desc += " · " + d
		}
		if r.pipeline != "" {
			desc += " · " + r.pipeline
		}
		items = append(items, SearchItem{ID: r.agentID, Text: title, Description: desc})
	}
	return items
}

// toggleSubagentsPopup opens the monitor, or closes it when it is the popup
// already showing. This is the Ctrl+T path; /subagents routes straight into
// newSearchPopup like /model and /agent do.
func (m *model) toggleSubagentsPopup() {
	if m.searchPopup != nil && m.searchPopup.mode == searchModeSubagents {
		m.searchPopup = nil
		return
	}
	m.newSearchPopup(searchModeSubagents)
}

// refreshSubagentsPopup rebuilds an open monitor's rows: statuses flip and
// durations advance while the popup sits open, and the events that would
// never come again have already arrived. The selection follows the same
// agentID across the rebuild.
func (m *model) refreshSubagentsPopup() {
	sp := m.searchPopup
	if sp == nil || sp.mode != searchModeSubagents {
		return
	}
	selected := ""
	if sp.selected >= 0 && sp.selected < len(sp.filtered) {
		selected = sp.filtered[sp.selected].ID
	}
	sp.entries = m.subagentSearchItems()
	// Recompute the "current" row too: the pinned running subagent may have
	// finished since the popup opened.
	sp.suggested = m.suggestedItems(sp.mode)
	sp.filterSearch()
	if selected != "" {
		for i, it := range sp.filtered {
			if it.ID == selected {
				sp.selected = i
				break
			}
		}
	}
	m.refreshSearchPopupHeight()
}

// --- Steer (follow-up message to a running subagent) ---

// subagentSteerState is the monitor's mini-input for steering a running
// subagent. Opened with `s` on a running row; Enter sends, Esc cancels.
type subagentSteerState struct {
	agentID string
	label   string // row title (or type), for the input line and the notices
	text    string
}

// label is the human name of a monitor row: the card title when there is one,
// otherwise the agent type.
func (r subagentRow) label() string {
	if r.title != "" {
		return r.title
	}
	return r.typ
}

// tryOpenSubagentSteer opens the steer input for the row the monitor has
// selected. Only an explicit running status from the orchestrator qualifies —
// a card whose status was inferred (no orchestrator entry) is not steerable,
// because there is nothing behind it to receive the message. Every outcome
// other than opening explains itself via a notice.
func (m *model) tryOpenSubagentSteer() {
	sp := m.searchPopup
	if sp == nil || sp.selected < 0 || sp.selected >= len(sp.filtered) {
		return
	}
	agentID := sp.filtered[sp.selected].ID

	var row subagentRow
	found := false
	for _, r := range m.subagentRows() {
		if r.agentID == agentID {
			row, found = r, true
			break
		}
	}

	switch {
	case m.cfg.SteerSubagent == nil:
		m.chatModel.AppendNotice("Steering is not available in this context.")
	case !found:
		m.chatModel.AppendNotice("Agent has no orchestrator status — cannot steer.")
	case row.status != "running":
		m.chatModel.AppendNotice(fmt.Sprintf("%s is %s — only running agents can be steered.",
			agentTitleFit(row.label(), 40), row.status))
	default:
		m.steerInput = &subagentSteerState{agentID: agentID, label: row.label()}
	}
}

// handleSubagentSteerKey drives the open steer input: printable characters
// append, Backspace deletes, Enter sends, Esc cancels. The monitor popup stays
// open underneath, so the steer_queued marker landing on the card is visible
// immediately.
func (m *model) handleSubagentSteerKey(key tea.Key) tea.Cmd {
	si := m.steerInput
	if si == nil {
		return nil
	}
	switch key.Code {
	case tea.KeyEsc:
		m.steerInput = nil
	case tea.KeyEnter:
		m.steerInput = nil
		text := strings.TrimSpace(si.text)
		if text == "" {
			return nil // nothing to send
		}
		if err := m.cfg.SteerSubagent(si.agentID, text); err != nil {
			m.chatModel.AppendNotice(fmt.Sprintf("Steer failed: %v", err))
			return nil
		}
		m.chatModel.AppendNotice(fmt.Sprintf("⏎ steer queued → %s", agentTitleFit(si.label, 40)))
	case tea.KeyBackspace:
		if r := []rune(si.text); len(r) > 0 {
			si.text = string(r[:len(r)-1])
		}
	default:
		// Same guard as the popup filter: one printable character, no chord.
		if len(key.Text) == 1 && key.Mod == 0 {
			si.text += key.Text
		}
	}
	return nil
}

// --- Fullscreen stream viewer ---

// subagentViewerState is the fullscreen view of one subagent card's full
// agentEvents — everything the collapsed card's 3-line window withholds.
type subagentViewerState struct {
	agentID string
	// scroll is rendered body lines above the bottom of the window — the same
	// from-bottom convention as ChatModel.Scroll, so a fresh viewer sits at
	// the newest output and PgUp walks back into history.
	scroll int
}

// viewerChrome is the rows the viewer spends on its own chrome: two border
// rows, the header, and the hint line.
const viewerChrome = 4

// viewerPage is how many lines PgUp/PgDn move.
const viewerPage = 10

// agentCardByID returns the transcript card carrying agentID's stream, or nil.
func (m *model) agentCardByID(agentID string) *message {
	for i := range m.chatModel.Messages {
		msg := &m.chatModel.Messages[i]
		if (msg.tool == "agent" || msg.tool == "subagent") && msg.agentID == agentID {
			return msg
		}
	}
	return nil
}

// viewerCard returns the transcript card the viewer is pointed at, or nil
// when it has none (the card scrolled out of the model).
func (m *model) viewerCard() *message {
	if m.subagentViewer == nil {
		return nil
	}
	return m.agentCardByID(m.subagentViewer.agentID)
}

// viewerBody renders every renderable event of the viewer's card in the same
// per-kind styles the collapsed card uses — just without the window cap.
// Live updates are free: the card is the single source, so an event that
// lands in the card shows up on the next frame.
func (m *model) viewerBody() []string {
	card := m.viewerCard()
	if card == nil {
		return nil
	}
	st := newAgentEventStyles(card.agentType, m.palette)
	cw := renderWrapWidth(m.chatWidth()-8, 0)
	var lines []string
	for _, ev := range renderableAgentEvents(card.agentEvents) {
		lines = append(lines, agentEventLines(ev, st, cw)...)
	}
	return lines
}

// subagentStatusOf returns the merged row for agentID, for the viewer header.
func (m *model) subagentStatusOf(agentID string) subagentRow {
	for _, r := range m.subagentRows() {
		if r.agentID == agentID {
			return r
		}
	}
	return subagentRow{status: "running", cardIdx: -1}
}

// handleSubagentViewerKey drives the fullscreen viewer. Owned keys: Esc steps
// back to the monitor, PgUp/PgDn/Home/End and j/k scroll. Everything else is
// swallowed — the popup filter behind the viewer must not collect invisible
// keystrokes — except Ctrl+C, which falls through to handleInterruptKey so
// canceling a running turn stays one chord away.
func (m *model) handleSubagentViewerKey(key tea.Key) (tea.Model, tea.Cmd, bool) {
	if m.subagentViewer == nil {
		return nil, nil, false
	}
	if key.Code == 'c' && key.Mod == tea.ModCtrl {
		return nil, nil, false
	}
	v := m.subagentViewer
	total, viewport := m.viewerBounds()
	maxScroll := max(0, total-viewport)
	switch {
	case key.Code == tea.KeyEsc:
		m.subagentViewer = nil
	case key.Code == tea.KeyPgUp:
		v.scroll = min(v.scroll+viewerPage, maxScroll)
	case key.Code == tea.KeyPgDown:
		v.scroll = max(0, v.scroll-viewerPage)
	case key.Code == 'k' && key.Mod == 0:
		v.scroll = min(v.scroll+1, maxScroll)
	case key.Code == 'j' && key.Mod == 0:
		v.scroll = max(0, v.scroll-1)
	case key.Code == tea.KeyHome:
		v.scroll = maxScroll
	case key.Code == tea.KeyEnd:
		v.scroll = 0
	}
	return m, nil, true
}

// viewerBounds is the body height the viewer window has inside the message
// viewport — the same budget the overlay will actually render with.
func (m *model) viewerBounds() (int, int) {
	return len(m.viewerBody()), max(1, m.messageViewportHeight()-viewerChrome)
}

// overlaySubagentViewer paints the viewer over the message viewport when it
// is open. It renders its own bordered box full-height and overwrites every
// message row; the transcript underneath is untouched, so the per-message
// render cache stays exactly as it was.
func (m *model) overlaySubagentViewer(messages string, bodyWidth int) string {
	if m.subagentViewer == nil {
		return messages
	}
	box := m.renderSubagentViewer(bodyWidth, len(strings.Split(messages, "\n")))
	boxLines := strings.Split(box, "\n")
	lines := strings.Split(messages, "\n")
	for i := range lines {
		if i < len(boxLines) {
			lines[i] = boxLines[i]
			continue
		}
		lines[i] = ""
	}
	return strings.Join(lines, "\n")
}

// renderSubagentViewer builds the fullscreen box: header (type, id tail,
// status, title), the scrollable body window, and the hint line.
func (m *model) renderSubagentViewer(width, viewport int) string {
	v := m.subagentViewer
	row := m.subagentStatusOf(v.agentID)

	accent := m.palette.Blue
	switch row.status {
	case "running":
		accent = m.palette.Yellow
	case "completed":
		accent = m.palette.Green
	case "failed", "timeout", "killed":
		accent = m.palette.Error
	}

	idTail := v.agentID
	if dash := strings.IndexByte(idTail, '-'); dash > 0 {
		idTail = idTail[:dash]
	}
	if len(idTail) > 8 {
		idTail = idTail[:8]
	}
	statusColor := lipgloss.NewStyle().Foreground(accent).Bold(true)
	header := fmt.Sprintf("%s agent[%s] · %s · %s %s",
		statusColor.Render(subagentStatusGlyph(row.status)),
		row.typ, idTail,
		statusColor.Render(row.status), row.duration())
	if row.title != "" {
		header += " — " + agentTitleFit(row.title, max(20, width-20))
	}

	body := m.viewerBody()
	bodyH := max(1, viewport-viewerChrome)
	total := len(body)
	start := max(0, total-v.scroll-bodyH)
	end := min(total, start+bodyH)
	visible := make([]string, 0, bodyH)
	visible = append(visible, body[start:end]...)
	for len(visible) < bodyH {
		visible = append(visible, "")
	}

	hint := "Esc back · PgUp/PgDn scroll"
	if total > bodyH {
		hint += fmt.Sprintf(" · lines %d–%d of %d", start+1, end, total)
	}
	hintStyle := lipgloss.NewStyle().Foreground(m.palette.Faint)

	var b strings.Builder
	b.WriteString(header)
	b.WriteString("\n")
	b.WriteString(strings.Join(visible, "\n"))
	b.WriteString("\n")
	b.WriteString(hintStyle.Render(hint))

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder(), true, true, true, true).
		BorderForeground(accent).
		Width(width).
		Render(b.String())
}
