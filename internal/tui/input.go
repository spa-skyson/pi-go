package tui

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/spa-skyson/pi-rate/internal/extension"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// charOffsetToByteOffset converts a UTF-8 character offset within a string
// to a byte offset. Returns 0 if pos is out of bounds.
func charOffsetToByteOffset(s string, charPos int) int {
	if charPos <= 0 {
		return 0
	}
	byteOffset := 0
	for i := 0; i < charPos && byteOffset < len(s); {
		_, size := utf8.DecodeRuneInString(s[byteOffset:])
		if size == 0 {
			break
		}
		byteOffset += size
		i++
	}
	return byteOffset
}

// terminalResponseRe matches common terminal response fragments that leak
// through as text: CSI params (digits, semicolons, question marks) ending
// with a letter, DECRPM ($y), OSC color payloads (rgb:/hex colons+slashes),
// and cursor position reports.
var terminalResponseRe = regexp.MustCompile(
	`\[\d+;\d+[A-Z]` + // CSI CPR like [38;4R
		`|\d+\$[A-Za-z]` + // DECRPM tails like ;2$y
		`|[0-9a-f]{4}/[0-9a-f]{4}/[0-9a-f]{4}` + // hex triplet XXXX/XXXX/XXXX
		`|rgb:` + // OSC color payload
		`|\]\d+;`, // OSC intro like ]11;
)

// InputSubmitMsg is emitted when the user presses Enter with non-empty input.
type InputSubmitMsg struct {
	Text     string
	Mentions []string // file paths referenced via @path
}

const (
	// inputDefaultMaxHeight is the fallback visual-row cap used until the
	// root model wires the real one (a third of the terminal) via
	// SetMaxHeight. Bounds growth even for bare models in tests.
	inputDefaultMaxHeight = 10

	// inputPromptWidth is the cell width reserved for the prompt column.
	// "> " on the first line, two spaces on continuation lines.
	inputPromptWidth = 2
)

// inputPrompt renders the per-line prompt: the "> " glyph on the first
// display row and a blank indent on every continuation (and scrolled filler)
// row, so a multi-line prompt reads as one block.
func inputPrompt(pi textarea.PromptInfo) string {
	if pi.LineNumber == 0 {
		return "> "
	}
	return "  "
}

// InputModel wraps Bubble Tea's textarea component with history and
// slash-command support. The engine is multi-line: Enter submits,
// Shift+Enter inserts a newline, and the area grows to a visual-row cap
// (a third of the terminal) before scrolling internally.
//
// History is recorded here but not navigated here: the root model binds Up
// to the history window (see handleKey), so the input only sees Up/Down when
// the cursor sits on an inner row of a multi-line prompt.
//
// Text and CursorPos are mirrors of the engine state, refreshed after every
// edit (syncFromInput); ensureInput pushes them back before an edit, so
// writes to the fields take effect on the next interaction exactly as they
// did with the single-line engine.
type InputModel struct {
	Text      string
	CursorPos int // character position (not byte offset)
	History   []HistoryEntry

	// Dependencies (set by root model).
	Skills    []extension.Skill
	SkillDirs []string
	WorkDir   string

	// Palette is the resolved theme palette, set each frame by the model before
	// rendering. Zero means the dark default.
	Palette Palette

	// input text, while expandMarkers restores it on submit. See paste.go.
	pastes   map[rune]pasteRecord // marker rune → full pasted text
	pasteSeq rune                 // last issued marker number

	input textarea.Model

	// maxHeight is the visual-row cap handed to the engine by SetMaxHeight.
	maxHeight int

	// stylePaletteKey fingerprints the palette the input's prompt and cursor
	// styles were built from, so RefreshTheme can rebuild them on a theme
	// switch. The textarea bakes its styles in at construction, so unlike the
	// lipgloss chrome they do not follow Palette on their own.
	stylePaletteKey uint64
}

// NewInputModel creates an InputModel with initial state.
func NewInputModel(history []HistoryEntry, skills []extension.Skill, skillDirs []string, workDir string) InputModel {
	im := InputModel{
		History:   history,
		Skills:    skills,
		SkillDirs: skillDirs,
		WorkDir:   workDir,
	}
	im.ensureInput()
	return im
}

// HandleKey processes a key press for the input area.
// Returns a tea.Cmd (InputSubmitMsg on submit, nil otherwise).
func (im *InputModel) HandleKey(msg tea.KeyPressMsg) tea.Cmd {
	im.ensureInput()
	key := msg.Key()

	switch {
	case isLineStartKey(key):
		im.input.CursorStart()
		im.syncFromInput()
		return nil
	case isLineEndKey(key):
		im.input.CursorEnd()
		im.syncFromInput()
		return nil
	}

	switch key.Code {
	case tea.KeyEnter:
		if key.Mod&tea.ModShift != 0 {
			// Shift+Enter inserts a newline. The textarea's own keymap does
			// not know the chord (its Update treats it as a no-op), so the
			// insertion happens here, at the engine cursor.
			im.input.InsertString("\n")
			im.syncFromInput()
			return nil
		}
		// Enter submits. Expand first: submit, mentions and history all see
		// the full pasted text — the placeholder exists only in the rendering.
		text := strings.TrimSpace(im.expandMarkers(im.input.Value()))
		if text == "" {
			return nil
		}
		mentions := extractMentions(text)
		entry := HistoryEntry{Text: text, Mentions: mentions}
		if len(im.History) == 0 || im.History[len(im.History)-1].Text != text {
			im.History = append(im.History, entry)
			appendHistory(entry)
		}
		im.setValue("")
		return func() tea.Msg { return InputSubmitMsg{Text: text, Mentions: mentions} }
	}

	if key.Text != "" && !isUserInput(key.Text) {
		if isUserPaste(key.Text) {
			im.InsertText(key.Text)
		}
		return nil
	}

	var cmd tea.Cmd
	im.input, cmd = im.input.Update(msg)
	im.syncFromInput()
	return cmd
}

// SetWidth sets the visible width of the editable input text, excluding the prompt.
func (im *InputModel) SetWidth(width int) {
	im.ensureInput()
	if width < 0 {
		width = 0
	}
	im.input.SetWidth(width)
	im.syncFromInput()
}

// SetMaxHeight caps the input's rendered height at rows visual rows (soft
// wraps count). Content beyond the cap scrolls inside the textarea instead
// of growing the layout. The root model calls this with terminalHeight/3 on
// every resize and frame.
func (im *InputModel) SetMaxHeight(rows int) {
	im.ensureInput()
	if rows < 1 {
		rows = 1
	}
	if im.maxHeight == rows {
		return
	}
	im.maxHeight = rows
	im.input.MaxHeight = rows
	// Re-clamp the current height against the new cap and keep the cursor
	// inside the shrunken viewport.
	im.input.SetHeight(im.input.Height())
	im.syncFromInput()
}

// CursorOnFirstVisualRow reports whether the engine cursor sits on the very
// first display row of the prompt (top edge, soft wraps included). The root
// model gives Up to the history window only on that edge; any inner row
// keeps the arrow for cursor movement.
func (im *InputModel) CursorOnFirstVisualRow() bool {
	im.ensureInput()
	return im.input.Line() == 0 && im.input.LineInfo().RowOffset == 0
}

// CursorOnLastVisualRow reports whether the engine cursor sits on the very
// last display row of the prompt (bottom edge, soft wraps included). The
// root model gives Down to the chat scroll only on that edge.
func (im *InputModel) CursorOnLastVisualRow() bool {
	im.ensureInput()
	li := im.input.LineInfo()
	return im.input.Line() == im.input.LineCount()-1 && li.RowOffset >= li.Height-1
}

// View renders the input area.
func (im *InputModel) View(running bool) string {
	im.ensureInput()
	if running {
		p := paletteOrDark(im.Palette)
		prefix := lipgloss.NewStyle().
			Foreground(p.Primary).
			Bold(true).
			Render("> ")
		dim := lipgloss.NewStyle().Foreground(p.Dim)
		return prefix + dim.Render("(waiting for response...)")
	}
	view := im.input.View()
	// Collapsed pastes: swap marker runes for their styled labels. Zero cost
	// while no paste is collapsed; the full text never shows in the input.
	return renderPastePlaceholders(im, view)
}

// InsertText inserts pasted or programmatic text at cursor position. Text
// above a collapse threshold (pasteSummaryMinBytes / pasteSummaryMinLines)
// becomes a single marker rune in the value; the full text waits in the
// buffer and is restored on submit (expandMarkers) and in the Alt+V viewer.
//
// A collapsed paste never triggers the @-mention popup: the marker rune is
// not '@', so a paste that itself starts with "@…" loses the trigger into the
// buffer — the popup stays closed, which is the intended outcome. A typed '@'
// immediately before a paste leaves a one-rune "prefix" the popup can filter
// by; no file matches it, which is harmless.
func (im *InputModel) InsertText(text string) {
	im.ensureInput()
	insert := text
	if pasteNeedsSummary(text) {
		if r, ok := im.registerPaste(text); ok {
			insert = string(r)
		}
	}
	// The textarea sanitizer turns a lone \r into \n, which would double
	// every CRLF pair; normalize before the engine sees the text. Marker
	// runes are unaffected (PUA is printable), and the engine parks the
	// cursor right after the inserted text itself.
	insert = normalizePasteNewlines(insert)
	im.input.InsertString(insert)
	im.repositionEngine()
	im.syncFromInput()
}

// normalizePasteNewlines folds CRLF and lone CR into plain LF so the
// textarea's sanitizer cannot double line breaks in pasted text.
func normalizePasteNewlines(s string) string {
	if !strings.ContainsRune(s, '\r') {
		return s
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

// Clear resets the input text and cursor.
func (im *InputModel) Clear() {
	im.ensureInput()
	im.setValue("")
}

// SetText replaces the input text and moves the cursor to the end.
func (im *InputModel) SetText(text string) {
	im.ensureInput()
	im.setValue(text)
	im.input.CursorEnd()
	im.repositionEngine()
	im.syncFromInput()
}

// ReloadSkills re-scans skill directories from disk and updates the cached list.
func (im *InputModel) ReloadSkills() {
	if len(im.SkillDirs) > 0 {
		if fresh, err := extension.LoadSkills(im.SkillDirs...); err == nil {
			im.Skills = fresh
		}
	}
}

// AllCommandNames returns a sorted list of all command names: built-in + skills.
func (im *InputModel) AllCommandNames() []string {
	seen := make(map[string]bool)
	var cmds []string
	for _, cmd := range slashCommands {
		if !seen[cmd] {
			seen[cmd] = true
			cmds = append(cmds, cmd)
		}
	}
	for _, skill := range im.Skills {
		name := "/" + skill.Name
		if !seen[name] {
			seen[name] = true
			cmds = append(cmds, name)
		}
	}
	sort.Strings(cmds)
	return cmds
}

// Cursor returns the real Bubble Tea cursor for the input's current position.
func (im *InputModel) Cursor() *tea.Cursor {
	im.ensureInput()
	return im.input.Cursor()
}

// applyPaletteStyles paints the text input's prompt and cursor from the current
// palette and records which palette they came from. The value region is
// deliberately left style-free: renderPastePlaceholders does a plain string
// replace over the rendered view, which is only exact when marker runes appear
// verbatim in the output.
func (im *InputModel) applyPaletteStyles() {
	p := paletteOrDark(im.Palette)
	promptStyle := lipgloss.NewStyle().
		Foreground(p.Primary).
		Bold(true)
	styles := im.input.Styles()
	styles.Focused.Prompt = promptStyle
	styles.Blurred.Prompt = promptStyle
	zero := lipgloss.NewStyle()
	styles.Focused.CursorLine = zero
	styles.Blurred.CursorLine = zero
	styles.Focused.Text = zero
	styles.Blurred.Text = zero
	styles.Focused.Base = zero
	styles.Blurred.Base = zero
	styles.Focused.EndOfBuffer = zero
	styles.Blurred.EndOfBuffer = zero
	styles.Focused.Placeholder = zero
	styles.Blurred.Placeholder = zero
	styles.Focused.LineNumber = zero
	styles.Blurred.LineNumber = zero
	styles.Focused.CursorLineNumber = zero
	styles.Blurred.CursorLineNumber = zero
	styles.Focused.Selection = zero
	styles.Blurred.Selection = zero
	styles.Cursor.Color = p.Primary
	styles.Cursor.Shape = tea.CursorBar
	im.input.SetStyles(styles)
	im.stylePaletteKey = paletteKey(p)
}

// RefreshTheme repaints the input's prompt and cursor when the palette has
// changed since they were built, and reports whether it did.
func (im *InputModel) RefreshTheme() bool {
	if im.input.KeyMap.CharacterForward.Keys() == nil {
		// Not constructed yet; ensureInput will pick up the current palette.
		return false
	}
	if im.stylePaletteKey == paletteKey(paletteOrDark(im.Palette)) {
		return false
	}
	im.applyPaletteStyles()
	return true
}

func (im *InputModel) ensureInput() {
	if im.input.KeyMap.CharacterForward.Keys() == nil {
		ta := textarea.New()
		ta.ShowLineNumbers = false
		ta.Placeholder = ""
		ta.EndOfBufferCharacter = ' '
		ta.CharLimit = 0
		ta.MaxWidth = 0
		ta.MaxContentHeight = 0 // never block input; the cap only bounds the view
		ta.DynamicHeight = true
		ta.MinHeight = 1
		ta.MaxHeight = inputDefaultMaxHeight
		ta.SetPromptFunc(inputPromptWidth, inputPrompt)
		ta.SetVirtualCursor(false)
		// Enter is owned by HandleKey (submit / Shift+Enter newline); the
		// engine must never insert a newline on its own. Bracketed paste is
		// the only paste path — it routes through InsertText so large pastes
		// collapse into markers — so the engine's ctrl+v clipboard binding
		// is off.
		ta.KeyMap.InsertNewline.SetEnabled(false)
		ta.KeyMap.Paste.SetEnabled(false)
		ta.SetHeight(1)
		im.input = ta
		im.maxHeight = inputDefaultMaxHeight
		im.applyPaletteStyles()
		_ = im.input.Focus()
	}
	if im.input.Value() != im.Text {
		im.input.SetValue(im.Text)
	}
	im.setFlatCursor(im.CursorPos)
	im.syncFromInput()
}

func (im *InputModel) setValue(text string) {
	im.input.SetValue(text)
	im.syncFromInput()
}

func (im *InputModel) syncFromInput() {
	im.Text = im.input.Value()
	im.CursorPos = im.flatCursorPos()
	// Editing keys that bypass InsertText (backspace on the marker rune,
	// ctrl+u, SetText) can drop markers from the value; drop their buffer
	// entries with them so the buffer never outlives its placeholders.
	im.prunePastes()
}

// flatCursorPos converts the engine's (line, column) cursor into the flat
// character position the mirror field exposes: every line before the cursor
// contributes its runes plus one for its newline.
func (im *InputModel) flatCursorPos() int {
	pos := im.input.Column()
	line := im.input.Line()
	if line == 0 {
		return pos
	}
	for i, l := range strings.Split(im.input.Value(), "\n") {
		if i >= line {
			break
		}
		pos += utf8.RuneCountInString(l) + 1
	}
	return pos
}

// setFlatCursor moves the engine cursor to the flat character position pos.
// The textarea has no flat-position API, so the walk goes MoveToBegin and
// then down one visual row at a time until the target logical line is
// reached (every row crossing lands at column 0), with SetCursorColumn
// fixing the final offset. A no-op when the cursor is already there, which
// is the steady state in production — the mirror and the engine agree after
// every edit.
func (im *InputModel) setFlatCursor(pos int) {
	if im.input.Value() == "" || im.flatCursorPos() == pos {
		return
	}
	lines := strings.Split(im.input.Value(), "\n")
	if pos < 0 {
		pos = 0
	}
	row, col := len(lines)-1, 0
	found := false
	for i, l := range lines {
		w := utf8.RuneCountInString(l)
		if pos <= w {
			row, col, found = i, pos, true
			break
		}
		pos -= w + 1
	}
	if !found {
		// pos past the end: last line, remainder as the column;
		// SetCursorColumn clamps it to the line length.
		col = max(0, pos)
	}
	ta := &im.input
	ta.MoveToBegin()
	// Soft wraps make one logical line span several visual rows; walk until
	// the logical line index matches. The bound is generous (wraps can never
	// exceed the rune count) and only guards a stuck loop on the last line.
	for steps := utf8.RuneCountInString(im.Text) + len(lines) + 1; ta.Line() < row && steps > 0; steps-- {
		ta.CursorDown()
	}
	ta.SetCursorColumn(col)
	im.repositionEngine()
}

// repositionEngine re-runs the engine's update tail after a programmatic
// edit or cursor walk: SetValue/InsertString and the cursor primitives move
// the cursor but leave the viewport scrolled to its old window, and the
// scroll itself only re-anchors once the viewport content is rebuilt — which
// happens inside Update. An empty key press matches no binding, so the call
// is exactly that tail: recalculate height, rebuild content, keep the cursor
// in view.
func (im *InputModel) repositionEngine() {
	im.input, _ = im.input.Update(tea.KeyPressMsg{})
}

func isLineStartKey(key tea.Key) bool {
	return key.Code == tea.KeyHome ||
		(key.Code == 'a' && key.Mod == tea.ModCtrl) ||
		key.Code == 0x01
}

func isLineEndKey(key tea.Key) bool {
	return key.Code == tea.KeyEnd ||
		(key.Code == 'e' && key.Mod == tea.ModCtrl) ||
		key.Code == 0x05
}

// completeSlashCommand returns the best matching slash command for the current input.
// Only suggests completions when at least 2 characters have been typed after '/'.
func completeSlashCommand(input string) string {
	if !strings.HasPrefix(input, "/") || len(input) < 3 {
		return ""
	}
	prefix := strings.ToLower(input)
	for _, cmd := range slashCommands {
		if strings.HasPrefix(cmd, prefix) && cmd != prefix {
			return cmd
		}
	}
	return ""
}

// matchingSlashCommands returns all slash commands matching the given prefix.
func matchingSlashCommands(input string) []string {
	prefix := strings.ToLower(input)
	var matches []string
	for _, cmd := range slashCommands {
		if strings.HasPrefix(cmd, prefix) {
			matches = append(matches, cmd)
		}
	}
	return matches
}

// isUserInput returns true if the string represents genuine user keyboard input.
// Real keyboard input via KeyPressMsg is always a single rune. Multi-character
// text values are terminal response fragments (CSI, OSC, DECRPM) that leaked
// through Bubble Tea's parser when escape sequences get split at arbitrary
// byte boundaries during resize or color queries.
func isUserInput(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsPrint(r) {
			return false
		}
	}
	// Real keystrokes produce exactly one rune. Multi-char text in a
	// KeyPressMsg is always terminal response garbage. Actual multi-char
	// input (paste) arrives via PasteMsg which is filtered separately.
	if utf8.RuneCountInString(s) > 1 {
		return false
	}
	return true
}

// isUserPaste returns true if a PasteMsg contains real pasted text rather than
// terminal response sequences that were misidentified as bracketed paste.
func isUserPaste(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsPrint(r) && r != '\n' && r != '\r' && r != '\t' {
			return false
		}
	}
	return !terminalResponseRe.MatchString(s)
}
