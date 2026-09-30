package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/spa-skyson/pi-rate/internal/permission"
)

// pressRune routes one plain keystroke through the full dispatch. Text
// accompanies Code so printable keys reach the prompt input the way real
// terminals deliver them.
func pressRune(t *testing.T, m *model, code rune) {
	t.Helper()
	key := tea.Key{Code: code}
	if code >= 32 && code < 0x80 {
		key.Text = string(code)
	}
	next, _ := m.handleKey(tea.KeyPressMsg(key))
	if _, ok := next.(*model); !ok {
		t.Fatalf("handleKey returned %T, want *model", next)
	}
}

// TestOverlayStack_TopEntryGetsKeyFirst pins the stack rule the fixed key
// chain used to encode: with two overlays open, the key goes to the one
// opened last, and only after it pops does the one below see anything.
func TestOverlayStack_TopEntryGetsKeyFirst(t *testing.T) {
	m := newTestModel(t)
	var bottomHit, topHit bool
	marker := func(hit *bool) keyHandler {
		return func(tea.Key) (tea.Model, tea.Cmd, bool) {
			*hit = true
			return m, nil, true
		}
	}
	m.pushOverlay(overlayEntry{kind: overlayCustom, keyHandler: marker(&bottomHit)})
	m.pushOverlay(overlayEntry{kind: overlayCustom, keyHandler: marker(&topHit)})

	pressRune(t, m, 'x')

	if !topHit || bottomHit {
		t.Fatalf("top=%v bottom=%v, want the top entry alone to win", topHit, bottomHit)
	}

	m.popOverlay(overlayCustom) // pops the topmost custom entry
	pressRune(t, m, 'x')

	if !bottomHit {
		t.Error("the entry below never received the key after the top popped")
	}
}

// TestOverlayStack_EscChain_ViewerMonitorThenCancelTurn walks the Esc ladder
// end to end: the viewer steps back to the monitor, the monitor closes, and
// only an empty stack lets Esc cancel the running turn — the exact order the
// old fixed chain held by position.
func TestOverlayStack_EscChain_ViewerMonitorThenCancelTurn(t *testing.T) {
	m := monitorTestModel(t)
	m = submit(t, m, "/subagents")
	selectRow(t, m, "explore-1727000000000000000")
	m = pressKey(t, m, tea.Key{Code: tea.KeyEnter}) // viewer over monitor
	if m.subagentViewer == nil || m.searchPopup == nil {
		t.Fatal("precondition: viewer over monitor did not open")
	}
	m.beginTurn()

	m = pressKey(t, m, tea.Key{Code: tea.KeyEsc})
	if m.subagentViewer != nil {
		t.Fatal("first Esc must close the viewer")
	}
	if m.searchPopup == nil {
		t.Fatal("first Esc must leave the monitor open")
	}
	if !m.running {
		t.Fatal("first Esc must not cancel the running turn")
	}

	m = pressKey(t, m, tea.Key{Code: tea.KeyEsc})
	if m.searchPopup != nil {
		t.Fatal("second Esc must close the monitor")
	}
	if !m.running {
		t.Fatal("second Esc must not cancel the running turn")
	}

	m = pressKey(t, m, tea.Key{Code: tea.KeyEsc})
	if m.running {
		t.Fatal("third Esc, with the stack empty, must cancel the running turn")
	}
}

// TestOverlayStack_GlobalsBlockedUnderSwallowingOverlay: the branch popup
// swallows every key it does not navigate with, so the global toggles must
// not fire around it — Up must not open history, PgUp must not scroll, and
// Ctrl+O must not flip compact output.
func TestOverlayStack_GlobalsBlockedUnderSwallowingOverlay(t *testing.T) {
	m := newTestModelFull(t)
	m.branchPopup = &branchPopupState{
		branches: []string{"main", "dev"}, selected: 1, active: "main", height: 2,
	}

	pressRune(t, m, tea.KeyUp)
	if m.searchPopup != nil {
		t.Error("Up opened the history window under an open branch popup")
	}
	if m.branchPopup == nil || m.branchPopup.selected != 0 {
		t.Fatalf("the popup did not own Up: %+v", m.branchPopup)
	}

	before := m.chatModel.Scroll
	pressRune(t, m, tea.KeyPgUp)
	if m.chatModel.Scroll != before {
		t.Error("PgUp scrolled the chat under an open branch popup")
	}

	pressRune(t, m, 'o') // Ctrl+O would toggle compact output below the popup
	if m.chatModel.ToolDisplay.CompactTools {
		t.Error("Ctrl+O toggled compact tools under an open branch popup")
	}
	// The popup's own rule survived: an unowned key dismisses it.
	if m.branchPopup != nil {
		t.Error("the unowned key did not dismiss the branch popup")
	}
}

// TestOverlayStack_FallThroughOverlayLeavesGlobalsLive pins the other half:
// the approval dialog deliberately passes unowned keys through, so the
// globals below it stay live — Ctrl+O still toggles, and the dialog is
// untouched by it.
func TestOverlayStack_FallThroughOverlayLeavesGlobalsLive(t *testing.T) {
	m := newTestModel(t)
	m.approval = &permission.ApprovalRequest{
		Tool:  "bash",
		Reply: make(chan permission.ApprovalResult, 1),
	}

	next, _ := m.handleKey(tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModCtrl}))
	if _, ok := next.(*model); !ok {
		t.Fatalf("handleKey returned %T, want *model", next)
	}

	if !m.chatModel.ToolDisplay.CompactTools {
		t.Error("Ctrl+O did not toggle compact output under the approval dialog")
	}
	if m.approval == nil {
		t.Error("Ctrl+O answered the approval dialog instead of falling through")
	}
}

// TestOverlayStack_ClearClosesEverything: clearOverlays pops the whole stack,
// top entry first, running each onClose so no backing state leaks.
func TestOverlayStack_ClearClosesEverything(t *testing.T) {
	m := newTestModel(t)
	m.approval = &permission.ApprovalRequest{
		Tool:  "bash",
		Reply: make(chan permission.ApprovalResult, 1),
	}
	m.openOverlay(overlayApproval)
	var closed []string
	m.pushOverlay(overlayEntry{
		kind:    overlayCustom,
		onClose: func(*model) { closed = append(closed, "custom") },
	})

	m.clearOverlays()

	if len(m.overlays) != 0 {
		t.Fatalf("stack holds %d entries after clear, want none", len(m.overlays))
	}
	if m.approval != nil {
		t.Error("clearOverlays left the approval dialog open")
	}
	if len(closed) != 1 || closed[0] != "custom" {
		t.Fatalf("onClose ran %v, want the top entry's onClose", closed)
	}
}

// TestOverlayStack_SyntheticOverlayNeedsNoHandleKeyEdit is the refactor's
// payoff: a brand-new overlay joins by pushing an entry, and handleKey does
// not know about it. Keys it owns never reach the layers below; keys it
// declines fall through to the prompt as usual.
func TestOverlayStack_SyntheticOverlayNeedsNoHandleKeyEdit(t *testing.T) {
	m := newTestModel(t)
	owned := false
	m.pushOverlay(overlayEntry{
		kind: overlayCustom,
		keyHandler: func(key tea.Key) (tea.Model, tea.Cmd, bool) {
			if key.Code == 'q' && key.Mod == 0 {
				owned = true
				return m, nil, true
			}
			return nil, nil, false
		},
	})

	pressRune(t, m, 'q')
	if !owned {
		t.Fatal("the synthetic overlay did not receive its key")
	}
	if len(m.inputModel.Text) != 0 {
		t.Fatalf("input = %q, want untouched (the overlay owns q)", m.inputModel.Text)
	}

	pressRune(t, m, 'a')
	if m.inputModel.Text != "a" {
		t.Fatalf("input = %q, want %q (a declined key must reach the prompt)", m.inputModel.Text, "a")
	}
}

// TestOverlayStack_SwallowsInputShadesBelow: swallowsInput turns a declined
// key into a consumed one, shading every layer below the entry.
func TestOverlayStack_SwallowsInputShadesBelow(t *testing.T) {
	decline := func(tea.Key) (tea.Model, tea.Cmd, bool) { return nil, nil, false }

	passing := newTestModel(t)
	passing.pushOverlay(overlayEntry{kind: overlayCustom, keyHandler: decline})
	pressRune(t, passing, 'a')
	if passing.inputModel.Text != "a" {
		t.Fatalf("input = %q, want the key to fall through to the prompt", passing.inputModel.Text)
	}

	shading := newTestModel(t)
	shading.pushOverlay(overlayEntry{kind: overlayCustom, keyHandler: decline, swallowsInput: true})
	pressRune(t, shading, 'a')
	if shading.inputModel.Text != "" {
		t.Fatalf("input = %q, want the declined key swallowed", shading.inputModel.Text)
	}
}

// TestOverlayStack_ApprovalGatesLiveTurn: while a tool call waits on the
// dialog, the dialog owns y/Enter/Esc — a stray Esc must deny the request,
// never cancel the turn — and only after the dialog is answered does Esc
// reach the interrupt layer.
func TestOverlayStack_ApprovalGatesLiveTurn(t *testing.T) {
	m := newTestModel(t)
	m.beginTurn()

	m.approval = &permission.ApprovalRequest{
		Tool:  "bash",
		Reply: make(chan permission.ApprovalResult, 1),
	}
	m.openOverlay(overlayApproval)

	pressRune(t, m, 'y')
	if m.approval != nil {
		t.Fatal("'y' did not resolve the approval dialog")
	}
	if !m.running {
		t.Fatal("'y' answered the dialog but also touched the running turn")
	}

	m.approval = &permission.ApprovalRequest{
		Tool:  "bash",
		Reply: make(chan permission.ApprovalResult, 1),
	}
	m.openOverlay(overlayApproval)

	pressRune(t, m, tea.KeyEsc)
	if m.approval != nil {
		t.Fatal("Esc did not deny the open dialog")
	}
	if !m.running {
		t.Fatal("Esc denied the dialog but canceled the running turn with it")
	}

	pressRune(t, m, tea.KeyEsc)
	if m.running {
		t.Fatal("Esc with the dialog gone must cancel the running turn")
	}
}
