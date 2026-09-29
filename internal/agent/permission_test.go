package agent

import (
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/permission"
)

// nameOnlyTool exposes just the name the permission gate matches on.
type nameOnlyTool struct{ name string }

func (t nameOnlyTool) Name() string        { return t.name }
func (t nameOnlyTool) Description() string { return "" }
func (t nameOnlyTool) IsLongRunning() bool { return false }

func TestNewPermissionCallback(t *testing.T) {
	rules := permission.Rules{
		Tools: map[string]permission.Directive{
			"read":    permission.Allow,
			"edit":    permission.Deny,
			"serena*": permission.Deny,
		},
		Bash: []permission.BashRule{
			{Pattern: "git *", Directive: permission.Allow},
			{Pattern: "rm *", Directive: permission.Deny},
			{Pattern: "*", Directive: permission.Ask},
		},
	}
	cb := NewPermissionCallback(rules)

	t.Run("allow passes through untouched", func(t *testing.T) {
		res, err := cb(nil, nameOnlyTool{"read"}, nil)
		if res != nil || err != nil {
			t.Errorf("allow returned (%v, %v), want (nil, nil)", res, err)
		}
	})

	t.Run("deny blocks with the rule named", func(t *testing.T) {
		res, err := cb(nil, nameOnlyTool{"edit"}, nil)
		if res != nil {
			t.Errorf("deny must not substitute a result: %v", res)
		}
		if err == nil {
			t.Fatal("deny must return an error")
		}
		if want := `permission denied by rule "edit" for tool edit`; err.Error() != want {
			t.Errorf("err = %q, want %q", err, want)
		}
	})

	t.Run("deny quotes the refused bash command", func(t *testing.T) {
		_, err := cb(nil, nameOnlyTool{"bash"}, map[string]any{"command": "rm -rf build"})
		if err == nil {
			t.Fatal("want error")
		}
		if want := `permission denied by rule "rm *" for tool bash (command: "rm -rf build")`; err.Error() != want {
			t.Errorf("err = %q, want %q", err, want)
		}
	})

	t.Run("allowed bash command passes", func(t *testing.T) {
		res, err := cb(nil, nameOnlyTool{"bash"}, map[string]any{"command": "git status"})
		if res != nil || err != nil {
			t.Errorf("allowed command returned (%v, %v), want (nil, nil)", res, err)
		}
	})

	t.Run("ask denies in a non-interactive session", func(t *testing.T) {
		_, err := cb(nil, nameOnlyTool{"bash"}, map[string]any{"command": "make test"})
		if err == nil {
			t.Fatal("want error")
		}
		if want := "tool bash requires interactive approval; denied in a non-interactive session"; err.Error() != want {
			t.Errorf("err = %q, want %q", err, want)
		}
	})

	t.Run("glob match", func(t *testing.T) {
		_, err := cb(nil, nameOnlyTool{"serena_find"}, nil)
		if err == nil || !strings.Contains(err.Error(), `rule "serena*"`) {
			t.Errorf("glob rule not applied: %v", err)
		}
	})
}
