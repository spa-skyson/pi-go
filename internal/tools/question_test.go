package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/spa-skyson/pi-rate/internal/testenv"
)

// questionArgs is a valid minimal argument map.
func questionArgs() map[string]any {
	return map[string]any{
		"question": "Which database should we use?",
		"options": []any{
			map[string]any{"label": "Postgres", "description": "boring and solid"},
			map[string]any{"label": "SQLite"},
		},
	}
}

func TestValidateQuestionInput(t *testing.T) {
	boolPtr := func(b bool) *bool { return &b }

	tests := []struct {
		name    string
		input   QuestionInput
		wantErr string // empty = valid
	}{
		{"valid", QuestionInput{Question: "q", Options: []QuestionOption{{Label: "a"}}}, ""},
		{"free text off is valid", QuestionInput{Question: "q", Options: []QuestionOption{{Label: "a"}}, AllowFreeText: boolPtr(false)}, ""},
		{"empty question", QuestionInput{Options: []QuestionOption{{Label: "a"}}}, "question must not be empty"},
		{"blank question", QuestionInput{Question: "   ", Options: []QuestionOption{{Label: "a"}}}, "question must not be empty"},
		{"no options", QuestionInput{Question: "q"}, "options must list 1..8"},
		{"too many options", QuestionInput{
			Question: "q",
			Options:  []QuestionOption{{Label: "1"}, {Label: "2"}, {Label: "3"}, {Label: "4"}, {Label: "5"}, {Label: "6"}, {Label: "7"}, {Label: "8"}, {Label: "9"}},
		}, "lists 9 entries, max is 8"},
		{"empty label", QuestionInput{Question: "q", Options: []QuestionOption{{Label: "a"}, {Label: "  "}}}, "option 2: label must not be empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateQuestionInput(tt.input)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateQuestionInput() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateQuestionInput() = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

// TestQuestionTool_HeadlessCanceled pins the no-bridge contract: with a nil
// notifier the tool never blocks — it returns canceled with the note telling
// the model to decide and proceed.
func TestQuestionTool_HeadlessCanceled(t *testing.T) {
	q, err := newQuestionTool(nil)
	if err != nil {
		t.Fatalf("newQuestionTool: %v", err)
	}
	out := runTool(t, q, questionArgs())
	if out["selected"] != "canceled" {
		t.Errorf("selected = %v, want canceled", out["selected"])
	}
	note, _ := out["note"].(string)
	if !strings.Contains(note, "decide yourself") {
		t.Errorf("note = %q, want the decide-yourself hint", note)
	}
	if _, has := out["label"]; has {
		t.Errorf("canceled headless result carries a label: %#v", out)
	}
}

// TestQuestionTool_NotifierRoundTrip runs the full bridge: the notifier gets
// the request with its options and free-text flag, the answer travels back
// into the tool result.
func TestQuestionTool_NotifierRoundTrip(t *testing.T) {
	var got QuestionRequest
	q, err := newQuestionTool(func(req QuestionRequest) {
		got = req
		req.Reply <- QuestionAnswer{Selected: "option", Label: "Postgres", Index: 0}
	})
	if err != nil {
		t.Fatalf("newQuestionTool: %v", err)
	}
	out := runTool(t, q, questionArgs())

	if got.Question != "Which database should we use?" {
		t.Errorf("request question = %q", got.Question)
	}
	if len(got.Options) != 2 || got.Options[0].Label != "Postgres" || got.Options[1].Description != "" {
		t.Errorf("request options = %+v", got.Options)
	}
	if !got.AllowFreeText {
		t.Error("omitted allow_free_text must default to true")
	}
	if cap(got.Reply) != 1 {
		t.Errorf("Reply buffer = %d, want 1", cap(got.Reply))
	}

	if out["selected"] != "option" || out["label"] != "Postgres" {
		t.Errorf("output = %#v, want selected=option label=Postgres", out)
	}
	// Index carries omitempty: the first option's 0 is absent by design —
	// the label is the authoritative answer. Pin the documented quirk and
	// the presence for a later position.
	if _, has := out["index"]; has {
		t.Errorf("first option must not carry an index: %#v", out)
	}

	q2, err := newQuestionTool(func(req QuestionRequest) {
		req.Reply <- QuestionAnswer{Selected: "option", Label: "SQLite", Index: 1}
	})
	if err != nil {
		t.Fatalf("newQuestionTool: %v", err)
	}
	out2 := runTool(t, q2, questionArgs())
	if out2["index"] != float64(1) {
		t.Errorf("index = %v, want 1 for the second option", out2["index"])
	}
}

// TestQuestionTool_FreeTextFlagAndCustomAnswer checks the flag reaches the
// request and a custom answer round-trips with the typed label.
func TestQuestionTool_FreeTextFlagAndCustomAnswer(t *testing.T) {
	off := false
	var allowFree bool
	q, err := newQuestionTool(func(req QuestionRequest) {
		allowFree = req.AllowFreeText
		req.Reply <- QuestionAnswer{Selected: "custom", Label: "neither — file an issue"}
	})
	if err != nil {
		t.Fatalf("newQuestionTool: %v", err)
	}

	args := questionArgs()
	args["allow_free_text"] = &off
	out := runTool(t, q, args)

	if allowFree {
		t.Error("allow_free_text=false did not reach the request")
	}
	if out["selected"] != "custom" || out["label"] != "neither — file an issue" {
		t.Errorf("output = %#v, want selected=custom with the typed label", out)
	}
	if _, has := out["index"]; has {
		t.Errorf("custom answer carries an index: %#v", out)
	}
}

// TestQuestionTool_ContextCancel pins the turn-cancel path: with the turn's
// context canceled while nobody answers, the tool returns canceled instead of
// parking forever.
func TestQuestionTool_ContextCancel(t *testing.T) {
	q, err := newQuestionTool(func(QuestionRequest) {})
	if err != nil {
		t.Fatalf("newQuestionTool: %v", err)
	}
	r, ok := q.(runnableTool)
	if !ok {
		t.Fatalf("question tool %T does not implement Run", q)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan map[string]any, 1)
	go func() {
		out, err := r.Run(mockToolCtx{Context: ctx}, questionArgs())
		if err != nil {
			t.Errorf("Run error: %v", err)
		}
		done <- out
	}()
	cancel()

	out := <-done
	if out["selected"] != "canceled" {
		t.Errorf("selected = %v, want canceled after ctx cancel", out["selected"])
	}
}

// TestCoreTools_QuestionRegistered pins that the tool registers
// unconditionally — headless sessions get the immediate-canceled variant, so
// the model is never offered a call that hangs.
func TestCoreTools_QuestionRegistered(t *testing.T) {
	sb := testSandbox(t, t.TempDir())
	built, err := CoreTools(sb)
	if err != nil {
		t.Fatalf("CoreTools: %v", err)
	}
	found := false
	for _, x := range built {
		if x.Name() == "question" {
			found = true
		}
	}
	if !found {
		t.Error("question tool not registered by CoreTools")
	}
}

// TestQuestionTool_ValidationErrorIsAnError keeps the model-visible contract:
// bad input is a returned error (the model reads it), not a panic or a
// silent canceled.
func TestQuestionTool_ValidationErrorIsAnError(t *testing.T) {
	testenv.SetHome(t, t.TempDir())

	q, err := newQuestionTool(func(QuestionRequest) {
		t.Error("notifier must not fire on an invalid call")
	})
	if err != nil {
		t.Fatalf("newQuestionTool: %v", err)
	}
	r := q.(runnableTool)
	if _, err := r.Run(mockToolCtx{Context: context.Background()}, map[string]any{
		"question": "q", "options": []any{},
	}); err == nil || !strings.Contains(err.Error(), "options must list") {
		t.Errorf("empty options: err = %v, want the options hint", err)
	}
}
