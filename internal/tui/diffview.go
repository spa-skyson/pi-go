package tui

import (
	"fmt"
	"image/color"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/spa-skyson/pi-rate/internal/tools"
)

// Fullscreen diff viewer (issue #25): every changed file of a scope with its
// unified diff, painted over the chat like the subagent and paste viewers.
//
// Scopes are honest about what the first version shows: "Working tree"
// (uncommitted: worktree + index vs HEAD) and "Last commit" (HEAD vs HEAD~1).
// A true last-agent-turn diff needs turn boundaries the session does not
// record yet.
//
// Data comes from the shared git plumbing in internal/tools/gitdiff.go —
// the same exec site and the same parser the git agent tools use; nothing
// here spawns git or parses diffs itself. Data loads once per open and per
// scope switch; rendering only ever styles the visible window of rows, so a
// multi-thousand-line diff never builds a giant styled string per frame.

// diffViewerChrome is the rows the viewer spends on its own chrome: two
// border rows, the header, the file strip, and the hint line.
const diffViewerChrome = 5

// diffViewerFile is one changed file: path, single-letter status, and its
// parsed hunks with per-file add/remove totals for the header.
type diffViewerFile struct {
	name    string
	status  string
	hunks   []tools.DiffHunk
	added   int
	removed int
}

// diffViewerRow is one renderable row of the current file's diff: a hunk
// header or a classified content line. hdr rows are the ]/[ jump targets.
type diffViewerRow struct {
	kind tools.DiffLineKind
	text string
	hdr  bool
}

// diffViewerState is the open viewer. line is the cursor row within the
// current file's rows; scroll is the first visible row — the cursor is kept
// inside [scroll, scroll+bodyH) by ensureVisible.
type diffViewerState struct {
	scope  tools.DiffScope
	files  []diffViewerFile
	err    error
	file   int // current file index
	line   int // cursor row within the current file
	scroll int // first visible row

	// rows is the current file flattened; rowsFor is the file index it was
	// built for. Rebuilt lazily on file switch, never per frame.
	rows    []diffViewerRow
	rowsFor int
}

// currentRows returns the flattened rows of the current file, rebuilding the
// cache after a file switch.
func (v *diffViewerState) currentRows() []diffViewerRow {
	if v.rowsFor != v.file || v.rows == nil {
		v.rebuildRows()
	}
	return v.rows
}

// rebuildRows flattens the current file's hunks into rows. Always leaves a
// non-nil slice, so an empty file is built once and not rebuilt every frame.
func (v *diffViewerState) rebuildRows() {
	rows := make([]diffViewerRow, 0, 16)
	v.rowsFor = v.file
	if v.file >= 0 && v.file < len(v.files) {
		f := v.files[v.file]
		for _, h := range f.hunks {
			rows = append(rows, diffViewerRow{text: h.Header, hdr: true})
			for _, l := range h.Lines {
				rows = append(rows, diffViewerRow{kind: l.Kind, text: l.Text})
			}
		}
	}
	v.rows = rows
}

// moveCursor moves the cursor by delta rows and keeps it in the window.
func (v *diffViewerState) moveCursor(delta, bodyH int) {
	if len(v.currentRows()) == 0 {
		return
	}
	v.line = min(max(v.line+delta, 0), len(v.rows)-1)
	v.ensureVisible(bodyH)
}

// switchFile steps to the next/previous file, resetting the cursor to its top.
func (v *diffViewerState) switchFile(delta int) {
	if len(v.files) == 0 {
		return
	}
	next := min(max(v.file+delta, 0), len(v.files)-1)
	if next == v.file {
		return
	}
	v.file = next
	v.line, v.scroll = 0, 0
	v.rebuildRows()
}

// jumpHunk moves the cursor to the nearest hunk header in delta direction;
// at the bounds it stays put.
func (v *diffViewerState) jumpHunk(delta, bodyH int) {
	rows := v.currentRows()
	for i := v.line + delta; i >= 0 && i < len(rows); i += delta {
		if rows[i].hdr {
			v.line = i
			v.ensureVisible(bodyH)
			return
		}
	}
}

// ensureVisible shifts the window so the cursor row is on screen.
func (v *diffViewerState) ensureVisible(bodyH int) {
	if v.line < v.scroll {
		v.scroll = v.line
	}
	if v.line >= v.scroll+bodyH {
		v.scroll = v.line - bodyH + 1
	}
	v.scroll = max(v.scroll, 0)
}

// openDiffViewer handles /diff: opens the viewer on the working tree and
// loads its data. A failed load (not a repo, git error) opens anyway — the
// body explains what went wrong.
func (m *model) openDiffViewer() (tea.Model, tea.Cmd) {
	m.diffViewer = &diffViewerState{scope: tools.DiffWorkingTree}
	m.diffViewerReload()
	m.openOverlay(overlayDiffViewer)
	return m, nil
}

// diffViewerReload reloads the current scope's files from git and resets the
// cursor. One DiffFiles call plus one git call per file; the results are the
// viewer's data for as long as it stays open.
func (m *model) diffViewerReload() {
	v := m.diffViewer
	if v == nil {
		return
	}
	v.file, v.line, v.scroll = 0, 0, 0
	v.rows, v.rowsFor = nil, 0

	dir := m.cwd()
	files, err := tools.DiffFiles(dir, v.scope)
	if err != nil {
		v.err = err
		v.files = nil
		return
	}
	v.err = nil
	out := make([]diffViewerFile, 0, len(files))
	for _, st := range files {
		f := diffViewerFile{name: st.File, status: st.Status}
		if patch, err := tools.DiffPatchFor(dir, st, v.scope); err == nil {
			f.hunks = tools.ParseUnifiedDiff(patch)
			for _, h := range f.hunks {
				f.added += h.Added
				f.removed += h.Removed
			}
		}
		out = append(out, f)
	}
	v.files = out
}

// diffViewerToggleScope flips Working tree ↔ Last commit and reloads.
func (m *model) diffViewerToggleScope() {
	if m.diffViewer == nil {
		return
	}
	if m.diffViewer.scope == tools.DiffWorkingTree {
		m.diffViewer.scope = tools.DiffLastCommit
	} else {
		m.diffViewer.scope = tools.DiffWorkingTree
	}
	m.diffViewerReload()
}

// handleDiffViewerKey drives the fullscreen diff viewer. Owned keys: q/Esc
// close it, j/k (arrows) move the line cursor, n/p switch files, ]/[ jump
// between hunk headers, g/G jump to top/bottom, s toggles the scope, PgUp/
// PgDn page. Everything else is swallowed — the prompt below must not
// collect invisible keystrokes — except Ctrl+C, which falls through to the
// interrupt layer like the other fullscreen viewers.
func (m *model) handleDiffViewerKey(key tea.Key) (tea.Model, tea.Cmd, bool) {
	if m.diffViewer == nil {
		return nil, nil, false
	}
	if key.Code == 'c' && key.Mod == tea.ModCtrl {
		return nil, nil, false
	}
	v := m.diffViewer
	bodyH := m.diffViewerBodyH()
	switch {
	case key.Code == tea.KeyEsc, key.Code == 'q' && key.Mod == 0:
		m.popOverlay(overlayDiffViewer)
	case key.Code == 'j' && key.Mod == 0, key.Code == tea.KeyDown:
		v.moveCursor(1, bodyH)
	case key.Code == 'k' && key.Mod == 0, key.Code == tea.KeyUp:
		v.moveCursor(-1, bodyH)
	case key.Code == 'n' && key.Mod == 0:
		v.switchFile(1)
	case key.Code == 'p' && key.Mod == 0:
		v.switchFile(-1)
	case key.Code == ']' && key.Mod == 0:
		v.jumpHunk(1, bodyH)
	case key.Code == '[' && key.Mod == 0:
		v.jumpHunk(-1, bodyH)
	case key.Code == 'g' && key.Mod == 0:
		v.line = 0
		v.ensureVisible(bodyH)
	case key.Code == 'G' && key.Mod == 0:
		if rows := v.currentRows(); len(rows) > 0 {
			v.line = len(rows) - 1
			v.ensureVisible(bodyH)
		}
	case key.Code == 's' && key.Mod == 0:
		m.diffViewerToggleScope()
	case key.Code == tea.KeyPgDown:
		v.moveCursor(bodyH, bodyH)
	case key.Code == tea.KeyPgUp:
		v.moveCursor(-bodyH, bodyH)
	}
	return m, nil, true
}

// diffViewerBodyH is the body height the viewer has inside the message
// viewport — the same budget the overlay renders with.
func (m *model) diffViewerBodyH() int {
	return max(1, m.messageViewportHeight()-diffViewerChrome)
}

// overlayDiffViewer paints the viewer over the message viewport when open.
// Same paint scheme as the other fullscreen viewers: a full-height bordered
// box overwriting the message rows; the transcript underneath is untouched,
// so closing restores the chat byte-for-byte.
func (m *model) overlayDiffViewer(messages string, bodyWidth int) string {
	if m.diffViewer == nil {
		return messages
	}
	box := m.renderDiffViewer(bodyWidth, len(strings.Split(messages, "\n")))
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

// diffCut clips s to at most n runes, replacing the last visible rune with
// '›' when it cuts. Diffs read with a cut marker better than with "..." —
// the chevron says "more to the right", not "text omitted here".
func diffCut(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return "›"
	}
	return string(r[:n-1]) + "›"
}

// renderDiffViewer builds the fullscreen box: header (scope, file counts,
// totals), the file strip, the visible window of the current file's diff with
// the cursor highlighted, and the hint line.
func (m *model) renderDiffViewer(width, viewport int) string {
	v := m.diffViewer
	p := paletteOrDark(m.palette)
	innerW := max(10, width-2)
	bodyH := max(1, viewport-diffViewerChrome)

	// Diff colors come from the theme's diff roles, falling back to the
	// palette's green/red when no theme manager is set (tests, bare models).
	addedC, removedC := p.Green, p.Red
	if m.themeManager != nil {
		c := m.themeManager.Colors()
		addedC, removedC = c.DiffAddedTextColor(), c.DiffRemovedTextColor()
	}
	addStyle := lipgloss.NewStyle().Foreground(addedC)
	delStyle := lipgloss.NewStyle().Foreground(removedC)
	ctxStyle := lipgloss.NewStyle().Foreground(p.DiffContext)
	metaStyle := lipgloss.NewStyle().Foreground(p.DiffHunkHeader)
	hdrStyle := lipgloss.NewStyle().Foreground(p.Primary)

	// Header: scope, file count, current file with status, add/remove totals.
	// Cut ANSI-aware to the inner width — a wrapped header would push body
	// rows out of the box.
	scopeStyle := lipgloss.NewStyle().Foreground(p.Primary).Bold(true)
	var header string
	switch {
	case v.err != nil:
		header = scopeStyle.Render(v.scope.Label()) + " · " +
			lipgloss.NewStyle().Foreground(p.Error).Render("ошибка загрузки")
	case len(v.files) == 0:
		header = scopeStyle.Render(v.scope.Label()) + " · нет изменений"
	default:
		f := v.files[v.file]
		stStyle := lipgloss.NewStyle().Foreground(diffStatusColor(p, f.status)).Bold(true)
		header = scopeStyle.Render(v.scope.Label()) +
			fmt.Sprintf(" · %d %s · файл %d/%d · ", len(v.files), diffPluralFiles(len(v.files)), v.file+1, len(v.files)) +
			stStyle.Render(f.status+" "+f.name) +
			fmt.Sprintf(" · +%d −%d", f.added, f.removed)
	}
	header = ansi.Truncate(header, innerW, "›")

	// File strip: every file as "status name", the current one highlighted.
	strip := ""
	if len(v.files) > 0 {
		parts := make([]string, 0, len(v.files))
		for i, f := range v.files {
			st := lipgloss.NewStyle().Foreground(diffStatusColor(p, f.status))
			name := st.Render(f.status + " " + f.name)
			if i == v.file {
				name = lipgloss.NewStyle().Bold(true).Reverse(true).Render(name)
			}
			parts = append(parts, name)
		}
		strip = ansi.Truncate(strings.Join(parts, " · "), innerW, "›")
	}

	// Body: empty states first, then only the visible window of rows.
	var body []string
	switch {
	case v.err != nil:
		body = []string{
			lipgloss.NewStyle().Foreground(p.Error).Render(diffCut("Не удалось загрузить дифф: "+v.err.Error(), innerW)),
			lipgloss.NewStyle().Foreground(p.Faint).Render("q — закрыть"),
		}
	case len(v.files) == 0:
		body = []string{
			lipgloss.NewStyle().Foreground(p.Dim).Render("Нет изменений (" + v.scope.Label() + ")"),
			lipgloss.NewStyle().Foreground(p.Faint).Render("s — переключить скоуп · q — закрыть"),
		}
	default:
		rows := v.currentRows()
		start := min(v.scroll, max(0, len(rows)-bodyH))
		end := min(len(rows), start+bodyH)
		maxText := innerW - 2 // room for the two-cell cursor gutter
		for i := start; i < end; i++ {
			body = append(body, renderDiffRow(rows[i], i == v.line, maxText, addStyle, delStyle, ctxStyle, metaStyle, hdrStyle))
		}
		if fh := v.files[v.file]; len(fh.hunks) == 0 && len(body) == 0 {
			body = append(body, metaStyle.Render(diffCut("(бинарный файл или нет текстовых изменений)", innerW)))
		}
	}

	var b strings.Builder
	b.WriteString(header)
	b.WriteString("\n")
	if strip != "" {
		b.WriteString(strip)
		b.WriteString("\n")
	}
	for _, line := range body {
		b.WriteString(line)
		b.WriteString("\n")
	}
	// Pad the body window to its full height so the hint sits on the bottom
	// row of the box regardless of how much of the file is visible.
	for i := len(body); i < bodyH; i++ {
		b.WriteString("\n")
	}

	hint := "j/k строки · n/p файлы · ]/[ хунки · g/G верх/низ · s скоуп · q выход"
	rows := v.currentRows()
	if len(rows) > 0 {
		hint += fmt.Sprintf(" · строка %d из %d", v.line+1, len(rows))
	}
	b.WriteString(lipgloss.NewStyle().Foreground(p.Faint).Render(diffCut(hint, innerW)))

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder(), true, true, true, true).
		BorderForeground(p.Primary).
		Width(width).
		Render(b.String())
}

// renderDiffRow styles one row: the display text is the raw diff line minus
// its leading prefix, cut to maxText runes; the cursor row is reversed.
func renderDiffRow(row diffViewerRow, cursor bool, maxText int, add, del, ctx, meta, hdr lipgloss.Style) string {
	style := hdr
	text := row.text
	switch {
	case row.hdr:
	case row.kind == tools.DiffLineAdd:
		style = add
		text = diffPrefixTrim(text)
	case row.kind == tools.DiffLineRemove:
		style = del
		text = diffPrefixTrim(text)
	case row.kind == tools.DiffLineMeta:
		style = meta
	default:
		style = ctx
		text = diffPrefixTrim(text)
	}
	if cursor {
		style = style.Reverse(true)
	}
	return style.Render(diffCut(text, maxText))
}

// diffPrefixTrim drops the diff marker column (the leading +, - or space) so
// the row renders without a redundant gutter.
func diffPrefixTrim(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	return string(r[1:])
}

// diffPluralFiles picks the Russian noun form for a file count:
// 1 файл / 2–4 файла / 5+ файлов, with 11–14 taking the genitive plural.
func diffPluralFiles(n int) string {
	n %= 100
	if n >= 11 && n <= 14 {
		return "файлов"
	}
	switch n % 10 {
	case 1:
		return "файл"
	case 2, 3, 4:
		return "файла"
	default:
		return "файлов"
	}
}

// diffStatusColor maps a git status letter to a palette color.
func diffStatusColor(p Palette, status string) color.Color {
	switch status {
	case "A":
		return p.Green
	case "D":
		return p.Red
	case "M", "T":
		return p.Yellow
	case "R":
		return p.Mauve
	case "?":
		return p.Cyan
	default:
		return p.Text
	}
}
