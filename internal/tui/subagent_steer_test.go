package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// lastNotice returns the most recent transcript notice, or "".
func lastNotice(m *model) string {
	for i := len(m.chatModel.Messages) - 1; i >= 0; i-- {
		if m.chatModel.Messages[i].isNotice {
			return m.chatModel.Messages[i].content
		}
	}
	return ""
}

// openMonitor prepares the monitor popup with a steer bridge recording calls.
func openMonitor(t *testing.T) (*model, *[]string) {
	t.Helper()
	m := monitorTestModel(t)
	steered := &[]string{}
	m.cfg.SteerSubagent = func(agentID, text string) error {
		*steered = append(*steered, agentID+"|"+text)
		return nil
	}
	m.newSearchPopup(searchModeSubagents)
	return m, steered
}

// `s` on a running row opens the mini-input; typing and Enter deliver the text
// through Config.SteerSubagent and close the input with a notice.
func TestSubagentSteer_OpenTypeSend(t *testing.T) {
	m, steered := openMonitor(t)
	selectRow(t, m, "explore-1727000000000000000")

	m = pressKey(t, m, tea.Key{Code: 's', Text: "s"})
	if m.steerInput == nil {
		t.Fatal("steer input did not open on a running row")
	}
	if m.steerInput.agentID != "explore-1727000000000000000" {
		t.Errorf("steer input agentID = %q, want the running agent", m.steerInput.agentID)
	}
	if len(*steered) != 0 {
		t.Errorf("steer sent before Enter: %v", *steered)
	}

	m = pressKey(t, m, tea.Key{Code: 'h', Text: "h"})
	m = pressKey(t, m, tea.Key{Code: 'i', Text: "i"})
	m = pressKey(t, m, tea.Key{Code: tea.KeyEnter})

	if want := "explore-1727000000000000000|hi"; len(*steered) != 1 || (*steered)[0] != want {
		t.Fatalf("steered = %v, want [%q]", *steered, want)
	}
	if m.steerInput != nil {
		t.Error("steer input should close after Enter")
	}
	if got := lastNotice(m); !strings.Contains(got, "steer queued") {
		t.Errorf("notice after send = %q, want it to mention the queueing", got)
	}
}

// Esc backs out without sending; Enter on an empty input sends nothing either.
func TestSubagentSteer_EscAndEmptyEnterCancel(t *testing.T) {
	m, steered := openMonitor(t)
	selectRow(t, m, "explore-1727000000000000000")
	m = pressKey(t, m, tea.Key{Code: 's', Text: "s"})

	m = pressKey(t, m, tea.Key{Code: tea.KeyEsc})
	if m.steerInput != nil {
		t.Fatal("Esc should close the steer input")
	}
	if len(*steered) != 0 {
		t.Errorf("Esc sent a steer: %v", *steered)
	}

	// Reopen and confirm an empty input: Enter closes it, sends nothing.
	m = pressKey(t, m, tea.Key{Code: 's', Text: "s"})
	m = pressKey(t, m, tea.Key{Code: tea.KeyEnter})
	if m.steerInput != nil {
		t.Error("Enter on empty input should close it")
	}
	if len(*steered) != 0 {
		t.Errorf("empty Enter sent a steer: %v", *steered)
	}
	if got := lastNotice(m); strings.Contains(got, "steer queued") {
		t.Errorf("unexpected queue notice: %q", got)
	}
}

// A row that is not explicitly running refuses to open the input.
func TestSubagentSteer_NonRunningRowShowsNotice(t *testing.T) {
	m, steered := openMonitor(t)
	selectRow(t, m, "done-5555") // completed

	m = pressKey(t, m, tea.Key{Code: 's', Text: "s"})

	if m.steerInput != nil {
		t.Fatal("steer input opened on a completed row")
	}
	if len(*steered) != 0 {
		t.Errorf("steer sent from a completed row: %v", *steered)
	}
	if got := lastNotice(m); !strings.Contains(got, "only running agents") {
		t.Errorf("notice = %q, want the only-running explanation", got)
	}
}

// Without the cli bridge the key is inert and says so.
func TestSubagentSteer_NilFunctionShowsNotice(t *testing.T) {
	m := monitorTestModel(t)
	m.cfg.SteerSubagent = nil
	m.newSearchPopup(searchModeSubagents)
	selectRow(t, m, "explore-1727000000000000000")

	m = pressKey(t, m, tea.Key{Code: 's', Text: "s"})

	if m.steerInput != nil {
		t.Fatal("steer input opened without a SteerSubagent bridge")
	}
	if got := lastNotice(m); !strings.Contains(got, "not available") {
		t.Errorf("notice = %q, want the not-available explanation", got)
	}
}

// The steer_queued card event renders as one fixed marker line.
func TestSubagentSteer_CardRendersMarker(t *testing.T) {
	evs := renderableAgentEvents([]agentEv{
		{kind: "text", content: "working"},
		{kind: "steer_queued"},
	})
	if len(evs) != 2 {
		t.Fatalf("renderableAgentEvents = %d events, want 2 (steer_queued must pass)", len(evs))
	}
	st := newAgentEventStyles("explore", Palette{})
	lines := agentEventLines(evs[1], st, 80)
	if len(lines) != 1 {
		t.Fatalf("steer_queued rendered %d lines, want 1", len(lines))
	}
	if !strings.Contains(lines[0], "⏎ steer queued") {
		t.Errorf("steer_queued line = %q, want the marker", lines[0])
	}
}
