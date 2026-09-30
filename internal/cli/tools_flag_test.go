package cli

import (
	"reflect"
	"strings"
	"testing"

	adktool "google.golang.org/adk/v2/tool"

	"github.com/spa-skyson/pi-rate/internal/config"
	"github.com/spa-skyson/pi-rate/internal/extension"
)

func TestParseToolAllowlist(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"empty means no filter", "", nil},
		{"whitespace only means no filter", "   ", nil},
		{"plain list", "read,bash", []string{"read", "bash"}},
		{"spaces around entries", " read , bash ", []string{"read", "bash"}},
		{"lowercased", "READ,Bash", []string{"read", "bash"}},
		{"empty entries dropped", "read,,bash,", []string{"read", "bash"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseToolAllowlist(tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseToolAllowlist(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

// withToolFlag sets the --tools flag for the duration of one test. The flag
// is a package global, so these tests must not run in parallel.
func withToolFlag(t *testing.T, v string) {
	t.Helper()
	orig := flagTools
	t.Cleanup(func() { flagTools = orig })
	flagTools = v
}

// stubToolNames returns the registered names of a tool list.
func stubToolNames(ts []adktool.Tool) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Name()
	}
	return out
}

func TestApplyToolAllowlist(t *testing.T) {
	// The search stub is spelled "ripgrep" — the registration a host with rg
	// installed gets — to pin the synonym against real-name plumbing.
	toolsFor := func() []adktool.Tool {
		return []adktool.Tool{
			&namedToolStub{name: "read"},
			&namedToolStub{name: "ripgrep"},
			&namedToolStub{name: "bash"},
		}
	}

	t.Run("empty allow keeps everything", func(t *testing.T) {
		withToolFlag(t, "")
		got := applyToolAllowlist(toolsFor())
		if want := []string{"read", "ripgrep", "bash"}; !reflect.DeepEqual(stubToolNames(got), want) {
			t.Errorf("kept = %v, want %v", stubToolNames(got), want)
		}
	})

	t.Run("allow keeps input order and drops the rest", func(t *testing.T) {
		withToolFlag(t, "bash, read")
		got := applyToolAllowlist(toolsFor())
		if want := []string{"read", "bash"}; !reflect.DeepEqual(stubToolNames(got), want) {
			t.Errorf("kept = %v, want %v", stubToolNames(got), want)
		}
	})

	t.Run("grep allow entry matches ripgrep registration", func(t *testing.T) {
		withToolFlag(t, "GREP")
		got := applyToolAllowlist(toolsFor())
		if want := []string{"ripgrep"}; !reflect.DeepEqual(stubToolNames(got), want) {
			t.Errorf("kept = %v, want %v", stubToolNames(got), want)
		}
	})

	t.Run("unknown name warns but does not fail", func(t *testing.T) {
		withToolFlag(t, "read, nope")
		var got []adktool.Tool
		stderr := captureStderr(t, func() { got = applyToolAllowlist(toolsFor()) })
		if want := []string{"read"}; !reflect.DeepEqual(stubToolNames(got), want) {
			t.Errorf("kept = %v, want %v", stubToolNames(got), want)
		}
		if !strings.Contains(stderr, "nope") || !strings.Contains(stderr, "matched nothing") {
			t.Errorf("stderr %q lacks the unknown-name warning", stderr)
		}
	})
}

func TestMCPServersForAllowlist(t *testing.T) {
	servers := []extension.MCPServerConfig{
		{Name: "alpha", Command: "cmd-a"},
		{Name: "Beta", Command: "cmd-b"},
		{Name: "gamma", Command: "cmd-g"},
	}

	t.Run("empty allow keeps every server", func(t *testing.T) {
		got := mcpServersForAllowlist(servers, nil)
		if !reflect.DeepEqual(got, servers) {
			t.Errorf("got %v, want all of %v", got, servers)
		}
	})

	t.Run("allow selects by name case-insensitively", func(t *testing.T) {
		got := mcpServersForAllowlist(servers, []string{"beta"})
		if len(got) != 1 || got[0].Name != "Beta" {
			t.Errorf("got %v, want only the Beta server", got)
		}
	})

	t.Run("no match keeps nothing", func(t *testing.T) {
		if got := mcpServersForAllowlist(servers, []string{"zzz"}); len(got) != 0 {
			t.Errorf("got %v, want none", got)
		}
	})
}

func TestMCPToolsetsForRun(t *testing.T) {
	// Command-only servers on purpose: BuildMCPToolsets constructs transports
	// lazily, so building toolsets spawns no processes and dials nothing.
	cfg := config.Config{MCP: &config.MCPConfig{Servers: []config.MCPServer{
		{Name: "alpha", Command: "cmd-a"},
		{Name: "beta", Command: "cmd-b"},
	}}}

	t.Run("no allow builds every configured server", func(t *testing.T) {
		withToolFlag(t, "")
		if got := mcpToolsetsForRun(cfg); len(got) != 2 {
			t.Errorf("got %d toolsets, want 2", len(got))
		}
	})

	t.Run("allow narrows to the named server", func(t *testing.T) {
		withToolFlag(t, "alpha")
		if got := mcpToolsetsForRun(cfg); len(got) != 1 {
			t.Errorf("got %d toolsets, want 1", len(got))
		}
	})

	t.Run("allow matching nothing builds none", func(t *testing.T) {
		withToolFlag(t, "zzz")
		if got := mcpToolsetsForRun(cfg); len(got) != 0 {
			t.Errorf("got %d toolsets, want 0", len(got))
		}
	})
}
