package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// Tests for the opencode-style chrome (#38): the bordered prompt, the framed
// assistant replies with their compact headers, and the way the extended
// theme tokens drive them.

// TestInputViewBorderedBox pins the prompt box: a rounded border around the
// prompt whose color follows focus — borderSubtle at rest, borderActive while
// the engine has focus.
func TestInputViewBorderedBox(t *testing.T) {
	im := NewInputModel(nil, nil, nil, "")
	im.Palette = darkPalette
	im.SetWidth(40)

	view := im.View(false)
	plain := ansi.Strip(view)
	lines := strings.Split(plain, "\n")
	if len(lines) != 3 {
		t.Fatalf("prompt box = %d rows, want 3 (1 content + 2 border)", len(lines))
	}
	if !strings.HasPrefix(lines[0], "╭") || !strings.HasPrefix(lines[2], "╰") {
		t.Errorf("prompt box missing rounded borders: %q / %q", lines[0], lines[2])
	}
	if !strings.Contains(lines[1], "> ") {
		t.Errorf("prompt lost the > marker inside the box: %q", lines[1])
	}

	// Focus moves the border color from borderSubtle to borderActive.
	blurred := im.View(false)
	im.input.Blur()
	focused := im.View(false)
	if ansi.Strip(focused) != ansi.Strip(blurred) {
		t.Error("focus changed the box content, not just the border color")
	}
	if focused == blurred {
		t.Error("focused and resting boxes render identically; the border color does not follow focus")
	}
	_ = im.input.Focus()
}

// TestAssistantReplyFramedWithHeader pins the framed reply: a thin border
// around the block, a compact one-line header carrying the role marker and
// the model label in the muted role, and no frame around user turns.
func TestAssistantReplyFramedWithHeader(t *testing.T) {
	c := NewChatModel(nil)
	c.Width = 60
	c.ModelLabel = "glm-5.3-flash"
	c.Messages = append(c.Messages,
		message{role: "user", content: "hello"},
		message{role: "assistant", content: "world"},
	)
	out := ansi.Strip(c.RenderMessages(false))

	if !strings.Contains(out, "╭") || !strings.Contains(out, "╰") {
		t.Errorf("assistant reply is not framed:\n%s", out)
	}
	if !strings.Contains(out, "◉ glm-5.3-flash") {
		t.Errorf("reply header missing the role marker and model:\n%s", out)
	}
	// The user turn keeps its bare "> " form — no box around it.
	if strings.Contains(out, "╭─") && strings.Contains(strings.Split(out, "hello")[0], "╭") {
		t.Errorf("user turn got a frame:\n%s", out)
	}
	if !strings.Contains(out, "> hello") {
		t.Errorf("user turn lost its prompt marker:\n%s", out)
	}
	// The frame spans exactly the chat width.
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "╭") && ansi.StringWidth(line) != 60 {
			t.Errorf("frame row is %d wide, want the chat width 60", ansi.StringWidth(line))
		}
	}
}

// TestAssistantHeaderFallsBackToRoleName covers a model with no label
// (tests, bare models): the header names the role instead.
func TestAssistantHeaderFallsBackToRoleName(t *testing.T) {
	c := NewChatModel(nil)
	c.Width = 60
	c.Messages = append(c.Messages, message{role: "assistant", content: "hi"})
	if out := ansi.Strip(c.RenderMessages(false)); !strings.Contains(out, "◉ assistant") {
		t.Errorf("header should fall back to the role name:\n%s", out)
	}
}

// TestReplyFrameDoesNotWrapContent pins the geometry contract with glamour:
// its longest line is width-2, which is exactly the frame's inner width, so
// nothing inside the box may push the border past the pane.
func TestReplyFrameDoesNotWrapContent(t *testing.T) {
	c := NewChatModel(nil)
	c.Width = 60
	c.UpdateRenderer(60)
	c.ModelLabel = "m"
	long := strings.Repeat("word ", 60)
	c.Messages = append(c.Messages, message{role: "assistant", content: long})
	for _, line := range strings.Split(ansi.Strip(c.RenderMessages(false)), "\n") {
		if w := ansi.StringWidth(line); w > 60 {
			t.Fatalf("framed reply produced a %d-wide row, past the %d-column pane: %q", w, 60, line)
		}
	}
}

// TestModelLabelSwitchInvalidatesCache pins the render-cache key: changing
// the model label must change the key, or a /model switch would repaint the
// chrome and leave stale headers in the transcript.
func TestModelLabelSwitchInvalidatesCache(t *testing.T) {
	msg := message{role: "assistant", content: "x"}
	before := msg.renderKey(80, false, false, false, 0, false, "model-a")
	after := msg.renderKey(80, false, false, false, 0, false, "model-b")
	if before == after {
		t.Error("model label is not part of the render key; headers would go stale after /model")
	}
}

// TestCursorAccountsForPromptBox pins the terminal-caret offset: the box
// insets the editable area by one row on top and one column on the left, and
// Cursor must hand back the screen cell, not the engine-internal one.
func TestCursorAccountsForPromptBox(t *testing.T) {
	im := NewInputModel(nil, nil, nil, "")
	im.SetWidth(40)
	c := im.Cursor()
	if c == nil {
		t.Fatal("Cursor() = nil")
	}
	if c.X != 3 || c.Y != 1 {
		t.Errorf("cursor = (%d,%d), want (3,1): column 1 for the border + 2 prompt cells, row 1 for the top border", c.X, c.Y)
	}
}
