package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// mentionModel builds a model whose WorkDir is a small fixed tree, so the
// file-mention popup has deterministic candidates:
//
//	docs/guide.md  readme.md  src/app.go  src/main.go   (popup entry order)
func mentionModel(t *testing.T) *model {
	t.Helper()
	dir := t.TempDir()
	for _, p := range []string{"src/app.go", "src/main.go", "docs/guide.md", "README.md"} {
		path := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return &model{
		ctx:        ctx,
		cancel:     cancel,
		cfg:        Config{WorkDir: dir},
		inputModel: NewInputModel(make([]HistoryEntry, 0), nil, nil, dir),
		chatModel:  ChatModel{Messages: make([]message, 0)},
		face:       NewFaceRenderer(),
		palette:    darkPalette,
		width:      100,
		height:     30,
	}
}

// typeRunes sends s through the real key dispatch, one rune per keystroke,
// so the mention trigger and the popup sync see exactly what typing sees.
func typeRunes(t *testing.T, m *model, s string) *model {
	t.Helper()
	for _, r := range s {
		next, _ := m.handleKey(tea.KeyPressMsg(tea.Key{Code: r, Text: string(r)}))
		mm, ok := next.(*model)
		if !ok {
			t.Fatalf("handleKey returned %T, want *model", next)
		}
		m = mm
	}
	return m
}

func filteredTexts(sp *searchPopupState) []string {
	var got []string
	for _, it := range sp.filtered {
		got = append(got, it.Text)
	}
	return got
}

// Typing @ opens the files popup; the typed prefix filters it; deleting the
// @ closes it again. The @ must start the line or follow whitespace, so an
// email-like "a@b" never opens the popup.
func TestMentionPopupTrigger(t *testing.T) {
	t.Run("bare @ opens with every candidate", func(t *testing.T) {
		m := typeRunes(t, mentionModel(t), "@")

		if m.searchPopup == nil || m.searchPopup.mode != searchModeFiles {
			t.Fatalf("typing @ did not open the files popup (popup = %+v)", m.searchPopup)
		}
		if got := strings.Join(filteredTexts(m.searchPopup), ","); got != "docs/guide.md,README.md,src/app.go,src/main.go" {
			t.Fatalf("entries = %q, want the whole tree", got)
		}
		if m.searchPopup.search != "" {
			t.Fatalf("search = %q, want the empty prefix", m.searchPopup.search)
		}
	})

	t.Run("prefix filters to fuzzy matches", func(t *testing.T) {
		m := typeRunes(t, mentionModel(t), "@sr")

		if m.searchPopup == nil || m.searchPopup.mode != searchModeFiles {
			t.Fatal("@sr did not keep the files popup open")
		}
		if m.searchPopup.search != "sr" {
			t.Fatalf("search = %q, want the typed prefix sr", m.searchPopup.search)
		}
		if got := strings.Join(filteredTexts(m.searchPopup), ","); got != "src/app.go,src/main.go" {
			t.Fatalf("filtered = %q, want only the src files", got)
		}
	})

	t.Run("deleting the @ closes the popup", func(t *testing.T) {
		m := typeRunes(t, mentionModel(t), "@sr")
		for _, want := range []string{"@s", "@", ""} {
			m = press(t, m, tea.KeyBackspace)
			if want == "" {
				if m.searchPopup != nil {
					t.Fatalf("popup = %+v, want closed once the @ is gone", m.searchPopup)
				}
				break
			}
			if m.searchPopup == nil || m.searchPopup.mode != searchModeFiles {
				t.Fatalf("popup closed early at %q", want)
			}
			if m.searchPopup.search != want[1:] {
				t.Fatalf("search = %q, want %q", m.searchPopup.search, want[1:])
			}
		}
		if m.inputModel.Text != "" {
			t.Fatalf("input = %q, want empty after deleting the mention", m.inputModel.Text)
		}
	})

	t.Run("email-like a@b never opens", func(t *testing.T) {
		m := mentionModel(t)
		for _, step := range []string{"a", "@", "b"} {
			m = typeRunes(t, m, step)
			if m.searchPopup != nil {
				t.Fatalf("popup opened after typing %q: text %q", step, m.inputModel.Text)
			}
		}
		if m.inputModel.Text != "a@b" {
			t.Fatalf("input = %q, want a@b", m.inputModel.Text)
		}
	})

	t.Run("@ after a space opens", func(t *testing.T) {
		m := mentionModel(t)
		m.inputModel.SetText("look ")
		m = typeRunes(t, m, "@")
		if m.searchPopup == nil || m.searchPopup.mode != searchModeFiles {
			t.Fatal("@ after a space did not open the files popup")
		}
	})
}

// While the popup is open the stack entry owns the arrows and Enter/Esc, and
// letters still reach the prompt — the text after @ is the filter.
func TestMentionPopupKeys(t *testing.T) {
	m := typeRunes(t, mentionModel(t), "@")

	m = press(t, m, tea.KeyDown)
	m = press(t, m, tea.KeyDown)
	if m.searchPopup.selected != 2 {
		t.Fatalf("after two Downs selected = %d, want 2", m.searchPopup.selected)
	}
	if m.inputModel.Text != "@" {
		t.Fatalf("input = %q, want @ (arrows must not move the cursor)", m.inputModel.Text)
	}

	m = typeRunes(t, m, "s")
	if m.inputModel.Text != "@s" {
		t.Fatalf("input = %q, want @s (letters must reach the prompt)", m.inputModel.Text)
	}
	if m.searchPopup == nil || m.searchPopup.mode != searchModeFiles {
		t.Fatal("typing closed the files popup")
	}
	// "s" prefix-matches both src files; docs/guide.md trails as a plain
	// subsequence hit; README.md has no "s" and drops out.
	if got := strings.Join(filteredTexts(m.searchPopup), ","); got != "src/app.go,src/main.go,docs/guide.md" {
		t.Fatalf("filter did not follow the typed text: %q", got)
	}
	if m.searchPopup.selected != 0 {
		t.Fatalf("selected = %d, want 0 after a re-filter", m.searchPopup.selected)
	}

	m = press(t, m, tea.KeyUp) // wraps to the last row
	if m.searchPopup.selected != 2 {
		t.Fatalf("after Up selected = %d, want 2 (wrap to last)", m.searchPopup.selected)
	}

	m = press(t, m, tea.KeyEsc)
	if m.searchPopup != nil {
		t.Fatal("Esc did not close the files popup")
	}
	if m.inputModel.Text != "@s" {
		t.Fatalf("input = %q, want @s preserved across Esc", m.inputModel.Text)
	}
}

// Enter replaces the @prefix with the chosen path plus a space, closes the
// popup, and the path rides along when the prompt is sent.
func TestMentionPopupInsert(t *testing.T) {
	m := typeRunes(t, mentionModel(t), "@sr")
	m = press(t, m, tea.KeyDown) // select src/main.go

	m = press(t, m, tea.KeyEnter)
	if m.searchPopup != nil {
		t.Fatal("Enter did not close the files popup")
	}
	if m.inputModel.Text != "@src/main.go " {
		t.Fatalf("input = %q, want @src/main.go with a trailing space", m.inputModel.Text)
	}
	if m.inputModel.CursorPos != len("@src/main.go ") {
		t.Fatalf("cursor = %d, want at the end", m.inputModel.CursorPos)
	}

	// Sending goes through the same HandleKey the TUI uses; the mention must
	// reach the submit message without extra wiring.
	cmd := m.inputModel.HandleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if cmd == nil {
		t.Fatal("Enter after insert did not submit")
	}
	msg, ok := cmd().(InputSubmitMsg)
	if !ok {
		t.Fatalf("submit msg = %T, want InputSubmitMsg", cmd())
	}
	if len(msg.Mentions) != 1 || msg.Mentions[0] != "src/main.go" {
		t.Fatalf("mentions = %v, want [src/main.go]", msg.Mentions)
	}
}

// A pasted @ is a mention like a typed one: the popup opens and filters to
// the pasted prefix; a paste without a triggerable @ stays closed.
func TestMentionPopupPaste(t *testing.T) {
	t.Run("paste with @sr opens filtered", func(t *testing.T) {
		m := mentionModel(t)
		m.handlePaste(tea.PasteMsg{Content: "@sr"}) // mutates m; returns nil model outside a resize drain

		if m.searchPopup == nil || m.searchPopup.mode != searchModeFiles {
			t.Fatalf("pasting @sr did not open the files popup (popup = %+v)", m.searchPopup)
		}
		if m.inputModel.Text != "@sr" {
			t.Fatalf("input = %q, want the pasted text", m.inputModel.Text)
		}
		if got := strings.Join(filteredTexts(m.searchPopup), ","); got != "src/app.go,src/main.go" {
			t.Fatalf("filtered = %q, want only the src files", got)
		}
	})

	t.Run("paste without @ stays closed", func(t *testing.T) {
		m := mentionModel(t)
		m.handlePaste(tea.PasteMsg{Content: "plain text, no mention"})

		if m.searchPopup != nil {
			t.Fatalf("paste without @ opened a popup: %+v", m.searchPopup)
		}
		if m.inputModel.Text != "plain text, no mention" {
			t.Fatalf("input = %q, want the pasted text", m.inputModel.Text)
		}
	})
}
