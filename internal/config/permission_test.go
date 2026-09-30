package config

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/spa-skyson/pi-rate/internal/permission"
)

// The config.json spelling a user writes — tool rules flat, bash as a
// pattern map — parses into the Rules the gate checks.
func TestPermissionInConfig(t *testing.T) {
	var cfg Config
	blob := `{"permission":{"edit":"deny","serena*":"deny","bash":{"*":"ask","git *":"allow"}}}`
	if err := json.Unmarshal([]byte(blob), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Permission == nil {
		t.Fatal("permission not parsed")
	}
	if cfg.Permission.Tools["edit"] != permission.Deny || cfg.Permission.Tools["serena*"] != permission.Deny {
		t.Errorf("Tools = %v", cfg.Permission.Tools)
	}
	wantBash := []permission.BashRule{
		{Pattern: "*", Directive: permission.Ask},
		{Pattern: "git *", Directive: permission.Allow},
	}
	if !reflect.DeepEqual(cfg.Permission.Bash, wantBash) {
		t.Errorf("Bash = %v, want %v", cfg.Permission.Bash, wantBash)
	}
}

func TestPermissionInConfig_UnknownDirectiveFailsLoad(t *testing.T) {
	var cfg Config
	if err := json.Unmarshal([]byte(`{"permission":{"edit":"denny"}}`), &cfg); err == nil {
		t.Fatal("unknown directive must fail the load")
	}
}

func TestPermissionInConfig_OmittedWhenUnset(t *testing.T) {
	blob, err := json.Marshal(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(blob), "permission") {
		t.Errorf("nil permission leaked into saved config: %s", blob)
	}
}

// The full chain: global config rules, agent rules on top, and the merged set
// decides calls the way the user's pm.md expects.
func TestPermissionMergeGlobalWithAgent(t *testing.T) {
	var cfg Config
	if err := json.Unmarshal([]byte(`{"permission":{"question":"allow","bash":{"*":"ask"}}}`), &cfg); err != nil {
		t.Fatal(err)
	}
	agentRules := permission.Rules{
		Tools: map[string]permission.Directive{"edit": permission.Deny},
		Bash:  []permission.BashRule{{Pattern: "git *", Directive: permission.Allow}},
	}

	merged := permission.Merge(*cfg.Permission, agentRules)

	if merged.Tools["question"] != permission.Allow || merged.Tools["edit"] != permission.Deny {
		t.Errorf("merged Tools = %v", merged.Tools)
	}
	if got := permission.Check(merged, "bash", map[string]any{"command": "git status"}); got.Directive != permission.Allow {
		t.Errorf("git status = %v, want allow", got.Directive)
	}
	if got := permission.Check(merged, "bash", map[string]any{"command": "make test"}); got.Directive != permission.Ask {
		t.Errorf("make test = %v, want ask", got.Directive)
	}
	if got := permission.Check(merged, "edit", nil); got.Directive != permission.Deny {
		t.Errorf("edit = %v, want deny", got.Directive)
	}
}
