package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// viewRows counts the rendered rows of the input area.
func viewRows(im *InputModel) int {
	return strings.Count(im.View(false), "\n") + 1
}

// --- auto-grow and the visual-row cap --------------------------------------

// The input grows line by line until the cap and then stops growing: 1 line
// renders 3 rows (the bordered box adds two), 5 lines render 7, and 50 lines
// render exactly the cap plus the box.
func TestInputAutoGrow_UpToCap(t *testing.T) {
	im := NewInputModel(nil, nil, nil, "")
	im.SetWidth(40)
	im.SetMaxHeight(4)

	if rows := viewRows(&im); rows != 3 {
		t.Fatalf("empty input rows = %d, want 3 (1 content + 2 border)", rows)
	}
	im.SetText(strings.Repeat("l\n", 4) + "l") // 5 lines
	if rows := viewRows(&im); rows != 6 {
		t.Fatalf("5 lines at cap 4 rows = %d, want 6 (4 content + 2 border)", rows)
	}
	im.SetText(strings.Repeat("line\n", 49) + "line") // 50 lines
	if rows := viewRows(&im); rows != 6 {
		t.Fatalf("50 lines at cap 4 rows = %d, want 6 (4 content + 2 border)", rows)
	}
}

// Past the cap the engine scrolls internally: the viewport window moves so
// the cursor (parked at the end) stays visible.
func TestInputInternalScroll_AtCap(t *testing.T) {
	im := NewInputModel(nil, nil, nil, "")
	im.SetWidth(40)
	im.SetMaxHeight(4)
	im.SetText(strings.Repeat("line\n", 19) + "line") // 20 lines

	off := im.input.ScrollYOffset()
	if off <= 0 {
		t.Fatalf("ScrollYOffset = %d, want > 0 with the cursor on line 20 of a 4-row window", off)
	}
	// The cursor line must sit inside the visible window. ("line" never
	// wraps at width 40, so logical line index == display row here.)
	cursor := im.input.Line()
	if cursor < off || cursor > off+4-1 {
		t.Fatalf("cursor line %d outside the scrolled window [%d, %d]", cursor, off, off+3)
	}
	// Back to the top: the window follows the cursor.
	im.CursorPos = 0
	im.setFlatCursor(im.CursorPos)
	if off := im.input.ScrollYOffset(); off != 0 {
		t.Fatalf("ScrollYOffset = %d after moving to the top, want 0", off)
	}
}

// Raising the cap on resize un-clamps the view; lowering it re-clamps. Rows
// are counted on the rendered View, so every expectation carries the two
// border rows of the prompt box.
func TestInputMaxHeight_Resize(t *testing.T) {
	im := NewInputModel(nil, nil, nil, "")
	im.SetWidth(40)
	im.SetText(strings.Repeat("line\n", 9) + "line") // 10 lines

	im.SetMaxHeight(8)
	if rows := viewRows(&im); rows != 10 {
		t.Fatalf("rows = %d, want 10 (8 content + 2 border)", rows)
	}
	im.SetMaxHeight(2)
	if rows := viewRows(&im); rows != 4 {
		t.Fatalf("rows = %d, want 4 (2 content + 2 border)", rows)
	}
	// Cap of at least one row, even for a degenerate resize.
	im.SetMaxHeight(0)
	if rows := viewRows(&im); rows != 3 {
		t.Fatalf("rows = %d, want 3 (1 content + 2 border)", rows)
	}
}

// The message viewport yields rows to a grown input and takes them back when
// the prompt shrinks.
func TestMessageViewport_YieldsRowsToInput(t *testing.T) {
	m := newTestModelFull(t) // height 30 → cap 10
	m.inputModel.SetWidth(80)
	m.inputModel.SetMaxHeight(max(1, m.height/3))

	before := m.messageViewportHeight()

	m.inputModel.SetText(strings.Repeat("line\n", 9) + "line") // 10 lines
	during := m.messageViewportHeight()
	if during >= before {
		t.Fatalf("viewport height %d must shrink while a 10-line prompt is open (was %d)", during, before)
	}

	m.inputModel.Clear()
	after := m.messageViewportHeight()
	if after != before {
		t.Fatalf("viewport height %d must recover after the prompt clears (was %d)", after, before)
	}
}

// The full View wires the cap (a third of the terminal) without a manual
// SetMaxHeight call.
func TestView_AppliesTerminalCap(t *testing.T) {
	m := newTestModelFull(t) // height 30
	m.View()
	if m.inputModel.maxHeight != 10 {
		t.Fatalf("cap after View = %d, want height/3 = 10", m.inputModel.maxHeight)
	}
	m.height = 9
	m.View()
	if m.inputModel.maxHeight != 3 {
		t.Fatalf("cap after resize = %d, want height/3 = 3", m.inputModel.maxHeight)
	}
}

// --- Enter / Shift+Enter ----------------------------------------------------

// Shift+Enter inserts a newline; Enter submits the multi-line text, records
// it in history with the newlines intact, and clears the prompt.
func TestEnterSemantics_Multiline(t *testing.T) {
	im := NewInputModel(nil, nil, nil, "")
	im.SetWidth(40)

	typeRune := func(code rune, text string, mod tea.KeyMod) tea.Cmd {
		return im.HandleKey(tea.KeyPressMsg(tea.Key{Code: code, Text: text, Mod: mod}))
	}
	typeRune('a', "a", 0)
	typeRune(tea.KeyEnter, "", tea.ModShift)
	typeRune('b', "b", 0)
	if im.Text != "a\nb" {
		t.Fatalf("text = %q, want %q", im.Text, "a\nb")
	}
	if im.CursorPos != 3 {
		t.Fatalf("cursor = %d, want 3 (after the newline)", im.CursorPos)
	}

	cmd := typeRune(tea.KeyEnter, "", 0)
	if cmd == nil {
		t.Fatal("Enter with content should submit")
	}
	msg, ok := cmd().(InputSubmitMsg)
	if !ok {
		t.Fatalf("expected InputSubmitMsg, got %T", cmd())
	}
	if msg.Text != "a\nb" {
		t.Errorf("submit text = %q, want the multi-line prompt", msg.Text)
	}
	if len(im.History) != 1 || im.History[0].Text != "a\nb" {
		t.Errorf("history should record the multi-line text, got %+v", im.History)
	}
	if im.Text != "" {
		t.Errorf("prompt should be clear after submit, got %q", im.Text)
	}
}

// Enter on an empty prompt is a no-op, Shift+Enter on an empty prompt starts
// a new line.
func TestEnterSemantics_EmptyPrompt(t *testing.T) {
	im := NewInputModel(nil, nil, nil, "")
	if cmd := im.HandleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})); cmd != nil {
		t.Fatal("Enter on an empty prompt must not submit")
	}
	im.HandleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter, Mod: tea.ModShift}))
	if im.Text != "\n" {
		t.Fatalf("Shift+Enter on an empty prompt = %q, want a newline", im.Text)
	}
}

// Mentions are extracted across lines: an @ on the second line rides along.
func TestSubmit_MentionsAcrossLines(t *testing.T) {
	im := NewInputModel(nil, nil, nil, "")
	im.SetText("see\n@src/app.go now")
	cmd := im.HandleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	msg := cmd().(InputSubmitMsg)
	if len(msg.Mentions) != 1 || msg.Mentions[0] != "src/app.go" {
		t.Errorf("mentions = %v, want [src/app.go]", msg.Mentions)
	}
}

// --- history window vs multiline arrows -------------------------------------

// Up from an inner row moves the cursor between lines and never opens the
// history window; from the first row it opens it, as it always did.
func TestHistoryWindow_UpGatesOnFirstRow(t *testing.T) {
	m := historyModel(t, "old prompt")
	m.inputModel.SetText("l1\nl2\nl3") // cursor at the end: line 3

	m = press(t, m, tea.KeyUp)
	if m.searchPopup != nil {
		t.Fatal("Up from an inner row opened the history window")
	}
	// Arrows keep the column (editor semantics): from the end of "l3" to the
	// same offset of "l2".
	if got := m.inputModel.CursorPos; got != 5 {
		t.Fatalf("cursor = %d, want 5 (line 2, column 2)", got)
	}

	m = press(t, m, tea.KeyUp)
	if m.searchPopup != nil {
		t.Fatal("Up from line 2 opened the history window")
	}
	if got := m.inputModel.CursorPos; got != 2 {
		t.Fatalf("cursor = %d, want 2 (line 1, column 2)", got)
	}

	m = press(t, m, tea.KeyUp)
	if m.searchPopup == nil || m.searchPopup.mode != searchModeHistory {
		t.Fatal("Up from the first row should open the history window")
	}
	if m.inputModel.Text != "l1\nl2\nl3" {
		t.Fatalf("draft clobbered: %q", m.inputModel.Text)
	}
}

// Down from an inner row moves the cursor down; from the last row it scrolls
// the chat, as it always did.
func TestHistoryWindow_DownGatesOnLastRow(t *testing.T) {
	m := historyModel(t)
	fillChat(m)
	m.chatModel.Scroll = 20
	m.inputModel.SetText("l1\nl2\nl3")
	m.inputModel.CursorPos = 0 // first row

	m = press(t, m, tea.KeyDown)
	if got := m.inputModel.CursorPos; got != 3 { // start of line 2
		t.Fatalf("cursor = %d, want 3 (start of line 2)", got)
	}
	m = press(t, m, tea.KeyDown)
	if got := m.inputModel.CursorPos; got != 6 { // start of line 3
		t.Fatalf("cursor = %d, want 6 (start of line 3)", got)
	}
	if m.chatModel.Scroll != 20 {
		t.Fatal("chat scrolled while the cursor was still inside the prompt")
	}

	m = press(t, m, tea.KeyDown)
	if m.chatModel.Scroll >= 20 {
		t.Fatal("Down from the last row should scroll the chat")
	}
}

// --- slash detection ---------------------------------------------------------

// The slash-command popup is a first-line affordance: any newline in the
// prompt (before or after the slash token) closes it.
func TestSlashPopup_FirstLineOnly(t *testing.T) {
	m := historyModel(t, "x")
	m.inputModel.SetText("/co")
	if !m.shouldShowSlashCommandPopup() {
		t.Fatal("/co on the first line should show the popup")
	}
	m.inputModel.SetText("/co\nsecond line")
	if m.shouldShowSlashCommandPopup() {
		t.Fatal("a newline after the token must close the popup")
	}
	m.inputModel.SetText("first\n/co")
	if m.shouldShowSlashCommandPopup() {
		t.Fatal("a slash token on a later line must not open the popup")
	}
}

// --- @-mentions across lines --------------------------------------------------

// An @ starts a mention from any line — the popup opens while the cursor is
// on the @'s line and closes when the cursor moves to another line.
func TestMentionPopup_AnyLineCursorGated(t *testing.T) {
	m := mentionModel(t)
	m.inputModel.SetText("look here\n@")
	// SetText parks the cursor at the end: on the @ line, past the @.
	m.syncFilesPopup()
	if m.searchPopup == nil || m.searchPopup.mode != searchModeFiles {
		t.Fatal("@ on the second line should open the file popup while the cursor is there")
	}

	// Cursor to the first line: the popup must close — the trigger is in
	// another line now.
	m.inputModel.CursorPos = 1
	m.syncFilesPopup()
	if m.searchPopup != nil {
		t.Fatal("the file popup must close when the cursor leaves the @'s line")
	}

	// Back to the @ line: it re-opens.
	m.inputModel.CursorPos = len("look here\n@")
	m.syncFilesPopup()
	if m.searchPopup == nil || m.searchPopup.mode != searchModeFiles {
		t.Fatal("the file popup must re-open when the cursor returns to the @")
	}
}

// A multi-line prompt never leaks a mention into the previous line: the
// newline is a hard boundary for the @ walk.
func TestMentionTrigger_NewlineBoundary(t *testing.T) {
	m := mentionModel(t)
	m.inputModel.SetText("first\nmail me")
	m.inputModel.CursorPos = len(m.inputModel.Text)
	m.syncFilesPopup()
	if m.searchPopup != nil {
		t.Fatal("no @, no popup")
	}
}

// --- paste (issue #22 markers on the new engine) -----------------------------

// CRLF pastes normalize to LF instead of doubling every break (the textarea
// sanitizer turns a lone \r into \n).
func TestPaste_CRLFNormalized(t *testing.T) {
	im := NewInputModel(nil, nil, nil, "")
	im.InsertText("a\r\nb\rc")
	if im.Text != "a\nb\nc" {
		t.Fatalf("text = %q, want %q", im.Text, "a\nb\nc")
	}
	if lines := strings.Count(im.Text, "\n"); lines != 2 {
		t.Fatalf("line breaks = %d, want 2 (no CR doubling)", lines)
	}
}

// A small multi-line paste stays literal and produces a real multi-line
// prompt; markers still keep big pastes collapsed to one rune.
func TestPaste_MultilineLiteralAndMarker(t *testing.T) {
	im := NewInputModel(nil, nil, nil, "")
	im.SetWidth(40)

	im.InsertText("one\ntwo")
	if im.Text != "one\ntwo" {
		t.Fatalf("literal paste = %q", im.Text)
	}
	if rows := viewRows(&im); rows != 4 {
		t.Fatalf("a two-line paste should render four rows (2 content + 2 border), got %d", rows)
	}

	r := insertCollapsed(t, &im, bigPaste())
	if im.Text != "one\ntwo"+string(r) {
		t.Fatalf("marker collapse = %q", im.Text)
	}
	// Submit expands markers and keeps the literal newlines.
	cmd := im.HandleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	msg := cmd().(InputSubmitMsg)
	if !strings.Contains(msg.Text, "one\ntwo") || !strings.Contains(msg.Text, "line of pasted content") {
		t.Errorf("submit should carry literal text + expanded paste, got %q", msg.Text)
	}
}

// Marker runes survive engine edits atomically: backspace mid-prompt removes
// a marker whole, never half.
func TestPaste_MarkerAtomicAcrossLines(t *testing.T) {
	im := NewInputModel(nil, nil, nil, "")
	im.SetWidth(40)
	im.InsertText("alpha\n")
	r := insertCollapsed(t, &im, widePaste())
	im.InsertText("\nomega")

	if im.Text != "alpha\n"+string(r)+"\nomega" {
		t.Fatalf("setup = %q", im.Text)
	}
	// Backspace at end-of-text eats the trailing newline first, then would
	// touch "omega"; walk back to just after the marker instead.
	im.CursorPos = len("alpha\n") + 1
	im.HandleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace}))
	if im.Text != "alpha\n\nomega" {
		t.Errorf("backspace should remove the whole marker, got %q", im.Text)
	}
	if len(im.pastes) != 0 {
		t.Errorf("buffer should prune the deleted paste, got %d", len(im.pastes))
	}
	_ = r
}

// --- mirror-field writes (the textinput-era API contract) ---------------------

// Direct writes to Text/CursorPos — the shape every pre-existing test and
// caller relies on — are honored by the engine on the next interaction.
func TestInput_MirrorFieldWrites(t *testing.T) {
	im := NewInputModel(nil, nil, nil, "")
	im.SetWidth(40)

	im.Text = "hello"
	im.CursorPos = 3
	im.HandleKey(tea.KeyPressMsg(tea.Key{Code: 'X', Text: "X"}))
	if im.Text != "helXlo" {
		t.Fatalf("text = %q, want %q", im.Text, "helXlo")
	}

	// Multi-line positioning through the same door.
	im.Clear()
	im.Text = "ab\ncd"
	im.CursorPos = 3 // start of line 2
	im.HandleKey(tea.KeyPressMsg(tea.Key{Code: 'Z', Text: "Z"}))
	if im.Text != "ab\nZcd" {
		t.Fatalf("text = %q, want %q", im.Text, "ab\nZcd")
	}
}

// Flat position and (line, column) agree in both directions, soft-wrap free:
// every line boundary contributes exactly one position for its newline.
func TestInput_FlatCursorRoundtrip(t *testing.T) {
	im := NewInputModel(nil, nil, nil, "")
	im.SetWidth(40)
	im.SetText("ab\ncd\nef")

	if got := im.CursorPos; got != 8 {
		t.Fatalf("end-of-text cursor = %d, want 8", got)
	}
	for pos, want := range map[int][2]int{
		0: {0, 0},
		2: {0, 2},
		3: {1, 0},
		4: {1, 1},
		6: {2, 0},
		8: {2, 2},
	} {
		// A write to the mirror field lands in the engine on the next
		// interaction; setFlatCursor is that push.
		im.CursorPos = pos
		im.setFlatCursor(im.CursorPos)
		if got := [2]int{im.input.Line(), im.input.Column()}; got != want {
			t.Errorf("pos %d → (line,col) = %v, want %v", pos, got, want)
		}
		if back := im.flatCursorPos(); back != pos {
			t.Errorf("pos %d → roundtrip = %d", pos, back)
		}
	}
}

// --- rendering invariants ------------------------------------------------------

// The welcome screen advertises the multiline affordance next to the @ hint.
func TestWelcome_ShiftEnterHint(t *testing.T) {
	cm := NewChatModel(nil)
	welcome := cm.renderWelcome(darkPalette)
	if !strings.Contains(ansi.Strip(welcome), "Shift+Enter") {
		t.Error("welcome should mention Shift+Enter for new lines")
	}
	if !strings.Contains(ansi.Strip(welcome), "to mention files") {
		t.Error("welcome should keep the @ mention hint")
	}
}

// The rendered value region stays ANSI-free around marker runes, so the
// placeholder replace remains exact on the multiline engine too. The prompt
// box adds a border row above and below; the assertions run on the content
// rows between them.
func TestRender_PromptOnFirstRowOnly(t *testing.T) {
	im := NewInputModel(nil, nil, nil, "")
	im.SetWidth(40)
	im.SetText("one\ntwo")

	view := im.View(false)
	plain := ansi.Strip(view)
	lines := strings.Split(plain, "\n")
	if len(lines) != 4 {
		t.Fatalf("rendered %d rows for a 2-line prompt, want 4 (2 content + 2 border)", len(lines))
	}
	// The left border glyph precedes the prompt on every content row.
	if !strings.HasPrefix(strings.TrimPrefix(lines[1], "│"), "> ") {
		t.Errorf("first content row = %q, want the > prompt", lines[1])
	}
	if strings.HasPrefix(strings.TrimPrefix(lines[2], "│"), "> ") {
		t.Errorf("continuation row must not repeat the > prompt: %q", lines[2])
	}
	if !strings.Contains(lines[2], "two") {
		t.Errorf("continuation row lost its text: %q", lines[2])
	}
}
