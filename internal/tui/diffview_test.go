package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/spa-skyson/pi-rate/internal/tools"
)

// --- fixtures ---

// diffRepoInit creates a temp git repo with signing off (mirrors the tools
// package's initGitRepo: a developer-global commit.gpgsign would otherwise
// route every fixture commit through 1Password and time out).
func diffRepoInit(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %s: %v", strings.Join(args, " "), out, err)
		}
	}

	run("init")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "Test")
	run("config", "commit.gpgsign", "false")
	run("config", "tag.gpgsign", "false")
	return dir
}

// diffRepoWrite writes a file and stages it.
func diffRepoWrite(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", dir, "add", name).CombinedOutput(); err != nil {
		t.Fatalf("git add %s: %s: %v", name, out, err)
	}
}

// diffMockFiles builds two files: the first with two hunks (so ]/[ has a
// target), the second with one.
func diffMockFiles() []diffViewerFile {
	return []diffViewerFile{
		{
			name:   "first.go",
			status: "M",
			hunks: []tools.DiffHunk{
				{
					Header: "@@ -1,3 +1,4 @@",
					Lines: []tools.DiffLine{
						{Kind: tools.DiffLineContext, Text: " context one"},
						{Kind: tools.DiffLineAdd, Text: "+added one"},
						{Kind: tools.DiffLineRemove, Text: "-removed one"},
					},
					Added: 1, Removed: 1,
				},
				{
					Header: "@@ -10,2 +11,3 @@",
					Lines: []tools.DiffLine{
						{Kind: tools.DiffLineContext, Text: " context two"},
						{Kind: tools.DiffLineAdd, Text: "+added two"},
					},
					Added: 1,
				},
			},
			added: 2, removed: 1,
		},
		{
			name:   "second.go",
			status: "A",
			hunks: []tools.DiffHunk{
				{
					Header: "@@ -0,0 +1,1 @@",
					Lines:  []tools.DiffLine{{Kind: tools.DiffLineAdd, Text: "+second file line"}},
					Added:  1,
				},
			},
			added: 1,
		},
	}
}

// diffViewerModel is a model with mock viewer data attached (no git involved).
func diffViewerModel(t *testing.T) *model {
	t.Helper()
	m := newTestModelFull(t)
	m.diffViewer = &diffViewerState{scope: tools.DiffWorkingTree, files: diffMockFiles()}
	return m
}

// diffKey builds a plain printable key press.
func diffKey(r rune) tea.Key {
	return tea.Key{Code: r}
}

// diffBodyStart is the output row the first body line lands on in
// renderDiffViewer: border, header, file strip, then the body.
const diffBodyStart = 3

// fgOf returns the foreground SGR sequence a rendered box row opens its
// CONTENT with. Every row starts with the border's own SGR (the '│' glyph is
// drawn in the border color), so the first sequence is skipped.
func diffFGOf(line string) string {
	rest := line
	if i := strings.Index(rest, "\x1b[m"); i >= 0 {
		rest = rest[i+3:]
	}
	if i := strings.Index(rest, "m"); i > 0 && strings.HasPrefix(rest, "\x1b[") {
		return rest[:i+1]
	}
	return ""
}

// --- opening ---

func TestDiffViewer_OpenCommand_LoadsFiles(t *testing.T) {
	dir := diffRepoInit(t)
	diffRepoWrite(t, dir, "a.txt", "one\n")
	gitTuiCommit(t, dir, "c1")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := newTestModelFull(t)
	m.cfg.WorkDir = dir
	m.handleSlashCommand("/diff")

	if m.diffViewer == nil {
		t.Fatal("diffViewer should be open after /diff")
	}
	if !m.hasOverlay(overlayDiffViewer) {
		t.Error("viewer should be pushed onto the overlay stack")
	}
	if m.diffViewer.scope != tools.DiffWorkingTree {
		t.Errorf("scope = %v, want working tree", m.diffViewer.scope)
	}
	if len(m.diffViewer.files) != 1 || m.diffViewer.files[0].name != "a.txt" {
		t.Fatalf("files = %+v, want one entry for a.txt", m.diffViewer.files)
	}
	f := m.diffViewer.files[0]
	if f.status != "M" {
		t.Errorf("status = %q, want M", f.status)
	}
	if len(f.hunks) != 1 || f.hunks[0].Added != 1 {
		t.Errorf("hunks = %+v, want one hunk with one added line", f.hunks)
	}
}

// gitTuiCommit commits in a fixture repo with a fixed identity.
func gitTuiCommit(t *testing.T, dir, msg string) {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "commit", "-m", msg)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %s: %v", out, err)
	}
}

func TestDiffViewer_OpenOutsideRepo_ShowsError(t *testing.T) {
	m := newTestModelFull(t)
	m.cfg.WorkDir = t.TempDir()
	m.handleSlashCommand("/diff")

	if m.diffViewer == nil {
		t.Fatal("viewer should open even when the load fails")
	}
	if m.diffViewer.err == nil {
		t.Error("expected a load error outside a repository")
	}
	out := m.renderDiffViewer(80, 30)
	if !strings.Contains(ansi.Strip(out), "Не удалось загрузить дифф") {
		t.Errorf("render should explain the load failure, got:\n%s", ansi.Strip(out))
	}
}

// --- line navigation (j/k, g/G) ---

func TestDiffViewer_Navigation_Lines(t *testing.T) {
	m := diffViewerModel(t)
	v := m.diffViewer
	bodyH := m.diffViewerBodyH()

	// A file taller than the window, so scrolling is observable.
	const total = 300
	lines := make([]tools.DiffLine, 0, total)
	for i := range total {
		lines = append(lines, tools.DiffLine{Kind: tools.DiffLineContext, Text: fmt.Sprintf(" row %d", i)})
	}
	v.files = []diffViewerFile{{
		name:   "tall.go",
		status: "M",
		hunks:  []tools.DiffHunk{{Header: "@@ -1 +1 @@", Lines: lines}},
	}}
	v.file, v.line, v.scroll, v.rows, v.rowsFor = 0, 0, 0, nil, -1
	rows := v.currentRows()
	if bodyH >= len(rows) {
		t.Fatalf("test misconfigured: bodyH %d covers all %d rows", bodyH, len(rows))
	}

	// j walks down and keeps the cursor inside the window.
	m.handleDiffViewerKey(diffKey('j'))
	m.handleDiffViewerKey(diffKey('j'))
	if v.line != 2 {
		t.Errorf("after two j, line = %d, want 2", v.line)
	}
	if v.line < v.scroll || v.line >= v.scroll+bodyH {
		t.Errorf("cursor %d outside window [%d,%d)", v.line, v.scroll, v.scroll+bodyH)
	}

	// k walks back up and stops at the top.
	m.handleDiffViewerKey(diffKey('k'))
	if v.line != 1 {
		t.Errorf("after k, line = %d, want 1", v.line)
	}
	for range len(rows) + 5 {
		m.handleDiffViewerKey(diffKey('k'))
	}
	if v.line != 0 || v.scroll != 0 {
		t.Errorf("k should clamp at the top, got line=%d scroll=%d", v.line, v.scroll)
	}

	// G jumps to the last row and the window follows.
	m.handleDiffViewerKey(diffKey('G'))
	if v.line != len(rows)-1 {
		t.Errorf("after G, line = %d, want %d", v.line, len(rows)-1)
	}
	if v.scroll != v.line-bodyH+1 {
		t.Errorf("after G, scroll = %d, want %d", v.scroll, v.line-bodyH+1)
	}

	// g returns to the top.
	m.handleDiffViewerKey(diffKey('g'))
	if v.line != 0 || v.scroll != 0 {
		t.Errorf("after g, line=%d scroll=%d, want 0/0", v.line, v.scroll)
	}
}

// --- file navigation (n/p) ---

func TestDiffViewer_Navigation_Files(t *testing.T) {
	m := diffViewerModel(t)
	v := m.diffViewer

	// Position the cursor mid-file: switching files resets it.
	m.handleDiffViewerKey(diffKey('j'))
	m.handleDiffViewerKey(diffKey('n'))
	if v.file != 1 {
		t.Errorf("after n, file = %d, want 1", v.file)
	}
	if v.line != 0 || v.scroll != 0 {
		t.Errorf("file switch should reset the cursor, got line=%d scroll=%d", v.line, v.scroll)
	}
	if v.files[v.file].name != "second.go" {
		t.Errorf("file = %q, want second.go", v.files[v.file].name)
	}

	// n at the last file stays put.
	m.handleDiffViewerKey(diffKey('n'))
	if v.file != 1 {
		t.Errorf("n at the last file should not move, file = %d", v.file)
	}

	// p walks back.
	m.handleDiffViewerKey(diffKey('p'))
	if v.file != 0 {
		t.Errorf("after p, file = %d, want 0", v.file)
	}

	// p at the first file stays put.
	m.handleDiffViewerKey(diffKey('p'))
	if v.file != 0 {
		t.Errorf("p at the first file should not move, file = %d", v.file)
	}
}

// --- hunk jumps (]/[) ---

func TestDiffViewer_Navigation_Hunks(t *testing.T) {
	m := diffViewerModel(t)
	v := m.diffViewer
	rows := v.currentRows()

	// From the top, ] lands on the first hunk header (row 0)…
	m.handleDiffViewerKey(diffKey(']'))
	if !rows[v.line].hdr {
		t.Fatalf("] from the top should land on hunk 0 header, line = %d", v.line)
	}
	// …and a second ] lands on the second hunk's header.
	m.handleDiffViewerKey(diffKey(']'))
	if !rows[v.line].hdr || v.line == 0 {
		t.Errorf("second ] should land on hunk 1 header, line = %d", v.line)
	}

	// [ walks back to hunk 0.
	m.handleDiffViewerKey(diffKey('['))
	if v.line != 0 {
		t.Errorf("[ should return to hunk 0, line = %d", v.line)
	}

	// [ before the first hunk stays put.
	m.handleDiffViewerKey(diffKey('['))
	if v.line != 0 {
		t.Errorf("[ at the first hunk should not move, line = %d", v.line)
	}

	// ] after the last hunk stays put.
	m.handleDiffViewerKey(diffKey('G'))
	m.handleDiffViewerKey(diffKey(']'))
	if v.line != len(rows)-1 {
		t.Errorf("] at the bottom should not move, line = %d, want %d", v.line, len(rows)-1)
	}
}

// --- scope toggle (s) ---

func TestDiffViewer_ScopeToggle(t *testing.T) {
	dir := diffRepoInit(t)
	diffRepoWrite(t, dir, "a.txt", "one\n")
	gitTuiCommit(t, dir, "c1")
	diffRepoWrite(t, dir, "a.txt", "one\ntwo\n")
	diffRepoWrite(t, dir, "b.txt", "fresh\n")
	gitTuiCommit(t, dir, "c2")
	// Uncommitted working-tree state: a.txt modified again, d.txt untracked.
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "d.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := newTestModelFull(t)
	m.cfg.WorkDir = dir
	m.handleSlashCommand("/diff")

	v := m.diffViewer
	if v.scope != tools.DiffWorkingTree {
		t.Fatalf("initial scope = %v", v.scope)
	}
	names := func() []string {
		out := make([]string, 0, len(v.files))
		for _, f := range v.files {
			out = append(out, f.name)
		}
		return out
	}
	if got := strings.Join(names(), ","); got != "a.txt,d.txt" {
		t.Fatalf("working tree files = %q, want a.txt,d.txt", got)
	}

	m.handleDiffViewerKey(diffKey('s'))
	if v.scope != tools.DiffLastCommit {
		t.Fatalf("after s, scope = %v, want last commit", v.scope)
	}
	if got := strings.Join(names(), ","); got != "a.txt,b.txt" {
		t.Errorf("last commit files = %q, want a.txt,b.txt", got)
	}
	if v.line != 0 || v.file != 0 {
		t.Errorf("scope switch should reset the cursor, got file=%d line=%d", v.file, v.line)
	}

	m.handleDiffViewerKey(diffKey('s'))
	if v.scope != tools.DiffWorkingTree {
		t.Errorf("second s should return to the working tree, got %v", v.scope)
	}
}

// --- window render on big data ---

func TestDiffViewer_WindowRender_OnlyVisible(t *testing.T) {
	m := diffViewerModel(t)
	v := m.diffViewer

	// One hunk of 5000 rows — far past any viewport.
	const total = 5000
	lines := make([]tools.DiffLine, 0, total)
	for i := range total {
		text := fmt.Sprintf(" filler row %d", i)
		if i == 5 {
			text = " MARKER-TOP"
		}
		if i == total-50 {
			text = " MARKER-FAR"
		}
		lines = append(lines, tools.DiffLine{Kind: tools.DiffLineContext, Text: text})
	}
	v.files = []diffViewerFile{{
		name:   "big.go",
		status: "M",
		hunks:  []tools.DiffHunk{{Header: "@@ -1 +" + fmt.Sprint(total+1) + " @@", Lines: lines}},
	}}
	v.file, v.line, v.scroll, v.rows, v.rowsFor = 0, 0, 0, nil, -1

	const width, viewport = 80, 30
	bodyH := viewport - diffViewerChrome
	out := m.renderDiffViewer(width, viewport)
	got := strings.Split(out, "\n")

	// The box paints exactly the viewport: 2 border rows + chrome + body.
	if len(got) != viewport {
		t.Errorf("render produced %d rows, want %d (full viewport)", len(got), viewport)
	}
	plain := ansi.Strip(out)
	if !strings.Contains(plain, "MARKER-TOP") {
		t.Error("the first body rows should be visible at scroll 0")
	}
	if strings.Contains(plain, "MARKER-FAR") {
		t.Error("MARKER-FAR sits ~50 rows below the window and must NOT be rendered")
	}
	if bodyH <= 10 || bodyH >= total {
		t.Fatalf("test misconfigured: bodyH = %d", bodyH)
	}
}

// --- empty and error states ---

func TestDiffViewer_EmptyDiff(t *testing.T) {
	m := newTestModelFull(t)
	m.diffViewer = &diffViewerState{scope: tools.DiffWorkingTree}

	out := ansi.Strip(m.renderDiffViewer(80, 30))
	if !strings.Contains(out, "Нет изменений") {
		t.Errorf("empty diff should say so, got:\n%s", out)
	}
	if !strings.Contains(out, "Working tree") {
		t.Errorf("empty state should name the scope, got:\n%s", out)
	}

	// Navigation on empty data must be a safe no-op.
	m.handleDiffViewerKey(diffKey('j'))
	m.handleDiffViewerKey(diffKey('n'))
	m.handleDiffViewerKey(diffKey(']'))
	if v := m.diffViewer; v.line != 0 || v.file != 0 {
		t.Errorf("navigation on empty data moved: file=%d line=%d", v.file, v.line)
	}
}

// --- close restores the chat ---

func TestDiffViewer_CloseKeepsChat(t *testing.T) {
	m := newTestModelFull(t)
	m.diffViewer = &diffViewerState{scope: tools.DiffWorkingTree, files: diffMockFiles()}
	m.openOverlay(overlayDiffViewer)
	before := len(m.chatModel.Messages)

	// q closes and pops.
	model, _, handled := m.handleDiffViewerKey(diffKey('q'))
	if !handled {
		t.Fatal("q should be handled by the viewer")
	}
	if model != m {
		t.Errorf("q should return the same model, got %T", model)
	}
	if m.diffViewer != nil {
		t.Error("diffViewer should be nil after q")
	}
	if m.hasOverlay(overlayDiffViewer) {
		t.Error("overlay entry should be popped after q")
	}
	if len(m.chatModel.Messages) != before {
		t.Error("closing the viewer must not touch the chat")
	}

	// Esc closes too.
	m.diffViewer = &diffViewerState{scope: tools.DiffWorkingTree, files: diffMockFiles()}
	m.openOverlay(overlayDiffViewer)
	m.handleDiffViewerKey(tea.Key{Code: tea.KeyEsc})
	if m.diffViewer != nil {
		t.Error("diffViewer should be nil after Esc")
	}
}

func TestDiffViewer_CtrlCPassesThrough(t *testing.T) {
	m := diffViewerModel(t)
	_, _, handled := m.handleDiffViewerKey(tea.Key{Code: 'c', Mod: tea.ModCtrl})
	if handled {
		t.Error("Ctrl+C must fall through to the interrupt layer")
	}
	if m.diffViewer == nil {
		t.Error("Ctrl+C must not close the viewer")
	}
}

// --- colors and truncation ---

func diffColorFixture(m *model) {
	m.diffViewer = &diffViewerState{
		scope: tools.DiffWorkingTree,
		files: []diffViewerFile{{
			name:   "f.go",
			status: "M",
			hunks: []tools.DiffHunk{{
				Header: "@@ -1,3 +1,3 @@",
				Lines: []tools.DiffLine{
					{Kind: tools.DiffLineContext, Text: " ctx line"},
					{Kind: tools.DiffLineAdd, Text: "+added line"},
					{Kind: tools.DiffLineRemove, Text: "-removed line"},
				},
			}},
		}},
	}
}

func TestDiffViewer_LineColors(t *testing.T) {
	m := newTestModelFull(t)
	diffColorFixture(m)
	m.themeManager = nil // palette fallback path: green/red from the palette

	out := m.renderDiffViewer(80, 30)
	got := strings.Split(out, "\n")
	// Body rows: hunk header, ctx, add, remove.
	add := got[diffBodyStart+2]
	del := got[diffBodyStart+3]
	ctx := got[diffBodyStart+1]

	for name, line := range map[string]string{"add": add, "del": del, "ctx": ctx} {
		if diffFGOf(line) == "" {
			t.Errorf("%s row rendered without color: %q", name, ansi.Strip(line))
		}
	}
	if diffFGOf(add) == diffFGOf(del) {
		t.Errorf("added and removed rows share a color: %q", diffFGOf(add))
	}
	if diffFGOf(add) == diffFGOf(ctx) || diffFGOf(del) == diffFGOf(ctx) {
		t.Error("context rows must be a third, distinct color")
	}

	// The theme's diff roles take precedence over the palette fallback.
	m2 := newTestModelFull(t) // themeManager set → tokyo-night diffAddedText
	diffColorFixture(m2)
	out2 := m2.renderDiffViewer(80, 30)
	add2 := strings.Split(out2, "\n")[diffBodyStart+2]
	if diffFGOf(add2) == diffFGOf(add) {
		t.Errorf("theme diff color should differ from the palette fallback: both %q", diffFGOf(add))
	}
}

func TestDiffViewer_CursorRowHighlighted(t *testing.T) {
	m := newTestModelFull(t)
	diffColorFixture(m)

	// Cursor on the ctx row (body index 1).
	m.diffViewer.line = 1
	withCursor := strings.Split(m.renderDiffViewer(80, 30), "\n")[diffBodyStart+1]

	// Move the cursor to the add row: the ctx row loses the highlight.
	m.diffViewer.line = 2
	got2 := strings.Split(m.renderDiffViewer(80, 30), "\n")
	if ansi.Strip(got2[diffBodyStart+1]) != ansi.Strip(withCursor) {
		t.Fatalf("the ctx row text should not change, got %q", ansi.Strip(got2[diffBodyStart+1]))
	}
	if got2[diffBodyStart+1] == withCursor {
		t.Error("the cursor row should render differently (reverse video) from an off-cursor row")
	}
}

func TestDiffViewer_LongLineTruncated(t *testing.T) {
	m := newTestModelFull(t)
	diffColorFixture(m)
	long := "+" + strings.Repeat("x", 200)
	m.diffViewer.files[0].hunks[0].Lines[1] = tools.DiffLine{Kind: tools.DiffLineAdd, Text: long}

	const width = 80 // wide enough that header and strip stay single-line
	out := m.renderDiffViewer(width, 30)
	got := strings.Split(out, "\n")
	// Drop the border glyphs and the box padding before counting.
	addRow := strings.TrimRight(strings.TrimSuffix(strings.TrimPrefix(ansi.Strip(got[diffBodyStart+2]), "│"), "│"), " ")

	// Inner width 78, two cells of gutter headroom → 76 visible runes, the
	// last one the chevron.
	if len([]rune(addRow)) != 76 {
		t.Errorf("long row clipped to %d runes, want 76: %q", len([]rune(addRow)), addRow)
	}
	if !strings.HasSuffix(addRow, "›") {
		t.Errorf("clipped row should end with '›', got %q", addRow)
	}
	if strings.Contains(addRow, "...") {
		t.Error("diff rows cut with '›', not '...'")
	}
}

// --- layout: strip and hint ---

func TestDiffViewer_HeaderAndStrip(t *testing.T) {
	m := diffViewerModel(t)

	out := ansi.Strip(m.renderDiffViewer(100, 30))
	got := strings.Split(out, "\n")

	if !strings.Contains(got[1], "Working tree") {
		t.Errorf("header should name the scope, got %q", got[1])
	}
	if !strings.Contains(got[1], "2 файла") || !strings.Contains(got[1], "файл 1/2") {
		t.Errorf("header should show the file count and position, got %q", got[1])
	}
	if !strings.Contains(got[2], "first.go") || !strings.Contains(got[2], "second.go") {
		t.Errorf("file strip should list both files, got %q", got[2])
	}
	if !strings.Contains(got[len(got)-2], "j/k") {
		t.Errorf("hint line should list the keys, got %q", got[len(got)-2])
	}
}

func TestDiffViewer_UntrackedFileStatus(t *testing.T) {
	// Untracked files flow through the --no-index patch route and render
	// every line as added — pinned end to end through the tools layer.
	dir := diffRepoInit(t)
	diffRepoWrite(t, dir, "a.txt", "one\n")
	gitTuiCommit(t, dir, "c1")
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("x\ny\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := newTestModelFull(t)
	m.cfg.WorkDir = dir
	m.handleSlashCommand("/diff")

	v := m.diffViewer
	if len(v.files) != 1 || v.files[0].name != "new.txt" || v.files[0].status != "?" {
		t.Fatalf("files = %+v, want only untracked new.txt", v.files)
	}
	if len(v.files[0].hunks) != 1 || v.files[0].hunks[0].Added != 2 {
		t.Errorf("untracked hunks = %+v, want 2 added lines", v.files[0].hunks)
	}
}

// --- registry wiring ---

func TestDiffViewer_SlashCommandRegistered(t *testing.T) {
	b, ok := registryByCommand["/diff"]
	if !ok {
		t.Fatal("/diff missing from the registry")
	}
	if b.ID != "cmd.diff" || b.Category != catGit {
		t.Errorf("binding = %+v, want cmd.diff in catGit", b)
	}
	if slashHandlerFor("cmd.diff") == nil {
		t.Error("cmd.diff has no handler")
	}
	// Autocomplete: "/d" resolves to /diff.
	m := newTestModelFull(t)
	m.newSearchPopup(searchModeCommands)
	m.searchPopup.search = "d"
	m.searchPopup.filterSearch()
	found := false
	for _, e := range m.searchPopup.filtered {
		if strings.HasPrefix(e.Text, "/diff") {
			found = true
		}
	}
	if !found {
		t.Error("'/d' autocomplete should surface /diff")
	}
}
