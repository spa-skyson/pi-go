package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/dimetron/pi-go/internal/permission"
)

// newApprovalModel builds a minimal model with one pending approval request
// and returns the model with the request it holds. Reply is buffered, so the
// test can read the answer after the key handler returns.
func newApprovalModel(t *testing.T) (*model, *permission.ApprovalRequest) {
	t.Helper()
	m := &model{width: 100, height: 30}
	m.agentCh = make(chan agentMsg, 8)
	// The bridge channel, so the handler's re-arm produces a real command.
	m.cfg.ApprovalCh = make(chan permission.ApprovalRequest, 1)
	req := permission.ApprovalRequest{
		Tool:    "bash",
		Command: "git push --force origin main",
		Rule:    "git *",
		Reply:   make(chan permission.ApprovalResult, 1),
	}
	return m, &req
}

func TestApprovalRequest_ShowsDialogAndRenders(t *testing.T) {
	m, req := newApprovalModel(t)

	model, cmd := m.handleApprovalRequest(approvalRequestMsg{req: *req})
	if model != m {
		t.Fatal("handler must return the same model")
	}
	if cmd == nil {
		t.Fatal("handler must re-arm the approval listener")
	}
	if m.approval == nil || m.approval.Tool != "bash" {
		t.Fatalf("approval state not set: %+v", m.approval)
	}

	view := m.renderApprovalDialog(m.chatWidth())
	for _, want := range []string{"bash", "git *", "git push --force origin main", "approval", "y allow"} {
		if !strings.Contains(strings.ToLower(view), strings.ToLower(want)) {
			t.Errorf("dialog view missing %q; got:\n%s", want, view)
		}
	}
}

func TestApprovalRequest_SecondReplacesFirstWithDenial(t *testing.T) {
	m, first := newApprovalModel(t)
	if _, _ = m.handleApprovalRequest(approvalRequestMsg{req: *first}); m.approval == nil || m.approval.Tool != "bash" {
		t.Fatal("first request not active")
	}

	second := permission.ApprovalRequest{Tool: "edit", Reply: make(chan permission.ApprovalResult, 1)}
	m.handleApprovalRequest(approvalRequestMsg{req: second})

	select {
	case got := <-first.Reply:
		if got.Allowed {
			t.Error("stale request must be denied, not allowed")
		}
	default:
		t.Fatal("stale request was never answered")
	}
	if m.approval == nil || m.approval.Tool != "edit" {
		t.Fatalf("second request not active: %+v", m.approval)
	}
}

func TestApprovalKey_Answers(t *testing.T) {
	tests := []struct {
		name string
		key  tea.Key
		want permission.ApprovalResult
	}{
		{"y allows", tea.Key{Code: 'y'}, permission.ApprovalResult{Allowed: true}},
		{"Y allows", tea.Key{Code: 'Y'}, permission.ApprovalResult{Allowed: true}},
		{"enter allows", tea.Key{Code: tea.KeyEnter}, permission.ApprovalResult{Allowed: true}},
		{"a allows always", tea.Key{Code: 'a'}, permission.ApprovalResult{Allowed: true, Always: true}},
		{"A allows always", tea.Key{Code: 'A'}, permission.ApprovalResult{Allowed: true, Always: true}},
		{"n denies", tea.Key{Code: 'n'}, permission.ApprovalResult{}},
		{"N denies", tea.Key{Code: 'N'}, permission.ApprovalResult{}},
		{"q denies", tea.Key{Code: 'q'}, permission.ApprovalResult{}},
		{"esc denies", tea.Key{Code: tea.KeyEsc}, permission.ApprovalResult{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, req := newApprovalModel(t)
			m.handleApprovalRequest(approvalRequestMsg{req: *req})

			_, _, handled := m.handleApprovalKey(tt.key)
			if !handled {
				t.Fatal("dialog must own its keys")
			}
			if m.approval != nil {
				t.Error("dialog state must clear after answering")
			}
			select {
			case got := <-req.Reply:
				if got != tt.want {
					t.Errorf("reply = %+v, want %+v", got, tt.want)
				}
			default:
				t.Fatal("no answer was sent to Reply")
			}
			// The decision is recorded in the transcript as a notice.
			last := m.chatModel.Messages[len(m.chatModel.Messages)-1]
			if !last.isNotice || last.content == "" {
				t.Errorf("expected a notice fact line, got role=%q notice=%v content=%q",
					last.role, last.isNotice, last.content)
			}
		})
	}
}

func TestApprovalKey_FactLines(t *testing.T) {
	tests := []struct {
		name string
		key  tea.Key
		want string
	}{
		{"allowed bash", tea.Key{Code: 'y'}, `allowed: bash "git push --force origin main"`},
		{"always", tea.Key{Code: 'a'}, `always allowed for this session: rule "git *"`},
		{"denied", tea.Key{Code: 'n'}, `denied by user: bash "git push --force origin main"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, req := newApprovalModel(t)
			m.handleApprovalRequest(approvalRequestMsg{req: *req})
			m.handleApprovalKey(tt.key)

			last := m.chatModel.Messages[len(m.chatModel.Messages)-1]
			if last.content != tt.want {
				t.Errorf("fact line = %q, want %q", last.content, tt.want)
			}
		})
	}
}

func TestApprovalKey_OtherKeysFallThrough(t *testing.T) {
	m, req := newApprovalModel(t)
	m.handleApprovalRequest(approvalRequestMsg{req: *req})

	// A key the dialog does not own reaches the prompt input untouched.
	if _, _, handled := m.handleApprovalKey(tea.Key{Code: 'x'}); handled {
		t.Fatal("dialog must not own unrelated keys")
	}
	if m.approval == nil {
		t.Fatal("unrelated key must not dismiss the dialog")
	}
	select {
	case <-req.Reply:
		t.Fatal("unrelated key must not answer the request")
	default:
	}
}

// TestApprovalKey_EnterDoesNotSubmitInput pins the modal contract: while the
// dialog is up, Enter answers it and never reaches the prompt, so the text
// typed so far is preserved rather than sent to the agent.
func TestApprovalKey_EnterDoesNotSubmitInput(t *testing.T) {
	m, req := newApprovalModel(t)
	m.handleApprovalRequest(approvalRequestMsg{req: *req})
	m.inputModel.SetText("draft that must survive")

	model, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	if m.approval != nil {
		t.Fatal("Enter must answer the dialog")
	}
	select {
	case got := <-req.Reply:
		if !got.Allowed {
			t.Error("Enter must allow")
		}
	default:
		t.Fatal("Enter produced no answer")
	}
	if got := m.inputModel.Text; got != "draft that must survive" {
		t.Errorf("input text = %q, want it preserved", got)
	}
	if len(m.pendingPrompts) != 0 {
		t.Errorf("Enter leaked %d prompts into the queue", len(m.pendingPrompts))
	}
	_ = model
}

func TestAgentDone_DismissesPendingApproval(t *testing.T) {
	m, req := newApprovalModel(t)
	m.handleApprovalRequest(approvalRequestMsg{req: *req})

	m.handleAgentDone(agentDoneMsg{})

	if m.approval != nil {
		t.Error("turn end must dismiss a pending dialog")
	}
	// The request was not answered by the TUI: the canceled bridge answers
	// deny on ctx, so Reply stays empty here — verify it is still readable
	// (buffered 1) and nothing blocked.
	select {
	case <-req.Reply:
		t.Fatal("TUI must not answer a request it did not present")
	default:
	}
	last := m.chatModel.Messages[len(m.chatModel.Messages)-1]
	if !last.isNotice || !strings.Contains(last.content, "approval canceled") {
		t.Errorf("expected a dismissal notice, got %q", last.content)
	}
}

func TestApprovalListener_NilChannel(t *testing.T) {
	if waitForApproval(nil) != nil {
		t.Fatal("nil channel must produce no command")
	}
}
