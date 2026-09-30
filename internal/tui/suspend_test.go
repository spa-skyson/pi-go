package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/spa-skyson/pi-rate/internal/permission"
)

// suspendMsgWant is the msg a suspend command must produce; a variable so
// comparisons inside if conditions avoid the composite-literal parenthesization
// rule.
var suspendMsgWant = tea.SuspendMsg{}

// TestCtrlZ_Suspend covers the Ctrl+Z binding (#17): the key must forward
// Bubble Tea's native suspend command — restore terminal, SIGTSTP the process
// group, full repaint on resume — without touching any other model state.
func TestCtrlZ_Suspend(t *testing.T) {
	tests := []struct {
		name    string
		running bool
		mut     func(m *model)
	}{
		{"idle", false, func(m *model) {}},
		{"running turn", true, func(m *model) { m.beginTurn() }},
		{"approval dialog open", true, func(m *model) {
			m.beginTurn()
			m.approval = &permission.ApprovalRequest{
				Tool:  "bash",
				Reply: make(chan permission.ApprovalResult, 1),
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &model{}
			tt.mut(m)

			ctrlZ := (tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl}).Key()

			// Direct handler.
			_, cmd, handled := m.handleInterruptKey(ctrlZ)
			if !handled {
				t.Fatal("handleInterruptKey must own Ctrl+Z")
			}
			if msg := cmd(); msg != suspendMsgWant {
				t.Fatalf("cmd() = %T, want tea.SuspendMsg", msg)
			}

			// Through the routing chain: suspend stays reachable while a
			// turn runs and under an open approval dialog (the dialog falls
			// through unowned keys), and chain order is unchanged.
			model, cmd := m.handleKey(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
			if model != m || cmd == nil {
				t.Fatal("handleKey must route Ctrl+Z to the suspend command")
			}
			if msg := cmd(); msg != suspendMsgWant {
				t.Fatalf("handleKey cmd() = %T, want tea.SuspendMsg", msg)
			}

			// No state mutation: suspending is orthogonal to the session.
			if m.running != tt.running {
				t.Fatalf("running = %v, want %v (Ctrl+Z must not touch turn state)", m.running, tt.running)
			}
		})
	}
}

// TestCtrlZ_DoesNotTouchCtrlC pins the neighbor key: the interrupt scheme is
// explicitly out of scope, and adding Ctrl+Z must not have changed it.
func TestCtrlZ_DoesNotTouchCtrlC(t *testing.T) {
	m := &model{}
	model, cmd := m.handleKey(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if model != m || cmd == nil {
		t.Fatal("first Ctrl+C must still warn (non-nil reset command)")
	}
	if m.ctrlCCount != 1 {
		t.Fatalf("ctrlCCount = %d, want 1", m.ctrlCCount)
	}
}
