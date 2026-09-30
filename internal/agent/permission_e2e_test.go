package agent

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/genai"

	"github.com/spa-skyson/pi-rate/internal/permission"
	"github.com/spa-skyson/pi-rate/internal/tools"
)

// TestAskingPermissionReachesModel runs a real ADK agent whose before-tool
// chain is the asking permission callback, against a model that calls bash
// once and then finishes. It is the end-to-end half of the callback tests: the
// asker's answer must be what the model actually receives as the tool result —
// approval runs the command, denial returns the dialog error in its place.
//
// The bash command is bounded to `echo`, so both branches are side-effect free.
func TestAskingPermissionReachesModel(t *testing.T) {
	rules := permission.Rules{
		Bash: []permission.BashRule{
			{Pattern: "echo *", Directive: permission.Ask},
		},
	}

	// run drives one turn and returns every function response the model saw.
	run := func(t *testing.T, answer permission.ApprovalResult) []map[string]any {
		t.Helper()

		dir := t.TempDir()
		ct, err := tools.CoreTools(testSandbox(t, dir))
		if err != nil {
			t.Fatal(err)
		}
		asked := 0
		cb := NewAskingPermissionCallback(rules, func(_ context.Context, req permission.ApprovalRequest) permission.ApprovalResult {
			asked++
			return answer
		})
		llm := &toolCallingLLM{
			name: "ask-e2e",
			functionCall: &genai.FunctionCall{
				ID:   "c1",
				Name: "bash",
				Args: map[string]any{"command": "echo approval-e2e-marker"},
			},
			finalText: "done",
		}
		a, err := New(Config{Model: llm, Tools: ct, Instruction: "t", BeforeToolCallbacks: []BeforeToolCallback{cb}})
		if err != nil {
			t.Fatal(err)
		}
		sid, _, err := a.CreateSession(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		var responses []map[string]any
		for ev, err := range a.Run(context.Background(), sid, "run it") {
			if err != nil {
				t.Fatal(err)
			}
			if ev == nil || ev.Content == nil {
				continue
			}
			for _, p := range ev.Content.Parts {
				if p.FunctionResponse != nil {
					responses = append(responses, p.FunctionResponse.Response)
				}
			}
		}
		if asked != 1 {
			t.Errorf("asker consulted %d times, want 1", asked)
		}
		return responses
	}

	t.Run("approval runs the tool", func(t *testing.T) {
		responses := run(t, permission.ApprovalResult{Allowed: true})
		if len(responses) != 1 {
			t.Fatalf("got %d function responses, want 1", len(responses))
		}
		joined := fmtResponse(responses[0])
		if !strings.Contains(joined, "approval-e2e-marker") {
			t.Errorf("approved call did not execute; model saw:\n%s", joined)
		}
		if strings.Contains(joined, "denied by user") {
			t.Errorf("approval produced a denial: %s", joined)
		}
	})

	t.Run("denial returns the dialog error to the model", func(t *testing.T) {
		responses := run(t, permission.ApprovalResult{})
		if len(responses) != 1 {
			t.Fatalf("got %d function responses, want 1", len(responses))
		}
		resp := responses[0]
		errText, _ := resp["error"].(string)
		if !strings.Contains(errText, "denied by user in the approval dialog for tool bash") {
			t.Errorf("denial did not reach the model; response: %v", resp)
		}
		// The tool's stdout key must be absent: the command never ran. (The
		// command line itself is quoted inside the error, so a substring check
		// for the marker cannot tell execution from refusal.)
		if _, ran := resp["stdout"]; ran {
			t.Errorf("denied call executed anyway: %v", resp)
		}
	})
}

// fmtResponse renders one function-response map for failure messages.
func fmtResponse(m map[string]any) string {
	var b strings.Builder
	for k, v := range m {
		b.WriteString(k)
		b.WriteString(": ")
		if s, ok := v.(string); ok {
			b.WriteString(s)
		} else {
			b.WriteString("?")
		}
		b.WriteString("\n")
	}
	return b.String()
}
