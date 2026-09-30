package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// attentionDoneAfter is the minimum turn length that rings the bell / sends
// the notification when the turn completes. A short answer arrives while the
// user is still reading the reply; ringing at every one of them trains the
// user to ignore the signal. Ten seconds keeps it for turns long enough that
// the user plausibly switched away.
const attentionDoneAfter = 10 * time.Second

// attentionBellSeq is the ASCII BEL: the classic audible/visible bell. It is
// sent through tea.Raw, which hands the bytes to the program's output buffer —
// the renderer's own write path — never to os.Stdout directly, so the sequence
// stays ordered against frame writes (AGENTS.md, TUI output safety).
const attentionBellSeq = "\a"

// attentionNotifySeq builds an OSC 777 desktop notification
// ("\033]777;notify;Title;Body\033\\") — the urxvt/iTerm2 shape. Supporting
// terminals surface it even when the window is in the background. Title and
// body go through stripControlChars (the same OSC-envelope scrub the window
// title uses) so a hostile tool name cannot break the sequence out.
func attentionNotifySeq(title, body string) string {
	return "\033]777;notify;" + stripControlChars(title) + ";" + stripControlChars(body) + "\033\\"
}

// attentionEnabled reports whether any attention channel is on. A nil config
// (directly constructed Config — tests, non-CLI callers) means off.
func (m *model) attentionEnabled() bool {
	return m.cfg.Attention != nil && (m.cfg.Attention.BellEnabled() || m.cfg.Attention.NotifyEnabled())
}

// attentionCmd returns the command that emits the attention sequences for one
// event, or nil when attention is off or the terminal is focused — a focused
// terminal means the user is looking at the screen, and ringing at someone
// who is already reading is noise.
//
// The command is returned once per event and executed once by Bubble Tea's
// command runner, which is what makes the sequence one-shot: unlike a View
// flag it cannot repeat on later frames, so no reset bookkeeping is needed.
func (m *model) attentionCmd(title, body string) tea.Cmd {
	if !m.attentionEnabled() || m.focused {
		return nil
	}
	seq := ""
	if m.cfg.Attention.BellEnabled() {
		seq += attentionBellSeq
	}
	if m.cfg.Attention.NotifyEnabled() {
		seq += attentionNotifySeq(title, body)
	}
	if seq == "" {
		return nil
	}
	return tea.Raw(seq)
}

// turnDoneAttention is the turn-completion signal from handleAgentDone: it
// fires only for turns that ran longer than attentionDoneAfter. The zero
// turnStarted (a turn that never began — e.g. handleAgentDone after a failed
// submit) stays quiet.
func (m *model) turnDoneAttention() tea.Cmd {
	if m.turnStarted.IsZero() || time.Since(m.turnStarted) < attentionDoneAfter {
		return nil
	}
	return m.attentionCmd("π Pi-rate", "Turn complete")
}
