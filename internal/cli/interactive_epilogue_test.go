package cli

import (
	"bytes"
	"testing"
)

// TestPrintSessionEpilogue pins the exit banner's bytes and its writer
// injection. The helper runs after tui.Run has returned and the tea.Program
// no longer owns the terminal — taking the writer as a parameter is what
// keeps that ordering testable: the function cannot print anywhere on its
// own, so the invariant "terminal writes only after Program exit" holds by
// construction at the call site (internal/cli/interactive.go).
func TestPrintSessionEpilogue(t *testing.T) {
	tests := []struct {
		name      string
		sessionID string
		want      string
	}{
		{
			name:      "writes session and resume command",
			sessionID: "260930-1200-abcd",
			want:      "\nSession: 260930-1200-abcd\nResume:  pi --session 260930-1200-abcd\n",
		},
		{
			name:      "empty session id prints nothing",
			sessionID: "",
			want:      "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			printSessionEpilogue(&buf, tt.sessionID)
			if buf.String() != tt.want {
				t.Fatalf("epilogue = %q, want %q", buf.String(), tt.want)
			}
		})
	}
}
