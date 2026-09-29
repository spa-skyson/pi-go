package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"

	adkagent "google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"

	"github.com/dimetron/pi-go/internal/permission"
)

// NewPermissionCallback builds a before-tool callback that gates every tool
// call against the rules before the tool runs. allow returns (nil, nil) — the
// chain continues and the tool executes; deny and ask return the error the
// model receives in place of the tool result. ask is a hard denial here
// because the session has no interactive approver: headless runs and
// subagents never do, and the TUI approval dialog (phase Б2) will extend this
// one decision point rather than add another.
func NewPermissionCallback(rules permission.Rules) BeforeToolCallback {
	return func(_ adkagent.Context, t tool.Tool, args map[string]any) (map[string]any, error) {
		name := t.Name()
		d := permission.Check(rules, name, args)
		if d.Directive == permission.Allow {
			return nil, nil
		}
		return nil, d.Error(name)
	}
}

// NewAskingPermissionCallback extends NewPermissionCallback with an
// interactive path for ask: instead of a hard denial, the decision is offered
// to an approver — the TUI approval dialog — whose answer (allow / deny /
// always allow) is returned to the gate. Headless and subagent sessions keep
// NewPermissionCallback, where ask stays a denial.
//
// Blocking: the callback runs on the agent-loop goroutine and parks on ask
// until the user answers or the turn's context is canceled — that is the
// design, not a hazard. The UI stays responsive because the dialog lives in
// the TUI's own event loop; the tool call simply waits. The asker MUST return:
// either a user answer, or a denial once ctx is done (the CLI bridge selects
// on ctx.Done for exactly this), so the callback cannot hang on a canceled
// session. Late answers are harmless — Reply is buffered to one and the wait
// has already been abandoned.
//
// An "always" answer records an allow override on the matched rule — the
// pattern in request.Rule ("git *", "edit"), not the individual call — by
// rewriting a copy of the rule set (permission.ApplyOverride). It holds for
// the rest of the session, in memory only: it is never persisted, so a
// permanent always-allow remains a manual config.json decision. A nil ask
// degrades to the non-interactive denial rather than panicking.
func NewAskingPermissionCallback(
	rules permission.Rules,
	ask func(ctx context.Context, req permission.ApprovalRequest) permission.ApprovalResult,
) BeforeToolCallback {
	// live carries the rule set including session overrides. ApplyOverride
	// replaces the map and slice rather than mutating them, so the read below
	// (under mu) and the swap (under mu) need no further coordination.
	var mu sync.Mutex
	live := rules

	return func(ctx adkagent.Context, t tool.Tool, args map[string]any) (map[string]any, error) {
		name := t.Name()

		mu.Lock()
		rules := live
		mu.Unlock()

		d := permission.Check(rules, name, args)
		switch d.Directive {
		case permission.Allow:
			return nil, nil
		case permission.Deny:
			return nil, d.Error(name)
		}

		if ask == nil {
			return nil, d.Error(name)
		}
		// adkagent.Context embeds InvocationContext, which embeds
		// context.Context, so ctx carries the turn's cancellation directly.
		// The nil fallback is defensive — the ADK always passes a context.
		var cctx context.Context = ctx
		if cctx == nil {
			cctx = context.Background()
		}
		res := ask(cctx, permission.ApprovalRequest{
			Tool:    name,
			Command: d.Command,
			Rule:    d.Rule,
			Reply:   make(chan permission.ApprovalResult, 1),
		})
		if !res.Allowed {
			msg := fmt.Sprintf("denied by user in the approval dialog for tool %s", name)
			if d.Command != "" {
				msg += fmt.Sprintf(" (command: %q)", d.Command)
			}
			return nil, errors.New(msg)
		}
		if res.Always {
			mu.Lock()
			live = permission.ApplyOverride(live, d.Rule, permission.Allow)
			mu.Unlock()
		}
		return nil, nil
	}
}
