package subagent

import (
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/permission"
)

// The real-world shape: flat tool rules plus a nested bash block with quoted
// patterns.
func TestParseAgentContent_PermissionBlock(t *testing.T) {
	content := `---
name: pm
description: Project manager
permission:
  edit: deny
  serena*: deny
  question: allow
  external_directory: allow
  bash:
    "*": ask
    "git *": allow
    "cd * && git *": allow
---

Instructions body here.
`
	cfg, err := parseAgentContent(content, "pm.md")
	if err != nil {
		t.Fatal(err)
	}

	wantTools := map[string]permission.Directive{
		"edit":               permission.Deny,
		"serena*":            permission.Deny,
		"question":           permission.Allow,
		"external_directory": permission.Allow,
	}
	if len(cfg.Permission.Tools) != len(wantTools) {
		t.Fatalf("Tools = %v, want %v", cfg.Permission.Tools, wantTools)
	}
	for k, v := range wantTools {
		if cfg.Permission.Tools[k] != v {
			t.Errorf("Tools[%q] = %v, want %v", k, cfg.Permission.Tools[k], v)
		}
	}

	wantBash := []permission.BashRule{
		{Pattern: "*", Directive: permission.Ask},
		{Pattern: "git *", Directive: permission.Allow},
		{Pattern: "cd * && git *", Directive: permission.Allow},
	}
	if len(cfg.Permission.Bash) != len(wantBash) {
		t.Fatalf("Bash = %v, want %v", cfg.Permission.Bash, wantBash)
	}
	for i, b := range wantBash {
		if cfg.Permission.Bash[i] != b {
			t.Errorf("Bash[%d] = %+v, want %+v", i, cfg.Permission.Bash[i], b)
		}
	}

	if !strings.Contains(cfg.Instruction, "Instructions body here") {
		t.Errorf("instruction lost around the permission block: %q", cfg.Instruction)
	}
}

// Keys after the permission block must still parse as regular frontmatter.
func TestParseAgentContent_PermissionFollowedByKeys(t *testing.T) {
	content := `---
permission:
  edit: deny
timeout: 30000
steps: 42
---
body
`
	cfg, err := parseAgentContent(content, "x.md")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Permission.Tools["edit"] != permission.Deny {
		t.Errorf("permission not parsed: %v", cfg.Permission)
	}
	if cfg.Timeout != 30000 || cfg.Steps != 42 {
		t.Errorf("keys after the block lost: timeout=%d steps=%d", cfg.Timeout, cfg.Steps)
	}
}

func TestParseAgentContent_PermissionUnquotedPatterns(t *testing.T) {
	content := `---
permission:
  bash:
    git *: allow
    '*': ask
---
`
	cfg, err := parseAgentContent(content, "x.md")
	if err != nil {
		t.Fatal(err)
	}
	want := []permission.BashRule{
		{Pattern: "git *", Directive: permission.Allow},
		{Pattern: "*", Directive: permission.Ask},
	}
	if len(cfg.Permission.Bash) != len(want) || cfg.Permission.Bash[0] != want[0] || cfg.Permission.Bash[1] != want[1] {
		t.Errorf("Bash = %v, want %v", cfg.Permission.Bash, want)
	}
}

func TestParseAgentContent_PermissionGarbageDirectives(t *testing.T) {
	content := `---
permission:
  edit: denny
  read: allow
  bash:
    git *: maybe
    rm *: deny
---
`
	cfg, err := parseAgentContent(content, "x.md")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Permission.Tools["edit"]; ok {
		t.Errorf("unusable directive kept: %v", cfg.Permission.Tools)
	}
	if cfg.Permission.Tools["read"] != permission.Allow {
		t.Errorf("usable rule lost: %v", cfg.Permission.Tools)
	}
	if len(cfg.Permission.Bash) != 1 || cfg.Permission.Bash[0].Pattern != "rm *" {
		t.Errorf("bash rules = %v, want only the rm * deny", cfg.Permission.Bash)
	}
}

func TestParseAgentContent_PermissionScalarBash(t *testing.T) {
	// A scalar `bash: deny` gates the tool wholesale.
	content := `---
permission:
  bash: deny
  read: allow
---
`
	cfg, err := parseAgentContent(content, "x.md")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Permission.Tools["bash"] != permission.Deny {
		t.Errorf("scalar bash lost: %v", cfg.Permission.Tools)
	}
	if len(cfg.Permission.Bash) != 0 {
		t.Errorf("unexpected bash rules: %v", cfg.Permission.Bash)
	}
}

func TestParseAgentContent_NoPermissionBlock(t *testing.T) {
	cfg, err := parseAgentContent("---\nname: x\n---\nbody", "x.md")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Permission.Empty() {
		t.Errorf("no block: got %+v, want zero rules", cfg.Permission)
	}
}
