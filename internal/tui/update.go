package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// updateState tracks the /update flow: the y/n confirm before the install
// script runs, and the upgrade while it runs.
type updateState struct {
	phase  string // "confirming", "upgrading"
	latest string
}

// updateCheckMsg carries the result of an async update check. confirm marks
// the bare-/update path, which opens the y/n prompt on a newer release;
// `/update check` only reports the version.
type updateCheckMsg struct {
	confirm bool
	latest  string
	err     error
}

// updateAppliedMsg carries the outcome of the install script.
type updateAppliedMsg struct {
	latest string
	err    error
}

// updateCheckTimeout bounds a /update fetch; the startup check keeps its own
// shorter budget (internal/cli).
const updateCheckTimeout = 5 * time.Second

// handleUpdateCommand implements /update: with no argument, check and — on a
// newer release — ask y/n before running the install script; `check` only
// reports the version. While an upgrade is running the command is refused.
func (m *model) handleUpdateCommand(args []string) (tea.Model, tea.Cmd) {
	if m.cfg.CheckUpdate == nil {
		m.chatModel.AppendNotice("Updates are not available in this context.")
		return m, nil
	}
	if m.update != nil && m.update.phase == "upgrading" {
		return m, m.setFlash("Already upgrading")
	}
	checkOnly := len(args) > 0 && strings.EqualFold(args[0], "check")
	if !checkOnly && m.running {
		m.chatModel.AppendNotice("Cannot update while a response is running. Wait for it to finish or cancel it first.")
		return m, nil
	}

	m.chatModel.Messages = append(m.chatModel.Messages, message{
		role:    "thinking",
		content: "Checking for updates...",
	})

	check, ctx := m.cfg.CheckUpdate, m.ctx
	return m, func() tea.Msg {
		cctx, cancel := context.WithTimeout(ctx, updateCheckTimeout)
		defer cancel()
		latest, err := check(cctx)
		return updateCheckMsg{confirm: !checkOnly, latest: latest, err: err}
	}
}

// handleUpdateCheckDone replaces the "Checking..." placeholder with the
// outcome: the y/n confirm for bare /update, a version notice for
// `/update check`, the error otherwise.
func (m *model) handleUpdateCheckDone(msg updateCheckMsg) (tea.Model, tea.Cmd) {
	var content string
	switch {
	case msg.err != nil:
		content = fmt.Sprintf("✗ Update check failed: %v", msg.err)
	case msg.latest == "":
		content = fmt.Sprintf("You are up to date (%s).", m.cfg.AppVersion)
	case !msg.confirm:
		content = fmt.Sprintf("⬆ Update available: %s → %s — run /update to upgrade.", m.cfg.AppVersion, msg.latest)
	default:
		m.update = &updateState{phase: "confirming", latest: msg.latest}
		m.openOverlay(overlayUpdate)
		content = fmt.Sprintf("⬆ Update available: %s → %s.\n\nUpgrade to %s? **y/n**",
			m.cfg.AppVersion, msg.latest, msg.latest)
	}

	reply := message{role: "assistant", content: content}
	if n := len(m.chatModel.Messages); n > 0 && m.chatModel.Messages[n-1].role == "thinking" {
		m.chatModel.Messages[n-1] = reply
	} else {
		m.chatModel.Messages = append(m.chatModel.Messages, reply)
	}
	return m, nil
}

// handleUpdateKey resolves the y/n confirm and swallows every other key while
// the flow is open — a stray press must not start an install, and nothing
// cancels the installer once it runs.
func (m *model) handleUpdateKey(key tea.Key) (tea.Model, tea.Cmd, bool) {
	if m.update == nil {
		return nil, nil, false
	}
	if m.update.phase == "upgrading" {
		return m, nil, true
	}
	switch {
	case key.Code == 'y' || key.Code == 'Y' || key.Code == tea.KeyEnter:
		model, cmd := m.handleUpdateConfirm()
		return model, cmd, true
	case key.Code == 'n' || key.Code == 'N' || isCancelKey(key):
		model, cmd := m.handleUpdateCancel()
		return model, cmd, true
	default:
		return m, nil, true
	}
}

// handleUpdateConfirm runs the install script asynchronously, reporting the
// outcome through updateAppliedMsg.
func (m *model) handleUpdateConfirm() (tea.Model, tea.Cmd) {
	if m.update == nil || m.update.phase != "confirming" {
		return m, nil
	}
	latest := m.update.latest
	if m.cfg.ApplyUpdate == nil {
		m.popOverlay(overlayUpdate) // onClose nils m.update
		m.chatModel.AppendNotice("Updates are not available in this context.")
		return m, nil
	}
	m.update.phase = "upgrading"
	m.chatModel.AppendNotice(fmt.Sprintf("Upgrading to %s…", latest))

	apply, ctx := m.cfg.ApplyUpdate, m.ctx
	return m, func() tea.Msg {
		return updateAppliedMsg{latest: latest, err: apply(ctx)}
	}
}

// handleUpdateCancel backs out of the confirm.
func (m *model) handleUpdateCancel() (tea.Model, tea.Cmd) {
	m.popOverlay(overlayUpdate) // onClose nils m.update
	m.chatModel.AppendNotice("Update canceled.")
	return m, nil
}

// handleUpdateApplied reports the install outcome and closes the flow.
func (m *model) handleUpdateApplied(msg updateAppliedMsg) (tea.Model, tea.Cmd) {
	m.popOverlay(overlayUpdate) // onClose nils m.update
	if msg.err != nil {
		m.chatModel.AppendNotice(fmt.Sprintf("✗ Upgrade to %s failed:\n%s", msg.latest, msg.err))
		return m, nil
	}
	m.chatModel.AppendNotice(fmt.Sprintf(
		"✓ Upgraded to %s. Restart pirate to use the new version — type /exit to quit.", msg.latest))
	return m, nil
}
