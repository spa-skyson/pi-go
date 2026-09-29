package agent

import (
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
