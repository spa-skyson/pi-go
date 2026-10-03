package tui

import (
	"strings"
	"testing"
)

// TestHandleAgentToolResult_StepLimitNotice is the UX half of issue #51: when
// the subagent tool returns a result whose results[].error names the step
// budget, the transcript gains a notice saying the report is partial — the
// result must not read as a successful completion.
func TestHandleAgentToolResult_StepLimitNotice(t *testing.T) {
	m := &model{
		chatModel: ChatModel{},
		agentCh:   make(chan agentMsg, 8),
	}

	result := `{"mode":"single","results":[{"agent":"golang-pro","agent_id":"ag-1","status":"failed",` +
		`"error":"subagent ag-1 stopped: steps limit 150 reached","result":"partial report"}],` +
		`"summary":"golang-pro failed in 49m40s"}`
	m.handleAgentToolResult(agentToolResultMsg{id: "fc-1", name: "subagent", content: result})

	// No card was open, so the only new transcript entry is the notice.
	if len(m.chatModel.Messages) != 1 {
		t.Fatalf("messages = %d, want exactly the notice", len(m.chatModel.Messages))
	}
	notice := m.chatModel.Messages[0]
	if !notice.isNotice {
		t.Errorf("notice isNotice = false, want true")
	}
	for _, want := range []string{"golang-pro", "hit its step budget", "report is partial"} {
		if !strings.Contains(notice.content, want) {
			t.Errorf("notice %q missing %q", notice.content, want)
		}
	}

	// A second step-limited result in the same payload still notices — with
	// every affected agent named.
	m2 := &model{chatModel: ChatModel{}, agentCh: make(chan agentMsg, 8)}
	m2.handleAgentToolResult(agentToolResultMsg{id: "fc-2", name: "subagent", content: `{"mode":"parallel","results":[{"agent":"a","status":"failed","error":"steps limit 150 reached"},` +
		`{"agent":"b","status":"completed","result":"ok"},{"agent":"c","status":"failed","error":"steps limit 3 reached"}]}`})
	if len(m2.chatModel.Messages) != 1 {
		t.Fatalf("parallel: messages = %d, want exactly the notice", len(m2.chatModel.Messages))
	}
	for _, want := range []string{"a", "c", "hit their step budget"} {
		if !strings.Contains(m2.chatModel.Messages[0].content, want) {
			t.Errorf("parallel notice %q missing %q", m2.chatModel.Messages[0].content, want)
		}
	}
	if strings.Contains(m2.chatModel.Messages[0].content, "`b`") {
		t.Errorf("parallel notice names the successful agent b: %q", m2.chatModel.Messages[0].content)
	}
}

// TestHandleAgentToolResult_SuccessNoNotice proves an ordinary result gains
// no notice: the warning is reserved for results that actually name the step
// budget.
func TestHandleAgentToolResult_SuccessNoNotice(t *testing.T) {
	m := &model{chatModel: ChatModel{}, agentCh: make(chan agentMsg, 8)}

	before := len(m.chatModel.Messages)
	m.handleAgentToolResult(agentToolResultMsg{id: "fc-1", name: "subagent", content: `{"mode":"single","results":[{"agent":"golang-pro","agent_id":"ag-1","status":"completed",` +
		`"result":"finished report"}],"summary":"golang-pro completed in 5s"}`})

	if len(m.chatModel.Messages) != before {
		t.Fatalf("messages grew to %d, want %d — a successful result must not notice", len(m.chatModel.Messages), before)
	}

	// The same holds when a different failure is present: no steps limit, no
	// notice.
	m.handleAgentToolResult(agentToolResultMsg{id: "fc-2", name: "subagent", content: `{"mode":"single","results":[{"agent":"golang-pro","agent_id":"ag-2","status":"failed",` +
		`"error":"pi process failed: exit status 1"}],"summary":"golang-pro failed in 5s"}`})
	if len(m.chatModel.Messages) != before {
		t.Fatalf("messages grew to %d after a non-step-limit failure, want %d", len(m.chatModel.Messages), before)
	}
}

// TestStepLimitedAgents_NonJSONIsSilent pins the guard on the parser: tool
// output that is not subagent JSON (bash noise, truncated payloads) yields no
// names rather than a panic or a false notice.
func TestStepLimitedAgents_NonJSONIsSilent(t *testing.T) {
	for _, content := range []string{"", "plain output", `{"results":"not-an-array"}`, `{"results":[{"agent":"x"}]}`} {
		if got := stepLimitedAgents(content); got != nil {
			t.Errorf("stepLimitedAgents(%q) = %v, want nil", content, got)
		}
	}
}
