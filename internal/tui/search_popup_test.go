package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/spa-skyson/pi-rate/internal/tools"
)

// TestFuzzyScore pins the scoring contract: text counts double, description
// adds, prefix beats word-start beats plain subsequence, a miss is -1.
func TestFuzzyScore(t *testing.T) {
	t.Run("prefix beats word-start beats plain hit", func(t *testing.T) {
		prefix := fuzzyScore("/he", "/help", "")    // "/help" starts with the query
		wordStart := fuzzyScore("he", "x help", "") // "help" after a space
		plain := fuzzyScore("he", "ahelp", "")
		if prefix <= wordStart || wordStart <= plain {
			t.Fatalf("prefix=%d wordStart=%d plain=%d, want strictly decreasing", prefix, wordStart, plain)
		}
	})

	t.Run("word boundary inside the string", func(t *testing.T) {
		// "model" after the path separator outranks a mid-word hit.
		if got := fuzzyScore("model", "anthropic/model-name", ""); got != 2*4 {
			t.Fatalf("word-start score = %d, want %d", got, 2*4)
		}
	})

	t.Run("description match counts, text counts double", func(t *testing.T) {
		if got := fuzzyScore("com", "/x", "the commit helper"); got != 4 {
			t.Fatalf("description-only score = %d, want 4 (word start)", got)
		}
		textOnly := fuzzyScore("com", "/commit", "")
		both := fuzzyScore("com", "/commit", "the commit helper")
		if both != textOnly+4 {
			t.Fatalf("text+desc = %d, want text-only %d plus desc 4", both, textOnly)
		}
	})

	t.Run("miss is -1", func(t *testing.T) {
		if got := fuzzyScore("zzz", "/help", "show help"); got != -1 {
			t.Fatalf("score = %d, want -1", got)
		}
	})

	t.Run("smo ranks models like smol above a mid-word hit", func(t *testing.T) {
		smol := fuzzyScore("smo", "smol", "")
		some := fuzzyScore("smo", "some-model", "")
		osmosis := fuzzyScore("smo", "osmosis", "")
		if smol <= some || some <= osmosis {
			t.Fatalf("smol=%d some=%d osmosis=%d, want strictly decreasing", smol, some, osmosis)
		}
	})
}

func popupWithEntries(mode searchMode, items ...SearchItem) *model {
	m := &model{palette: darkPalette, width: 100, height: 40}
	sp := &searchPopupState{mode: mode, entries: items, filtered: items, height: len(items)}
	sp.filterSearch()
	m.searchPopup = sp
	return m
}

// TestFilterSearchFuzzyOrdersByScore pins ranking and ordering in the popup.
func TestFilterSearchFuzzyOrdersByScore(t *testing.T) {
	t.Run("prefix matches first, entry order breaks ties", func(t *testing.T) {
		m := popupWithEntries(searchModeCommands,
			SearchItem{Text: "/help", Description: "help"},
			SearchItem{Text: "/heap", Description: "heap"},
			SearchItem{Text: "/theme", Description: "theme"},
		)
		m.searchPopup.search = "/he"
		m.searchPopup.filterSearch()

		var got []string
		for _, it := range m.searchPopup.filtered {
			got = append(got, it.Text)
		}
		// /help and /heap are both prefix matches (equal score) — the entry
		// order between them stands; /theme's plain hit trails.
		want := []string{"/help", "/heap", "/theme"}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("order = %v, want %v", got, want)
		}
	})

	t.Run("description-only match is kept", func(t *testing.T) {
		m := popupWithEntries(searchModeAgents,
			SearchItem{Text: "xyz", Description: "orchestrates builds"},
			SearchItem{Text: "build", Description: "executor"},
		)
		m.searchPopup.search = "orch"
		m.searchPopup.filterSearch()
		if len(m.searchPopup.filtered) != 1 || m.searchPopup.filtered[0].Text != "xyz" {
			t.Fatalf("filtered = %+v, want only the description match", m.searchPopup.filtered)
		}
	})

	t.Run("empty filter keeps the entry order", func(t *testing.T) {
		m := popupWithEntries(searchModeModels,
			SearchItem{Text: "b"}, SearchItem{Text: "a"},
		)
		var got []string
		for _, it := range m.searchPopup.filtered {
			got = append(got, it.Text)
		}
		if strings.Join(got, ",") != "b,a" {
			t.Fatalf("order = %v, want the entry order b,a", got)
		}
	})

	t.Run("nothing matched falls back to the full list", func(t *testing.T) {
		m := popupWithEntries(searchModeCommands,
			SearchItem{Text: "/help"}, SearchItem{Text: "/plan"},
		)
		m.searchPopup.search = "zzz"
		m.searchPopup.filterSearch()
		if len(m.searchPopup.filtered) != 2 {
			t.Fatalf("filtered = %d rows, want the full list on a miss", len(m.searchPopup.filtered))
		}
	})
}

// TestSuggestedCommandsFrecency drives the frecency path through the real
// selection handler.
func TestSuggestedCommandsFrecency(t *testing.T) {
	m := cplxModel(t)
	m.inputModel.SetText("/")

	// "Use" two commands through the popup: select /plan, then /commit.
	for _, cmd := range []string{"/plan", "/commit"} {
		m.inputModel.SetText("/")
		m.newSearchPopup(searchModeCommands)
		found := false
		for i, it := range m.searchPopup.filtered {
			if it.Text == cmd {
				m.searchPopup.selected = i
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("%s is not among the popup entries", cmd)
		}
		if _, handled := m.handleSearchPopupKey(tea.Key{Code: tea.KeyEnter}); !handled {
			t.Fatalf("Enter not handled for %s", cmd)
		}
		if m.searchPopup != nil {
			t.Fatalf("Enter did not close the popup for %s", cmd)
		}
	}

	m.inputModel.SetText("/")
	m.newSearchPopup(searchModeCommands)
	var got []string
	for _, it := range m.searchPopup.filtered[:2] {
		got = append(got, it.Text)
	}
	if strings.Join(got, ",") != "/commit,/plan" {
		t.Fatalf("suggested order = %v, want [/commit /plan] (most recent first)", got)
	}

	// The section renders with a header; move off the pinned row so the
	// ● current marker is visible (the selection arrow owns the top row).
	m.searchPopup.selected = 1
	out := ansi.Strip(m.renderSearchPopup(80))
	if !strings.Contains(out, "Suggested") || !strings.Contains(out, "●") {
		t.Fatalf("popup missing the Suggested section: %q", out)
	}

	// A non-empty filter drops the section.
	m.searchPopup.search = "pl"
	m.searchPopup.filterSearch()
	if m.searchPopup.suggestedN != 0 {
		t.Fatalf("suggestedN = %d under a filter, want 0", m.searchPopup.suggestedN)
	}
	out = ansi.Strip(m.renderSearchPopup(80))
	if strings.Contains(out, "Suggested") {
		t.Fatalf("a filtered popup must not show the Suggested section: %q", out)
	}

	// The list is capped at suggestedCommandsMax.
	for _, cmd := range []string{"/a", "/b", "/c", "/d", "/e", "/f"} {
		m.recordCommandUse(cmd)
	}
	if len(m.cmdRecent) != suggestedCommandsMax {
		t.Fatalf("cmdRecent = %d entries, want %d", len(m.cmdRecent), suggestedCommandsMax)
	}
	if m.cmdRecent[0] != "/f" {
		t.Fatalf("cmdRecent[0] = %q, want the most recent /f", m.cmdRecent[0])
	}
	if m.cmdCounts["/plan"] != 1 {
		t.Fatalf("usage counter for /plan = %d, want 1", m.cmdCounts["/plan"])
	}
}

// TestSuggestedActiveModelPinned pins the active model on top with the
// current marker.
func TestSuggestedActiveModelPinned(t *testing.T) {
	m, _ := popupTestModel(t)
	m.cfg.ActiveRole = "zai-coding-plan/glm-5.3"
	m = submit(t, m, "/model")

	if m.searchPopup == nil || len(m.searchPopup.filtered) == 0 {
		t.Fatal("/model did not open the models popup")
	}
	if got := m.searchPopup.filtered[0].Text; got != "zai-coding-plan/glm-5.3" {
		t.Fatalf("top row = %q, want the active model", got)
	}
	m.searchPopup.selected = 1 // step off the pinned row so ● is visible
	out := ansi.Strip(m.renderSearchPopup(100))
	if !strings.Contains(out, "Suggested") || !strings.Contains(out, "●") {
		t.Fatalf("models popup missing the Suggested section: %q", out)
	}
}

// TestSuggestedTodosCurrent pins the in-progress todo on top.
func TestSuggestedTodosCurrent(t *testing.T) {
	m := cplxModel(t)
	m.todoState = &tools.TodoState{Items: []tools.TodoItem{
		{Content: "already done", Status: "completed"},
		{Content: "current step", Status: "in_progress"},
		{Content: "later step", Status: "pending"},
	}}

	m.newSearchPopup(searchModeTodos)

	if got := m.searchPopup.filtered[0].Text; got != "[~] current step" {
		t.Fatalf("top row = %q, want the in-progress todo", got)
	}
	out := ansi.Strip(m.renderSearchPopup(80))
	if !strings.Contains(out, "Suggested") {
		t.Fatalf("todos popup missing the Suggested section: %q", out)
	}
}

// TestSuggestedStaleRowDropsOut: a suggestion whose row vanished from the
// entries must not pin a phantom.
func TestSuggestedStaleRowDropsOut(t *testing.T) {
	m := popupWithEntries(searchModeModels, SearchItem{Text: "default"}, SearchItem{Text: "smol"})
	m.searchPopup.suggested = []SearchItem{{Text: "gone"}}
	m.searchPopup.filterSearch()
	if m.searchPopup.suggestedN != 0 {
		t.Fatalf("suggestedN = %d, want 0 for a stale suggestion", m.searchPopup.suggestedN)
	}
	if got := m.searchPopup.filtered[0].Text; got != "default" {
		t.Fatalf("top row = %q, want the untouched entry order", got)
	}
}

// TestSearchPopupFooters pins the honest per-mode key hints.
func TestSearchPopupFooters(t *testing.T) {
	cases := []struct {
		mode      searchMode
		wantEnter bool
	}{
		{searchModeCommands, true},
		{searchModeHistory, true},
		{searchModeModels, true},
		{searchModeAgents, true},
		{searchModeSubagents, true},
		{searchModeTodos, false}, // view-only: Enter does nothing here
	}
	for _, tc := range cases {
		t.Run(string(tc.mode), func(t *testing.T) {
			m := cplxModel(t)
			m.searchPopup = cplxPopup(tc.mode, 2, 3)
			out := ansi.Strip(m.renderSearchPopup(80))

			for _, want := range []string{"↑/↓", "Esc"} {
				if !strings.Contains(out, want) {
					t.Errorf("footer missing %q: %q", want, out)
				}
			}
			if got := strings.Contains(out, "Enter"); got != tc.wantEnter {
				t.Errorf("footer Enter = %v, want %v: %q", got, tc.wantEnter, out)
			}
			if strings.Contains(out, "Tab") {
				t.Errorf("footer must not advertise Tab (bug #8): %q", out)
			}
			if tc.mode == searchModeSubagents && !strings.Contains(out, "s steer") {
				t.Errorf("subagents footer missing the steer key: %q", out)
			}
		})
	}
}

// TestSearchPopupFooterEmptyList: the footer renders with the no-match state
// too, so the popup never loses its key hints.
func TestSearchPopupFooterEmptyList(t *testing.T) {
	m := cplxModel(t)
	m.searchPopup = cplxPopup(searchModeCommands, 0, 0)
	out := ansi.Strip(m.renderSearchPopup(60))
	if !strings.Contains(out, "No matching commands") || !strings.Contains(out, "Esc") {
		t.Fatalf("empty popup missing the no-match line or footer: %q", out)
	}
}

// TestSearchPopupRenderWidthAllModes renders all six modes with long rows and
// a pinned suggestion: no line may exceed the popup frame.
func TestSearchPopupRenderWidthAllModes(t *testing.T) {
	long := strings.Repeat("word ", 12)
	for _, mode := range []searchMode{
		searchModeCommands, searchModeHistory, searchModeModels,
		searchModeAgents, searchModeSubagents, searchModeTodos,
	} {
		t.Run(string(mode), func(t *testing.T) {
			items := []SearchItem{
				{ID: "id-1", Text: long + " one", Description: long + " desc"},
				{ID: "id-2", Text: long + " two", Description: long + " desc"},
			}
			m := &model{palette: darkPalette, width: 100, height: 40}
			sp := &searchPopupState{
				mode:      mode,
				entries:   items,
				suggested: []SearchItem{{ID: "id-1", Text: long + " one"}},
				filtered:  items,
				height:    2,
			}
			sp.filterSearch()
			m.searchPopup = sp

			const width = 60
			for _, line := range strings.Split(ansi.Strip(m.renderSearchPopup(width)), "\n") {
				if got := len([]rune(line)); got > width+2 {
					t.Fatalf("line is %d columns, want ≤ %d (width %d + border): %q", got, width+2, width, line)
				}
			}
		})
	}
}
