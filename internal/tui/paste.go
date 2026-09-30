package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Paste summary (issue #22): a large paste collapses into a placeholder in the
// prompt while the full text rides in a side buffer on InputModel. The model
// receives the full text — the placeholder lives only in what is rendered.
//
// Representation: the collapsed paste is a SINGLE rune from Unicode's Private
// Use Area A (pasteMarkerBase+seq), stored in the textinput value, with the
// full text kept in InputModel.pastes keyed by that rune. A one-rune marker is
// what makes the scheme robust: every textinput edit (backspace, delete,
// arrows, word jumps, ctrl+u/ctrl+w) already operates atomically on runes, so
// the marker cannot be torn in half by any editing key — deleting it is one
// backspace, no boundary special-casing. Rendering swaps the rune for a
// human-readable placeholder; input.View output is ANSI-free in the value
// region (virtual cursor is hidden, text style is zero), so a plain string
// replace is exact.
//
// Marker runes can never collide with real content: isUserPaste rejects any
// paste containing non-printable runes, and PUA is not printable, so pasted
// text never carries PUA runes into the value.

const (
	// pasteSummaryMinBytes / pasteSummaryMinLines are the collapse thresholds
	// (either one trips): a paste of more than 2 KiB or more than 30 lines
	// becomes a placeholder instead of literal input text.
	pasteSummaryMinBytes = 2048
	pasteSummaryMinLines = 30

	// pasteMarkerBase..pasteMarkerLast is the rune range markers are drawn
	// from: Private Use Area A, staying below U+F000 where icon fonts park
	// their glyphs. pasteSeq starts at 1, so the first marker is U+E001;
	// the range allows 4095 concurrent pastes in one prompt.
	pasteMarkerBase rune = 0xE000
	pasteMarkerLast rune = 0xEFFF
)

// pasteRecord is one collapsed paste: the full text plus the counts its
// placeholder label shows. Counts are cached at registration — the label is
// rendered every frame, and re-counting a 100 KiB paste per frame is waste.
type pasteRecord struct {
	text  string
	lines int
	bytes int
}

// pasteNeedsSummary reports whether a pasted text trips a collapse threshold.
func pasteNeedsSummary(text string) bool {
	return len(text) > pasteSummaryMinBytes ||
		strings.Count(text, "\n")+1 > pasteSummaryMinLines
}

// isMarkerRune reports whether r is in the marker range. Range membership
// alone does not mean the rune is backed by a paste — pair it with the map
// (isPasteMarker) before treating it as one.
func isMarkerRune(r rune) bool {
	return r > pasteMarkerBase && r <= pasteMarkerLast
}

// registerPaste stores a full paste and returns its marker rune. Returns
// ok=false when the marker space is exhausted; the caller then inserts the
// text literally (unreachable in practice — 4095 live pastes in one prompt).
func (im *InputModel) registerPaste(text string) (rune, bool) {
	if im.pasteSeq >= pasteMarkerLast-pasteMarkerBase {
		return 0, false
	}
	im.pasteSeq++
	r := pasteMarkerBase + im.pasteSeq
	if im.pastes == nil {
		im.pastes = make(map[rune]pasteRecord)
	}
	im.pastes[r] = pasteRecord{
		text:  text,
		lines: strings.Count(text, "\n") + 1,
		bytes: len(text),
	}
	return r, true
}

// isPasteMarker reports whether r is a marker rune that is still backed by a
// buffered paste. Unbacked PUA runes (a paste can no longer produce them, but
// a stale one could survive a SetText) degrade to ordinary characters.
func (im *InputModel) isPasteMarker(r rune) bool {
	_, ok := im.pastes[r]
	return isMarkerRune(r) && ok
}

// expandMarkers replaces every backed marker rune in text with its full paste.
// This is the submit-side half of the feature: InputSubmitMsg, mentions and
// the history entry all see the complete text.
func (im *InputModel) expandMarkers(text string) string {
	if len(im.pastes) == 0 {
		return text
	}
	if !strings.ContainsFunc(text, isMarkerRune) {
		return text
	}
	var b strings.Builder
	b.Grow(len(text))
	for _, r := range text {
		if rec, ok := im.pastes[r]; ok {
			b.WriteString(rec.text)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// prunePastes drops buffer entries whose marker is no longer in the input —
// the paste was deleted, cleared, or replaced. Runs off syncFromInput, so the
// buffer can never outlive its markers. An empty buffer resets the sequence:
// rune reuse is safe exactly when no marker is live, and it keeps the 4095
// marker space from ever running out over a long session.
func (im *InputModel) prunePastes() {
	if len(im.pastes) == 0 {
		return
	}
	live := make(map[rune]bool, len(im.pastes))
	for _, r := range im.Text {
		if isMarkerRune(r) {
			live[r] = true
		}
	}
	for r := range im.pastes {
		if !live[r] {
			delete(im.pastes, r)
		}
	}
	if len(im.pastes) == 0 {
		im.pasteSeq = 0
	}
}

// pasteMarkerNearCursor picks the paste the viewer should show: the marker
// under the cursor, else the nearest one left of it, else the nearest right.
// One marker anywhere in the prompt is therefore always reachable without a
// picker; with several, walking the cursor to a marker selects it.
func (im *InputModel) pasteMarkerNearCursor() (rune, bool) {
	runes := []rune(im.Text)
	if im.CursorPos < len(runes) && im.isPasteMarker(runes[im.CursorPos]) {
		return runes[im.CursorPos], true
	}
	for i := im.CursorPos - 1; i >= 0; i-- {
		if im.isPasteMarker(runes[i]) {
			return runes[i], true
		}
	}
	for i := im.CursorPos + 1; i < len(runes); i++ {
		if im.isPasteMarker(runes[i]) {
			return runes[i], true
		}
	}
	return 0, false
}

// pasteSummaryLabel is the human-readable placeholder for one collapsed paste:
// "[вставка: 34 строки / 12 КБ]".
func pasteSummaryLabel(rec pasteRecord) string {
	kb := (rec.bytes + 1023) / 1024
	if kb < 1 {
		kb = 1
	}
	return fmt.Sprintf("[вставка: %d %s / %d КБ]", rec.lines, pluralRuLines(rec.lines), kb)
}

// pluralRuLines picks the Russian noun form for a line count:
// 1 строка / 2–4 строки / 5+ строк, with 11–14 taking the genitive plural.
func pluralRuLines(n int) string {
	n %= 100
	if n >= 11 && n <= 14 {
		return "строк"
	}
	switch n % 10 {
	case 1:
		return "строка"
	case 2, 3, 4:
		return "строки"
	default:
		return "строк"
	}
}

// renderPastePlaceholders swaps marker runes in an already-rendered input line
// for their styled labels. Safe as a plain replace because the textinput's
// value region renders without ANSI (virtual cursor hidden, zero text style):
// every marker rune appears verbatim in the output, cursor on it or not.
func renderPastePlaceholders(im *InputModel, view string) string {
	if len(im.pastes) == 0 || !strings.ContainsFunc(view, isMarkerRune) {
		return view
	}
	style := lipgloss.NewStyle().Foreground(paletteOrDark(im.Palette).Dim)
	for r, rec := range im.pastes {
		marker := string(r)
		if strings.Contains(view, marker) {
			view = strings.ReplaceAll(view, marker, style.Render(pasteSummaryLabel(rec)))
		}
	}
	return view
}

// --- Paste viewer (Alt+V) ---

// pasteViewerChrome is the rows the viewer spends on its own chrome: two
// border rows, the header, and the hint line — same budget as the subagent
// viewer.
const pasteViewerChrome = 4

// pasteViewerState is the read-only fullscreen view of one collapsed paste's
// full text. scroll is the first visible body line (top-anchored: a paste is
// static, so unlike the live stream viewer it opens at its beginning).
type pasteViewerState struct {
	marker rune
	scroll int
}

// openPasteViewer opens the viewer for the paste nearest the cursor; a no-op
// when the prompt holds no collapsed paste.
func (m *model) openPasteViewer() {
	r, ok := m.inputModel.pasteMarkerNearCursor()
	if !ok {
		return
	}
	m.pasteViewer = &pasteViewerState{marker: r}
	m.pushOverlay(overlayEntry{
		kind:       overlayCustom,
		keyHandler: m.handlePasteViewerKey,
		alive:      func(*model) bool { return m.pasteViewer != nil },
		onClose:    func(*model) { m.pasteViewer = nil },
	})
}

// handlePasteViewerKey drives the paste viewer. Owned keys: Esc steps back to
// the prompt, PgUp/PgDn/Home/End and arrows/j/k scroll. Everything else is
// swallowed — the prompt below the viewer must not collect invisible
// keystrokes — except Ctrl+C, which falls through to handleInterruptKey so
// canceling a running turn stays one chord away.
func (m *model) handlePasteViewerKey(key tea.Key) (tea.Model, tea.Cmd, bool) {
	if m.pasteViewer == nil {
		return nil, nil, false
	}
	if key.Code == 'c' && key.Mod == tea.ModCtrl {
		return nil, nil, false
	}
	v := m.pasteViewer
	total, viewport := m.pasteViewerBounds()
	maxScroll := max(0, total-viewport)
	switch {
	case key.Code == tea.KeyEsc:
		m.popOverlay(overlayCustom)
	case key.Code == tea.KeyPgUp:
		v.scroll = max(0, v.scroll-viewerPage)
	case key.Code == tea.KeyPgDown:
		v.scroll = min(v.scroll+viewerPage, maxScroll)
	case key.Code == tea.KeyUp:
		v.scroll = max(0, v.scroll-1)
	case key.Code == tea.KeyDown:
		v.scroll = min(v.scroll+1, maxScroll)
	case key.Code == 'k' && key.Mod == 0:
		v.scroll = max(0, v.scroll-1)
	case key.Code == 'j' && key.Mod == 0:
		v.scroll = min(v.scroll+1, maxScroll)
	case key.Code == tea.KeyHome:
		v.scroll = 0
	case key.Code == tea.KeyEnd:
		v.scroll = maxScroll
	}
	return m, nil, true
}

// pasteViewerBody returns the viewed paste's full text split into raw lines,
// or nil when the buffer entry is gone (viewer closes on the next sync).
func (m *model) pasteViewerBody() []string {
	if m.pasteViewer == nil {
		return nil
	}
	rec, ok := m.inputModel.pastes[m.pasteViewer.marker]
	if !ok {
		return nil
	}
	return strings.Split(rec.text, "\n")
}

// pasteViewerBounds is the body height the viewer window has inside the
// message viewport — the same budget the overlay actually renders with.
func (m *model) pasteViewerBounds() (int, int) {
	return len(m.pasteViewerBody()), max(1, m.messageViewportHeight()-pasteViewerChrome)
}

// overlayPasteViewer paints the viewer over the message viewport when it is
// open. Same paint scheme as the subagent viewer: a full-height bordered box
// overwriting the message rows; the transcript underneath is untouched.
func (m *model) overlayPasteViewer(messages string, bodyWidth int) string {
	if m.pasteViewer == nil {
		return messages
	}
	box := m.renderPasteViewer(bodyWidth, len(strings.Split(messages, "\n")))
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

// renderPasteViewer builds the viewer box: header (placeholder label and
// paste size), the scrollable body window, and the hint line.
func (m *model) renderPasteViewer(width, viewport int) string {
	rec := m.inputModel.pastes[m.pasteViewer.marker]
	bodyH := max(1, viewport-pasteViewerChrome)
	body := m.pasteViewerBody()
	total := len(body)
	start := min(m.pasteViewer.scroll, max(0, total-bodyH))
	end := min(total, start+bodyH)

	p := paletteOrDark(m.palette)
	header := lipgloss.NewStyle().Foreground(p.Primary).Bold(true).
		Render(pasteSummaryLabel(rec) + fmt.Sprintf(" · %d байт", rec.bytes))

	visible := make([]string, 0, bodyH)
	visible = append(visible, body...)
	for len(visible) < bodyH {
		visible = append(visible, "")
	}

	hint := "Esc back · PgUp/PgDn scroll"
	if total > bodyH {
		hint += fmt.Sprintf(" · lines %d–%d of %d", start+1, end, total)
	}
	hintStyle := lipgloss.NewStyle().Foreground(p.Faint)

	var b strings.Builder
	b.WriteString(header)
	b.WriteString("\n")
	b.WriteString(strings.Join(visible, "\n"))
	b.WriteString("\n")
	b.WriteString(hintStyle.Render(hint))

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder(), true, true, true, true).
		BorderForeground(p.Primary).
		Width(width).
		Render(b.String())
}
