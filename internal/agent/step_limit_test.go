package agent

import (
	"strings"
	"testing"
)

// TestStepLimitCallback pins the subagent iteration budget: the first maxSteps
// calls pass through untouched, the next one fails the invocation with the
// limit named, and zero disables the guard. The callback must never mutate the
// result it is handed — it is a control-flow guard, not a transform.
func TestStepLimitCallback(t *testing.T) {
	t.Run("zero disables the limit", func(t *testing.T) {
		cb := NewStepLimitCallback(0)
		for i := range 10 {
			if _, err := cb(nil, nil, nil, nil, nil); err != nil {
				t.Fatalf("call %d: unexpected error %v", i+1, err)
			}
		}
	})

	t.Run("within limit passes, exceeding fails", func(t *testing.T) {
		cb := NewStepLimitCallback(3)
		for i := range 3 {
			if _, err := cb(nil, nil, nil, nil, nil); err != nil {
				t.Fatalf("call %d: unexpected error %v", i+1, err)
			}
		}
		_, err := cb(nil, nil, nil, nil, nil)
		if err == nil {
			t.Fatal("4th call: expected steps limit error, got nil")
		}
		if !strings.Contains(err.Error(), "steps limit 3 reached") {
			t.Fatalf("error = %v, want containing %q", err, "steps limit 3 reached")
		}
	})

	t.Run("result is never mutated or replaced", func(t *testing.T) {
		cb := NewStepLimitCallback(1)
		result := map[string]any{"stdout": "keep me", "truncated": true}
		got, err := cb(nil, nil, nil, result, nil)
		if err != nil {
			t.Fatalf("unexpected error %v", err)
		}
		if got != nil {
			t.Fatalf("callback returned a replacement result %v, want nil", got)
		}
		if len(result) != 2 || result["stdout"] != "keep me" || result["truncated"] != true {
			t.Fatalf("result was mutated: %v", result)
		}
	})
}
