package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// chatText joins the transcript so assertions can look for one notice without
// caring where it landed.
func chatText(m *model) string {
	var b strings.Builder
	for _, msg := range m.chatModel.Messages {
		b.WriteString(msg.content)
		b.WriteString("\n")
	}
	return b.String()
}

// runUpdateCheck drives /update end to end: handler → async cmd → done msg
// through Update, the same path the TUI's event loop takes.
func runUpdateCheck(t *testing.T, m *model, args []string) *model {
	t.Helper()
	newM, cmd := m.handleUpdateCommand(args)
	mm := newM.(*model)
	if cmd == nil {
		t.Fatal("expected an async check cmd")
	}
	msg := cmd()
	if _, ok := msg.(updateCheckMsg); !ok {
		t.Fatalf("cmd produced %T, want updateCheckMsg", msg)
	}
	got, _ := mm.Update(msg)
	return got.(*model)
}

func TestUpdateCommand_UpToDate(t *testing.T) {
	m := &model{
		ctx:       context.Background(),
		chatModel: ChatModel{},
		cfg: Config{
			AppVersion: "v1.2.3",
			CheckUpdate: func(context.Context) (string, error) {
				return "", nil
			},
		},
	}

	mm := runUpdateCheck(t, m, nil)
	if mm.update != nil {
		t.Errorf("update state = %+v, want nil (nothing to confirm)", mm.update)
	}
	if !strings.Contains(chatText(mm), "You are up to date (v1.2.3)") {
		t.Errorf("chat = %q, want an up-to-date notice", chatText(mm))
	}
}

func TestUpdateCommand_ConfirmFlow(t *testing.T) {
	m := &model{
		ctx:       context.Background(),
		chatModel: ChatModel{},
		cfg: Config{
			AppVersion: "v1.2.3",
			CheckUpdate: func(context.Context) (string, error) {
				return "v1.3.0", nil
			},
		},
	}

	mm := runUpdateCheck(t, m, nil)
	if mm.update == nil || mm.update.phase != "confirming" || mm.update.latest != "v1.3.0" {
		t.Fatalf("update state = %+v, want confirming v1.3.0", mm.update)
	}
	text := chatText(mm)
	if !strings.Contains(text, "⬆ Update available: v1.2.3 → v1.3.0") ||
		!strings.Contains(text, "Upgrade to v1.3.0? **y/n**") {
		t.Errorf("chat = %q, want the confirm prompt", text)
	}
}

func TestUpdateCommand_CheckArgOnlyReports(t *testing.T) {
	m := &model{
		ctx:       context.Background(),
		chatModel: ChatModel{},
		cfg: Config{
			AppVersion: "v1.2.3",
			CheckUpdate: func(context.Context) (string, error) {
				return "v1.3.0", nil
			},
		},
	}

	mm := runUpdateCheck(t, m, []string{"check"})
	if mm.update != nil {
		t.Errorf("update state = %+v, want nil (/update check never confirms)", mm.update)
	}
	if !strings.Contains(chatText(mm), "run /update to upgrade") {
		t.Errorf("chat = %q, want the version notice", chatText(mm))
	}
}

func TestUpdateCommand_CheckError(t *testing.T) {
	m := &model{
		ctx:       context.Background(),
		chatModel: ChatModel{},
		cfg: Config{
			CheckUpdate: func(context.Context) (string, error) {
				return "", errors.New("network down")
			},
		},
	}

	mm := runUpdateCheck(t, m, nil)
	if !strings.Contains(chatText(mm), "✗ Update check failed: network down") {
		t.Errorf("chat = %q, want the failure notice", chatText(mm))
	}
}

func TestUpdateCommand_RefusedWhileRunning(t *testing.T) {
	called := false
	m := &model{
		ctx:       context.Background(),
		running:   true,
		chatModel: ChatModel{},
		cfg: Config{
			CheckUpdate: func(context.Context) (string, error) {
				called = true
				return "v1.3.0", nil
			},
		},
	}

	newM, cmd := m.handleUpdateCommand(nil)
	mm := newM.(*model)
	if cmd != nil {
		t.Error("expected no async cmd while a turn is running")
	}
	if called {
		t.Error("CheckUpdate must not run while a turn is running")
	}
	if !strings.Contains(chatText(mm), "Cannot update while a response is running") {
		t.Errorf("chat = %q, want the refusal", chatText(mm))
	}
}

func TestUpdateCommand_AlreadyUpgrading(t *testing.T) {
	called := false
	m := &model{
		ctx: context.Background(),
		update: &updateState{
			phase:  "upgrading",
			latest: "v1.3.0",
		},
		chatModel: ChatModel{},
		cfg: Config{
			CheckUpdate: func(context.Context) (string, error) {
				called = true
				return "", nil
			},
		},
	}

	newM, cmd := m.handleUpdateCommand(nil)
	mm := newM.(*model)
	if called {
		t.Error("CheckUpdate must not re-run during an upgrade")
	}
	if cmd == nil {
		t.Fatal("expected the flash cmd")
	}
	if mm.flash != "Already upgrading" {
		t.Errorf("flash = %q, want %q", mm.flash, "Already upgrading")
	}
	if n := len(mm.chatModel.Messages); n != 0 {
		t.Errorf("chat gained %d messages during upgrade, want 0", n)
	}
}

func TestUpdateCommand_NoChecker(t *testing.T) {
	m := &model{ctx: context.Background(), chatModel: ChatModel{}}

	newM, cmd := m.handleUpdateCommand(nil)
	mm := newM.(*model)
	if cmd != nil {
		t.Error("expected no cmd when CheckUpdate is nil")
	}
	if !strings.Contains(chatText(mm), "Updates are not available") {
		t.Errorf("chat = %q, want the unavailable notice", chatText(mm))
	}
}

func TestUpdateKey_YesRunsInstaller(t *testing.T) {
	applied := 0
	m := &model{
		ctx: context.Background(),
		update: &updateState{
			phase:  "confirming",
			latest: "v1.3.0",
		},
		chatModel: ChatModel{},
		cfg: Config{
			ApplyUpdate: func(context.Context) error {
				applied++
				return nil
			},
		},
	}

	// 'y' confirms: the state turns "upgrading", the installer cmd is armed,
	// and the chat shows the Upgrading notice.
	newM, cmd, _ := m.handleUpdateKey(tea.Key{Code: 'y'})
	mm := newM.(*model)
	if mm.update == nil || mm.update.phase != "upgrading" {
		t.Fatalf("phase = %+v, want upgrading", mm.update)
	}
	if !strings.Contains(chatText(mm), "Upgrading to v1.3.0") {
		t.Errorf("chat = %q, want the upgrading notice", chatText(mm))
	}
	if applied != 0 {
		t.Fatal("installer ran before its cmd fired")
	}

	// Fire the cmd and feed the result through Update.
	msg := cmd()
	got, _ := mm.Update(msg)
	final := got.(*model)
	if applied != 1 {
		t.Fatalf("installer ran %d times, want 1", applied)
	}
	if final.update != nil {
		t.Errorf("update state = %+v, want nil after completion", final.update)
	}
	if !strings.Contains(chatText(final), "✓ Upgraded to v1.3.0") ||
		!strings.Contains(chatText(final), "Restart pirate") {
		t.Errorf("chat = %q, want the success notice", chatText(final))
	}
}

func TestUpdateKey_NoCancels(t *testing.T) {
	applied := 0
	m := &model{
		ctx: context.Background(),
		update: &updateState{
			phase:  "confirming",
			latest: "v1.3.0",
		},
		chatModel: ChatModel{},
		cfg: Config{
			ApplyUpdate: func(context.Context) error {
				applied++
				return nil
			},
		},
	}

	for _, key := range []tea.Key{{Code: 'n'}, {Code: tea.KeyEsc}} {
		newM, cmd, _ := m.handleUpdateKey(key)
		mm := newM.(*model)
		if cmd != nil {
			t.Errorf("key %v: expected no cmd", key)
		}
		if mm.update != nil {
			t.Errorf("key %v: update state = %+v, want nil after cancel", key, mm.update)
		}
		if applied != 0 {
			t.Fatalf("installer ran on cancel")
		}
		if !strings.Contains(chatText(mm), "Update canceled.") {
			t.Errorf("key %v: chat = %q, want the cancel notice", key, chatText(mm))
		}
		// Reset for the second pass.
		m.update = &updateState{phase: "confirming", latest: "v1.3.0"}
		m.chatModel.Messages = nil
	}
}

func TestUpdateKey_SwallowsOtherKeys(t *testing.T) {
	m := &model{
		ctx: context.Background(),
		update: &updateState{
			phase:  "confirming",
			latest: "v1.3.0",
		},
		chatModel: ChatModel{},
	}

	newM, cmd, handled := m.handleUpdateKey(tea.Key{Code: 'x'})
	if !handled || cmd != nil {
		t.Errorf("handled = %v, cmd = %v, want swallowed with no cmd", handled, cmd)
	}
	if mm := newM.(*model); mm.update == nil || mm.update.phase != "confirming" {
		t.Errorf("stray key changed the state: %+v", mm.update)
	}
}

func TestUpdateKey_InactiveWhenClosed(t *testing.T) {
	m := &model{ctx: context.Background()}
	_, _, handled := m.handleUpdateKey(tea.Key{Code: 'y'})
	if handled {
		t.Error("a nil update state must leave the key to the prompt")
	}
}

func TestUpdateApplied_ErrorCarriesScriptOutput(t *testing.T) {
	m := &model{
		ctx:       context.Background(),
		chatModel: ChatModel{},
		cfg: Config{
			ApplyUpdate: func(context.Context) error {
				return errors.New("exit status 1: curl: (22) not found")
			},
		},
	}
	m.update = &updateState{phase: "upgrading", latest: "v1.3.0"}

	got, _ := m.Update(updateAppliedMsg{
		latest: "v1.3.0",
		err:    errors.New("curl: (22) The requested URL returned error: 404\nexit status 1"),
	})
	mm := got.(*model)
	if mm.update != nil {
		t.Errorf("update state = %+v, want nil after failure", mm.update)
	}
	text := chatText(mm)
	if !strings.Contains(text, "✗ Upgrade to v1.3.0 failed") ||
		!strings.Contains(text, "404") {
		t.Errorf("chat = %q, want the failure with the script's output", text)
	}
}
