package tui

import (
	"strings"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// bigPaste builds a paste that trips both thresholds.
func bigPaste() string {
	var b strings.Builder
	for i := 0; i < pasteSummaryMinLines+5; i++ {
		b.WriteString("line of pasted content\n")
	}
	return b.String()
}

// widePaste builds a single-line paste over the byte threshold.
func widePaste() string {
	return strings.Repeat("x", pasteSummaryMinBytes+100)
}

// insertCollapsed pastes big text and returns the marker rune it collapsed
// to. InsertText parks the cursor right after the inserted marker, so the
// marker is the rune before the cursor.
func insertCollapsed(t *testing.T, im *InputModel, text string) rune {
	t.Helper()
	im.InsertText(text)
	runes := []rune(im.Text)
	if im.CursorPos > 0 && im.CursorPos <= len(runes) {
		if r := runes[im.CursorPos-1]; im.isPasteMarker(r) {
			return r
		}
	}
	t.Fatalf("expected a paste marker before cursor %d in value %q", im.CursorPos, im.Text)
	return 0
}

func TestPasteCollapse_OverThresholds(t *testing.T) {
	tests := []struct {
		name  string
		paste string
	}{
		{"many lines", bigPaste()},
		{"many bytes", widePaste()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !pasteNeedsSummary(tt.paste) {
				t.Fatal("paste should trip a threshold")
			}
			im := NewInputModel(nil, nil, nil, "")
			r := insertCollapsed(t, &im, tt.paste)
			if im.Text != string(r) {
				t.Errorf("value should be exactly the marker, got %q", im.Text)
			}
			rec, ok := im.pastes[r]
			if !ok {
				t.Fatal("buffer should hold the full paste")
			}
			if rec.text != tt.paste {
				t.Error("buffer text should equal the pasted text")
			}
		})
	}
}

func TestPasteCollapse_UnderThreshold(t *testing.T) {
	im := NewInputModel(nil, nil, nil, "")
	// Single-line: the textinput flattens \n on any insert (pre-existing
	// bubbles sanitize behavior) — only the collapse path preserves them.
	small := "just a normal paste, under the threshold"
	im.InsertText(small)
	if im.Text != small {
		t.Errorf("small paste should stay literal, got %q", im.Text)
	}
	if len(im.pastes) != 0 {
		t.Errorf("buffer should be empty, got %d entries", len(im.pastes))
	}
}

// A collapsed paste keeps what the literal path loses: the textinput
// sanitizer flattens newlines inside SetValue, but the buffer holds the
// original text, so a multi-line paste still submits with its newlines.
func TestPasteCollapse_BufferKeepsNewlines(t *testing.T) {
	im := NewInputModel(nil, nil, nil, "")
	paste := strings.Repeat("line\n", pasteSummaryMinLines+1)
	r := insertCollapsed(t, &im, paste)
	if im.pastes[r].text != paste {
		t.Error("buffer should keep the paste exactly as pasted")
	}
	if strings.ContainsRune(im.Text, '\n') {
		t.Error("value must stay single-line")
	}
}

func TestPasteCollapse_ThresholdBoundaries(t *testing.T) {
	exact := []struct {
		name  string
		paste string
		want  bool
	}{
		{"bytes at threshold stays literal", strings.Repeat("x", pasteSummaryMinBytes), false},
		{"bytes over threshold collapses", strings.Repeat("x", pasteSummaryMinBytes+1), true},
		{"lines at threshold stay literal", strings.Repeat("l\n", pasteSummaryMinLines-1) + "l", false},
		{"lines over threshold collapse", strings.Repeat("l\n", pasteSummaryMinLines), true},
	}
	for _, tt := range exact {
		t.Run(tt.name, func(t *testing.T) {
			if got := pasteNeedsSummary(tt.paste); got != tt.want {
				t.Errorf("pasteNeedsSummary = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPasteRender_LabelInPlaceOfMarker(t *testing.T) {
	im := NewInputModel(nil, nil, nil, "")
	paste := bigPaste()
	r := insertCollapsed(t, &im, paste)
	im.CursorPos = 1

	view := im.View(false)
	if strings.ContainsRune(view, r) {
		t.Error("rendered input must not show the raw marker rune")
	}
	want := pasteSummaryLabel(im.pastes[r])
	if !strings.Contains(view, want) {
		t.Errorf("rendered input should contain label %q, got %q", want, ansi.Strip(view))
	}
	if strings.Contains(view, "\n") {
		t.Error("input render must stay a single line")
	}
	if !strings.Contains(view, "[вставка:") {
		t.Errorf("label should be human-readable, got %q", ansi.Strip(view))
	}
}

func TestPasteRender_TypingAroundMarker(t *testing.T) {
	im := NewInputModel(nil, nil, nil, "")
	r := insertCollapsed(t, &im, widePaste())

	// Type before and after the marker: value keeps the marker in the middle.
	im.CursorPos = 0
	im.HandleKey(tea.KeyPressMsg{Code: 'a', Text: "a"})
	im.CursorPos = utf8.RuneCountInString(im.Text)
	im.HandleKey(tea.KeyPressMsg{Code: 'b', Text: "b"})
	if im.Text != "a"+string(r)+"b" {
		t.Errorf("typing around marker broken: %q", im.Text)
	}
	// The buffer survives typed edits.
	if _, ok := im.pastes[r]; !ok {
		t.Error("buffer entry lost after typing around the marker")
	}
}

func TestPasteSubmit_ExpandsToFullText(t *testing.T) {
	im := NewInputModel(nil, nil, nil, "")
	paste := "see @internal/agent/loop.go for details\n" + strings.Repeat("y", pasteSummaryMinBytes)
	r := insertCollapsed(t, &im, paste)
	im.CursorPos = utf8.RuneCountInString(im.Text) // end

	cmd := im.HandleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter with content should submit")
	}
	msg, ok := cmd().(InputSubmitMsg)
	if !ok {
		t.Fatalf("expected InputSubmitMsg, got %T", cmd())
	}
	if msg.Text != paste {
		t.Errorf("submit should carry the full paste, got %d bytes, want %d", len(msg.Text), len(paste))
	}
	if len(msg.Mentions) != 1 || msg.Mentions[0] != "internal/agent/loop.go" {
		t.Errorf("mentions should come from the expanded text, got %v", msg.Mentions)
	}
	if len(im.History) != 1 || im.History[0].Text != paste {
		t.Error("history should record the full paste")
	}
	if len(im.pastes) != 0 {
		t.Errorf("buffer should be pruned after submit, got %d entries", len(im.pastes))
	}
	if _, ok := im.pastes[r]; ok {
		t.Error("marker entry should be gone")
	}
}

func TestPasteBackspace_AtMarkerDeletesWholeMarker(t *testing.T) {
	im := NewInputModel(nil, nil, nil, "")
	r := insertCollapsed(t, &im, widePaste())
	if im.Text != string(r) {
		t.Fatalf("setup: expected lone marker, got %q", im.Text)
	}

	// Cursor sits right after the marker (InsertText parks it there); one
	// backspace removes the whole collapsed paste.
	im.HandleKey(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if im.Text != "" {
		t.Errorf("backspace should remove the whole marker atom, got %q", im.Text)
	}
	if len(im.pastes) != 0 {
		t.Errorf("buffer should prune the deleted paste, got %d entries", len(im.pastes))
	}
}

func TestPasteDelete_ForwardAtMarkerDeletesWholeMarker(t *testing.T) {
	im := NewInputModel(nil, nil, nil, "")
	prefix := "keep "
	im.InsertText(prefix)
	r := insertCollapsed(t, &im, widePaste())
	if im.Text != prefix+string(r) {
		t.Fatalf("setup: got %q", im.Text)
	}

	// Park the cursor on the marker (before it) and delete forward.
	im.CursorPos = utf8.RuneCountInString(prefix)
	im.HandleKey(tea.KeyPressMsg{Code: tea.KeyDelete})
	if im.Text != prefix {
		t.Errorf("delete should remove the marker atom, got %q", im.Text)
	}
	if len(im.pastes) != 0 {
		t.Error("buffer should prune the deleted paste")
	}
}

func TestPasteViewer_OpenViewClose(t *testing.T) {
	m := newTestModelFull(t)
	paste := bigPaste()
	m.inputModel.InsertText(widePaste())
	m.inputModel.InsertText(paste) // second marker, nearest the cursor

	m.handleKey(tea.KeyPressMsg{Code: 'v', Mod: tea.ModAlt})
	if m.pasteViewer == nil {
		t.Fatal("alt+v should open the paste viewer")
	}
	r := m.pasteViewer.marker
	rec, ok := m.inputModel.pastes[r]
	if !ok || rec.text != paste {
		t.Fatal("viewer should target the paste nearest the cursor (the last one)")
	}

	// Renders over the message viewport with the full text inside.
	view := m.View().Content
	if !strings.Contains(ansi.Strip(view), "line of pasted content") {
		t.Error("viewer render should contain the full pasted text")
	}
	if !strings.Contains(ansi.Strip(view), pasteSummaryLabel(rec)) {
		t.Error("viewer header should show the placeholder label")
	}

	// Scroll and close.
	m.handleKey(tea.KeyPressMsg{Code: 'j'})
	if m.pasteViewer == nil || m.pasteViewer.scroll != 1 {
		t.Error("j should scroll the viewer")
	}
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.pasteViewer != nil {
		t.Error("esc should close the paste viewer")
	}
}

func TestPasteViewer_NoMarkerIsNoop(t *testing.T) {
	m := newTestModel(t)
	m.inputModel.InsertText("small paste")
	m.handleKey(tea.KeyPressMsg{Code: 'v', Mod: tea.ModAlt})
	if m.pasteViewer != nil {
		t.Error("alt+v without collapsed pastes should be a no-op")
	}
}

func TestPasteMarkerNearCursor_Selection(t *testing.T) {
	im := NewInputModel(nil, nil, nil, "")
	im.InsertText("x")
	r1 := insertCollapsed(t, &im, widePaste())
	im.InsertText("y")
	r2 := insertCollapsed(t, &im, bigPaste())
	im.InsertText("z")

	// Cursor at the end: nearest left marker wins (r2).
	im.CursorPos = utf8.RuneCountInString(im.Text)
	if r, ok := im.pasteMarkerNearCursor(); !ok || r != r2 {
		t.Errorf("nearest left marker expected, got %v ok=%v", r, ok)
	}
	// Cursor before everything: nearest right marker wins (r1).
	im.CursorPos = 1 // on the "x"→marker boundary; put at 1 (right after "x")
	if r, ok := im.pasteMarkerNearCursor(); !ok || r != r1 {
		t.Errorf("nearest right marker expected, got %v ok=%v", r, ok)
	}
	// No markers at all → not ok.
	im2 := NewInputModel(nil, nil, nil, "")
	im2.InsertText("plain")
	if _, ok := im2.pasteMarkerNearCursor(); ok {
		t.Error("no markers should report not-ok")
	}
}

func TestPasteSummaryLabel_Format(t *testing.T) {
	tests := []struct {
		lines int
		bytes int
		want  string
	}{
		{1, 3000, "[вставка: 1 строка / 3 КБ]"},
		{2, 2049, "[вставка: 2 строки / 3 КБ]"},
		{5, 4096, "[вставка: 5 строк / 4 КБ]"},
		{11, 5120, "[вставка: 11 строк / 5 КБ]"},
		{21, 1024, "[вставка: 21 строка / 1 КБ]"},
		{34, 12544, "[вставка: 34 строки / 13 КБ]"},
	}
	for _, tt := range tests {
		got := pasteSummaryLabel(pasteRecord{lines: tt.lines, bytes: tt.bytes})
		if got != tt.want {
			t.Errorf("pasteSummaryLabel(%d lines, %d bytes) = %q, want %q", tt.lines, tt.bytes, got, tt.want)
		}
	}
}

func TestPasteExpandMarkers_MixedText(t *testing.T) {
	im := NewInputModel(nil, nil, nil, "")
	p1 := widePaste()
	im.InsertText("before ")
	r1 := insertCollapsed(t, &im, p1)
	im.InsertText(" middle ")
	r2 := insertCollapsed(t, &im, bigPaste())
	im.InsertText(" after")

	got := im.expandMarkers(im.Text)
	want := "before " + p1 + " middle " + bigPaste() + " after"
	if got != want {
		t.Errorf("expansion mismatch:\n got %d bytes\nwant %d bytes", len(got), len(want))
	}
	if r1 == r2 {
		t.Error("markers must be unique per paste")
	}
}

// Deleting the last paste frees its rune for reuse: the sequence resets with
// the buffer, so the marker space cannot run out over a session.
func TestPasteSeq_ResetsWhenBufferEmpties(t *testing.T) {
	im := NewInputModel(nil, nil, nil, "")
	r1 := insertCollapsed(t, &im, widePaste())
	im.CursorPos = utf8.RuneCountInString(im.Text)
	im.HandleKey(tea.KeyPressMsg{Code: tea.KeyBackspace}) // deletes the marker
	if len(im.pastes) != 0 {
		t.Fatalf("buffer should be empty after delete, got %d", len(im.pastes))
	}
	r2 := insertCollapsed(t, &im, widePaste())
	if r2 != r1 {
		t.Errorf("sequence should restart at the first marker, got %v want %v", r2, r1)
	}
}
