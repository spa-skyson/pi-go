package tools

import (
	"reflect"
	"testing"

	"google.golang.org/adk/v2/tool"
)

// namedTools builds a tool list carrying only registered names, via the
// shared prodTool test type.
func namedTools(names ...string) []tool.Tool {
	out := make([]tool.Tool, len(names))
	for i, n := range names {
		out[i] = &prodTool{name: n}
	}
	return out
}

func keptNames(ts []tool.Tool) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Name()
	}
	return out
}

func TestFilterToolsByName(t *testing.T) {
	tests := []struct {
		name       string
		toolNames  []string
		allow      []string
		wantKept   []string
		wantUnkept []string
	}{
		{
			name:      "empty allow keeps everything",
			toolNames: []string{"read", "bash", "ripgrep"},
			allow:     nil,
			wantKept:  []string{"read", "bash", "ripgrep"},
		},
		{
			name:      "selective keeps input order",
			toolNames: []string{"read", "bash", "write"},
			allow:     []string{"write", "read"},
			wantKept:  []string{"read", "write"},
		},
		{
			name:       "unknown names are returned sorted",
			toolNames:  []string{"read", "bash"},
			allow:      []string{"bash", "reed", "aaah"},
			wantKept:   []string{"bash"},
			wantUnkept: []string{"aaah", "reed"},
		},
		{
			name:      "case and whitespace insensitive",
			toolNames: []string{"read", "bash"},
			allow:     []string{"  BASH ", "Read"},
			wantKept:  []string{"read", "bash"},
		},
		{
			name:      "grep allow entry matches ripgrep registration",
			toolNames: []string{"read", "ripgrep"},
			allow:     []string{"grep", "read"},
			wantKept:  []string{"read", "ripgrep"},
		},
		{
			name:      "ripgrep allow entry matches grep registration",
			toolNames: []string{"read", "grep"},
			allow:     []string{"ripgrep", "read"},
			wantKept:  []string{"read", "grep"},
		},
		{
			name:      "synonym is case-insensitive both ways",
			toolNames: []string{"RipGrep"},
			allow:     []string{"GREP"},
			wantKept:  []string{"RipGrep"},
		},
		{
			name:       "synonym miss still reports unknown",
			toolNames:  []string{"read", "bash"},
			allow:      []string{"ripgrep"},
			wantKept:   []string{},
			wantUnkept: []string{"ripgrep"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kept, unknown := FilterToolsByName(namedTools(tt.toolNames...), tt.allow)
			if got := keptNames(kept); !reflect.DeepEqual(got, tt.wantKept) {
				t.Errorf("kept = %v, want %v", got, tt.wantKept)
			}
			if !reflect.DeepEqual(unknown, tt.wantUnkept) {
				t.Errorf("unknown = %v, want %v", unknown, tt.wantUnkept)
			}
		})
	}
}
