package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	adkagent "google.golang.org/adk/v2/agent"

	"github.com/spa-skyson/pi-rate/internal/permission"
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

// askingRules is the shared rule set for the asking-callback tests: one plain
// ask on a tool, one ask on a bash pattern.
func askingRules() permission.Rules {
	return permission.Rules{
		Tools: map[string]permission.Directive{
			"read": permission.Allow,
			"edit": permission.Ask,
		},
		Bash: []permission.BashRule{
			{Pattern: "git *", Directive: permission.Ask},
			{Pattern: "rm *", Directive: permission.Deny},
		},
	}
}

// countingAsker is an approver stub that answers every request with the same
// result and counts how often it was consulted.
type countingAsker struct {
	calls int
	res   permission.ApprovalResult
}

func (a *countingAsker) ask(_ context.Context, req permission.ApprovalRequest) permission.ApprovalResult {
	a.calls++
	return a.res
}

func TestNewAskingPermissionCallback(t *testing.T) {
	t.Run("ask allowed passes through", func(t *testing.T) {
		asker := &countingAsker{res: permission.ApprovalResult{Allowed: true}}
		cb := NewAskingPermissionCallback(askingRules(), asker.ask)

		res, err := cb(nil, nameOnlyTool{"edit"}, nil)
		if res != nil || err != nil {
			t.Fatalf("allowed ask returned (%v, %v), want (nil, nil)", res, err)
		}
		if asker.calls != 1 {
			t.Errorf("asker consulted %d times, want 1", asker.calls)
		}
	})

	t.Run("ask denied by user errors with the dialog wording", func(t *testing.T) {
		asker := &countingAsker{res: permission.ApprovalResult{Allowed: false}}
		cb := NewAskingPermissionCallback(askingRules(), asker.ask)

		_, err := cb(nil, nameOnlyTool{"edit"}, nil)
		if err == nil {
			t.Fatal("want error")
		}
		if want := "denied by user in the approval dialog for tool edit"; err.Error() != want {
			t.Errorf("err = %q, want %q", err, want)
		}

		_, err = cb(nil, nameOnlyTool{"bash"}, map[string]any{"command": "git push --force"})
		if err == nil || !strings.Contains(err.Error(), `denied by user in the approval dialog for tool bash (command: "git push --force")`) {
			t.Errorf("bash denial missing command context: %v", err)
		}
	})

	t.Run("always allow answers later calls for the same rule without asking", func(t *testing.T) {
		asker := &countingAsker{res: permission.ApprovalResult{Allowed: true, Always: true}}
		cb := NewAskingPermissionCallback(askingRules(), asker.ask)

		for i := 0; i < 3; i++ {
			if _, err := cb(nil, nameOnlyTool{"edit"}, nil); err != nil {
				t.Fatalf("call %d: %v", i, err)
			}
		}
		// Same rule key via bash: "git *" covers both commands.
		for _, cmd := range []string{"git status", "git log --oneline"} {
			if _, err := cb(nil, nameOnlyTool{"bash"}, map[string]any{"command": cmd}); err != nil {
				t.Fatalf("bash %q: %v", cmd, err)
			}
		}
		if asker.calls != 2 {
			t.Errorf("asker consulted %d times, want 2 (edit once, git * once)", asker.calls)
		}
	})

	t.Run("allow-then-deny rules never reach the asker", func(t *testing.T) {
		asker := &countingAsker{}
		cb := NewAskingPermissionCallback(askingRules(), asker.ask)

		if res, err := cb(nil, nameOnlyTool{"read"}, nil); res != nil || err != nil {
			t.Errorf("allow rule returned (%v, %v)", res, err)
		}
		_, err := cb(nil, nameOnlyTool{"bash"}, map[string]any{"command": "rm -rf /"})
		if err == nil || !strings.Contains(err.Error(), `permission denied by rule "rm *"`) {
			t.Errorf("deny rule not enforced before ask: %v", err)
		}
		if asker.calls != 0 {
			t.Errorf("asker consulted %d times, want 0", asker.calls)
		}
	})

	t.Run("nil asker degrades to non-interactive denial", func(t *testing.T) {
		cb := NewAskingPermissionCallback(askingRules(), nil)
		_, err := cb(nil, nameOnlyTool{"edit"}, nil)
		if err == nil || !strings.Contains(err.Error(), "requires interactive approval") {
			t.Errorf("nil asker: want hard denial, got %v", err)
		}
	})
}

// TestAskingPermissionCallbackCanceledContext pins the contract the CLI bridge
// relies on: the callback hands its own turn context to the asker, so a
// canceled turn unblocks the gate instead of parking a tool call forever.
func TestAskingPermissionCallbackCanceledContext(t *testing.T) {
	rules := permission.Rules{Tools: map[string]permission.Directive{"edit": permission.Ask}}

	ctx, cancel := context.WithCancel(context.Background())
	actx := adkagent.NewStrictContextMock(ctx)
	cb := NewAskingPermissionCallback(rules, func(ctx context.Context, req permission.ApprovalRequest) permission.ApprovalResult {
		<-ctx.Done() // the bridge's select on ctx.Done
		return permission.ApprovalResult{}
	})

	done := make(chan error, 1)
	go func() {
		_, err := cb(&actx, nameOnlyTool{"edit"}, nil)
		done <- err
	}()
	cancel()

	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "denied by user") {
			t.Fatalf("canceled ask: want user-denial error, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("callback stayed blocked after ctx cancellation")
	}
}
