package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestIsCancelKey(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		key  tea.Key
		want bool
	}{
		{"esc cancels", tea.Key{Code: tea.KeyEsc}, true},
		{"ctrl+c cancels", tea.Key{Code: 'c', Mod: tea.ModCtrl}, true},
		{"plain c does not", tea.Key{Code: 'c'}, false},
		{"ctrl+d does not", tea.Key{Code: 'd', Mod: tea.ModCtrl}, false},
		{"enter does not", tea.Key{Code: tea.KeyEnter}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isCancelKey(tt.key); got != tt.want {
				t.Errorf("isCancelKey(%+v) = %v, want %v", tt.key, got, tt.want)
			}
		})
	}
}

func TestBranchPopupMoveUp(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		popup         branchPopupState
		wantSelected  int
		wantScrollOff int
	}{
		{
			name:          "stops at the top",
			popup:         branchPopupState{branches: []string{"a", "b"}, selected: 0, height: 2},
			wantSelected:  0,
			wantScrollOff: 0,
		},
		{
			name:          "moves up within the window",
			popup:         branchPopupState{branches: []string{"a", "b", "c"}, selected: 2, scrollOff: 0, height: 3},
			wantSelected:  1,
			wantScrollOff: 0,
		},
		{
			name:          "scrolls when leaving the window",
			popup:         branchPopupState{branches: []string{"a", "b", "c"}, selected: 2, scrollOff: 2, height: 1},
			wantSelected:  1,
			wantScrollOff: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := tt.popup
			p.moveUp()
			if p.selected != tt.wantSelected || p.scrollOff != tt.wantScrollOff {
				t.Errorf("moveUp() = selected %d/scrollOff %d, want %d/%d",
					p.selected, p.scrollOff, tt.wantSelected, tt.wantScrollOff)
			}
		})
	}
}

func TestBranchPopupMoveDown(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		popup         branchPopupState
		wantSelected  int
		wantScrollOff int
	}{
		{
			name:          "stops at the bottom",
			popup:         branchPopupState{branches: []string{"a", "b"}, selected: 1, height: 2},
			wantSelected:  1,
			wantScrollOff: 0,
		},
		{
			name:          "moves down within the window",
			popup:         branchPopupState{branches: []string{"a", "b", "c"}, selected: 0, height: 3},
			wantSelected:  1,
			wantScrollOff: 0,
		},
		{
			name:          "scrolls when leaving the window",
			popup:         branchPopupState{branches: []string{"a", "b", "c"}, selected: 0, scrollOff: 0, height: 1},
			wantSelected:  1,
			wantScrollOff: 1,
		},
		{
			name:          "empty list cannot move",
			popup:         branchPopupState{branches: nil, selected: 0, height: 1},
			wantSelected:  0,
			wantScrollOff: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := tt.popup
			p.moveDown()
			if p.selected != tt.wantSelected || p.scrollOff != tt.wantScrollOff {
				t.Errorf("moveDown() = selected %d/scrollOff %d, want %d/%d",
					p.selected, p.scrollOff, tt.wantSelected, tt.wantScrollOff)
			}
		})
	}
}

func TestBranchPopupNavigationStaysInBounds(t *testing.T) {
	t.Parallel()
	p := branchPopupState{branches: []string{"a", "b", "c"}, height: 2}

	// Hammer both directions well past the ends; the selection must stay valid.
	for range 10 {
		p.moveDown()
	}
	if p.selected != len(p.branches)-1 {
		t.Errorf("selected = %d after many moveDown, want %d", p.selected, len(p.branches)-1)
	}
	for range 10 {
		p.moveUp()
	}
	if p.selected != 0 {
		t.Errorf("selected = %d after many moveUp, want 0", p.selected)
	}
	if p.scrollOff < 0 || p.scrollOff > len(p.branches) {
		t.Errorf("scrollOff = %d is out of range", p.scrollOff)
	}
}

func TestHandleScrollKey(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		key         tea.Key
		wantHandled bool
	}{
		{"page up is handled", tea.Key{Code: tea.KeyPgUp}, true},
		{"page down is handled", tea.Key{Code: tea.KeyPgDown}, true},
		{"other keys fall through", tea.Key{Code: 'x'}, false},
		{"enter falls through", tea.Key{Code: tea.KeyEnter}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := &model{height: 24}
			_, _, handled := m.handleScrollKey(tt.key)
			if handled != tt.wantHandled {
				t.Errorf("handleScrollKey(%+v) handled = %v, want %v", tt.key, handled, tt.wantHandled)
			}
		})
	}
}

func TestHandleInterruptKey(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		key         tea.Key
		wantHandled bool
	}{
		{"esc is handled", tea.Key{Code: tea.KeyEsc}, true},
		{"ctrl+c is handled", tea.Key{Code: 'c', Mod: tea.ModCtrl}, true},
		{"f12 is swallowed", tea.Key{Code: tea.KeyF12}, true},
		{"ordinary keys fall through", tea.Key{Code: 'a'}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := newTestModel(t)
			_, _, handled := m.handleInterruptKey(tt.key)
			if handled != tt.wantHandled {
				t.Errorf("handleInterruptKey(%+v) handled = %v, want %v", tt.key, handled, tt.wantHandled)
			}
		})
	}
}

func TestHandleInterruptKeyEscDismissesSearchPopupFirst(t *testing.T) {
	t.Parallel()
	m := newTestModel(t)
	m.newSearchPopup(searchModeCommands)
	if m.searchPopup == nil {
		t.Skip("search popup could not be opened in this fixture")
	}

	if _, _, handled := m.handleInterruptKey(tea.Key{Code: tea.KeyEsc}); !handled {
		t.Fatal("esc should be handled")
	}
	if m.searchPopup != nil {
		t.Error("esc should dismiss the search popup")
	}
}

func TestHandleInterruptKeyCtrlCQuitsOnSecondPress(t *testing.T) {
	t.Parallel()
	m := newTestModel(t)
	ctrlC := tea.Key{Code: 'c', Mod: tea.ModCtrl}

	if _, _, _ = m.handleInterruptKey(ctrlC); m.quitting {
		t.Fatal("a single Ctrl+C should warn, not quit")
	}
	if _, _, _ = m.handleInterruptKey(ctrlC); !m.quitting {
		t.Error("a second Ctrl+C should quit")
	}
}

// TestHandleInterruptKeyEscWarnsBeforeCancelTurn guards the double-Esc
// confirmation on a running turn: the first Esc warns and keeps the turn
// alive, the second within the window cancels and resets the counter.
func TestHandleInterruptKeyEscWarnsBeforeCancelTurn(t *testing.T) {
	t.Parallel()
	m := newTestModel(t)
	m.running = true
	esc := tea.Key{Code: tea.KeyEsc}

	model, cmd, handled := m.handleInterruptKey(esc)
	if !handled {
		t.Fatal("esc should be handled while running")
	}
	if model != m {
		t.Fatal("handleInterruptKey should return the same model")
	}
	if !m.running {
		t.Fatal("the first Esc must not cancel the running turn")
	}
	if m.escCancelCount != 1 {
		t.Fatalf("escCancelCount = %d, want 1 after the first Esc", m.escCancelCount)
	}
	if cmd == nil {
		t.Fatal("the first Esc must arm the reset timer")
	}
	if got := m.chatModel.Messages; len(got) == 0 {
		t.Fatal("the first Esc must append a warning")
	}
	if !strings.Contains(m.chatModel.Messages[len(m.chatModel.Messages)-1].content, "Esc again") {
		t.Errorf("last message = %q, want an \"Esc again\" warning",
			m.chatModel.Messages[len(m.chatModel.Messages)-1].content)
	}

	if _, _, _ = m.handleInterruptKey(esc); m.running {
		t.Error("the second Esc must cancel the running turn")
	}
	if m.escCancelCount != 0 {
		t.Errorf("escCancelCount = %d, want 0 after the cancel", m.escCancelCount)
	}
}

// TestHandleInterruptKeyEscTimerReset: the resetEscCancelCountMsg delivery
// clears the counter, so after it a single Esc warns again instead of
// canceling.
func TestHandleInterruptKeyEscTimerReset(t *testing.T) {
	t.Parallel()
	m := newTestModel(t)
	m.running = true
	esc := tea.Key{Code: tea.KeyEsc}

	if _, _, _ = m.handleInterruptKey(esc); m.escCancelCount != 1 {
		t.Fatalf("escCancelCount = %d, want 1 after the first Esc", m.escCancelCount)
	}

	m.Update(resetEscCancelCountMsg{seq: m.escResetSeq})
	if m.escCancelCount != 0 {
		t.Fatalf("escCancelCount = %d, want 0 after the reset msg", m.escCancelCount)
	}

	// A timer from a superseded window must not clear a freshly armed one:
	// Esc armed window 1, the count was reset by cancelAgent/beginTurn, a new
	// Esc armed window 2, and the stale window-1 timer fires.
	m.escCancelCount = 1
	seq2 := m.escResetSeq
	m.Update(resetEscCancelCountMsg{seq: seq2 - 1})
	if m.escCancelCount != 1 {
		t.Fatalf("escCancelCount = %d, want 1 — a stale timer must not clear the current window", m.escCancelCount)
	}
	m.Update(resetEscCancelCountMsg{seq: seq2})
	if m.escCancelCount != 0 {
		t.Fatalf("escCancelCount = %d, want 0 after the current-window timer", m.escCancelCount)
	}

	if _, _, _ = m.handleInterruptKey(esc); m.escCancelCount != 1 {
		t.Fatalf("escCancelCount = %d, want 1 after a fresh single Esc", m.escCancelCount)
	}
	if !m.running {
		t.Error("a single Esc after the reset must warn, not cancel")
	}
}

// TestHandleInterruptKeyEscOverlaysInstant: Esc over an open overlay closes
// it immediately and never feeds the double-Esc counter, even mid-turn.
func TestHandleInterruptKeyEscOverlaysInstant(t *testing.T) {
	t.Parallel()

	t.Run("search popup", func(t *testing.T) {
		t.Parallel()
		m := newTestModel(t)
		m.running = true
		m.newSearchPopup(searchModeCommands)
		if m.searchPopup == nil {
			t.Skip("search popup could not be opened in this fixture")
		}

		if _, _, _ = m.handleInterruptKey(tea.Key{Code: tea.KeyEsc}); m.searchPopup != nil {
			t.Error("esc should dismiss the search popup")
		}
		if !m.running {
			t.Error("dismissing the popup must not cancel the turn")
		}
		if m.escCancelCount != 0 {
			t.Errorf("escCancelCount = %d, want 0 — overlays must not feed the counter", m.escCancelCount)
		}
	})

	t.Run("steer input", func(t *testing.T) {
		t.Parallel()
		m := newTestModel(t)
		m.running = true
		m.steerInput = &subagentSteerState{agentID: "a1", label: "explore"}

		if _, _, _ = m.handleInterruptKey(tea.Key{Code: tea.KeyEsc}); m.steerInput != nil {
			t.Error("esc should dismiss the steer input")
		}
		if !m.running {
			t.Error("dismissing the steer input must not cancel the turn")
		}
		if m.escCancelCount != 0 {
			t.Errorf("escCancelCount = %d, want 0 — overlays must not feed the counter", m.escCancelCount)
		}
	})
}

// TestHandleInterruptKeyEscNoTurnNoWarning: with no running turn Esc stays a
// swallow — no warning, no cancel, no counter movement.
func TestHandleInterruptKeyEscNoTurnNoWarning(t *testing.T) {
	t.Parallel()
	m := newTestModel(t)
	esc := tea.Key{Code: tea.KeyEsc}

	model, cmd, handled := m.handleInterruptKey(esc)
	if !handled {
		t.Fatal("esc should still be handled (swallowed) when idle")
	}
	if model != m {
		t.Fatal("handleInterruptKey should return the same model")
	}
	if cmd != nil {
		t.Error("an idle Esc must not arm a reset timer")
	}
	if m.escCancelCount != 0 {
		t.Errorf("escCancelCount = %d, want 0 when idle", m.escCancelCount)
	}
	if len(m.chatModel.Messages) != 0 {
		t.Errorf("an idle Esc must not warn, got %d messages", len(m.chatModel.Messages))
	}
}

func TestHandleCommitKeyIgnoredWhenNotConfirming(t *testing.T) {
	t.Parallel()
	m := newTestModel(t)
	// No commit in flight: every key must fall through to the layers below.
	if _, _, handled := m.handleCommitKey(tea.Key{Code: tea.KeyEnter}); handled {
		t.Error("commit handler should not claim keys when no commit is confirming")
	}
}

func TestHandleSkillCreateKeyIgnoredWhenNoPendingSkill(t *testing.T) {
	t.Parallel()
	m := newTestModel(t)
	if _, _, handled := m.handleSkillCreateKey(tea.Key{Code: tea.KeyEnter}); handled {
		t.Error("skill-create handler should not claim keys with nothing pending")
	}
}

func TestHandleBranchPopupKeyIgnoredWhenClosed(t *testing.T) {
	t.Parallel()
	m := newTestModel(t)
	if _, _, handled := m.handleBranchPopupKey(tea.Key{Code: tea.KeyEnter}); handled {
		t.Error("branch popup handler should not claim keys while closed")
	}
}

func TestHandleBranchPopupKeyDismissesOnUnknownKey(t *testing.T) {
	t.Parallel()
	m := newTestModel(t)
	m.branchPopup = &branchPopupState{branches: []string{"main", "dev"}, height: 2}

	if _, _, handled := m.handleBranchPopupKey(tea.Key{Code: 'z'}); !handled {
		t.Fatal("an open branch popup should claim the key")
	}
	if m.branchPopup != nil {
		t.Error("an unrecognized key should dismiss the branch popup")
	}
}

func TestHandleBranchPopupKeyArrowsMoveSelection(t *testing.T) {
	t.Parallel()
	m := newTestModel(t)
	m.branchPopup = &branchPopupState{branches: []string{"main", "dev", "feat"}, height: 3}

	if _, _, handled := m.handleBranchPopupKey(tea.Key{Code: tea.KeyDown}); !handled {
		t.Fatal("down should be handled")
	}
	if m.branchPopup == nil {
		t.Fatal("arrow keys must not dismiss the popup")
	}
	if m.branchPopup.selected != 1 {
		t.Errorf("selected = %d after down, want 1", m.branchPopup.selected)
	}

	m.handleBranchPopupKey(tea.Key{Code: tea.KeyUp})
	if m.branchPopup.selected != 0 {
		t.Errorf("selected = %d after up, want 0", m.branchPopup.selected)
	}
}
