package tools

import (
	"sort"
	"strings"

	"google.golang.org/adk/v2/tool"
)

// canonicalToolName folds host-dependent spellings to one name. The search
// tool registers as "ripgrep" where rg is installed and "grep" elsewhere
// (newGrepTool), so one tool answers to two names and an allow entry spelled
// either way must match either registration.
func canonicalToolName(name string) string {
	if name == "ripgrep" {
		return "grep"
	}
	return name
}

// FilterToolsByName keeps only the tools named in allow, preserving the input
// order. An empty allow list keeps everything and reports no unknowns.
//
// Matching is case-insensitive on trimmed names, and grep/ripgrep are one
// tool: an allow entry spelled either way matches the search tool under
// either registration. Names in allow that matched no tool come back as
// unknown so the caller can warn about a typo; printing the warning is the
// caller's job because only it knows where its diagnostics belong.
func FilterToolsByName(tools []tool.Tool, allow []string) (kept []tool.Tool, unknown []string) {
	if len(allow) == 0 {
		return tools, nil
	}
	want := make(map[string]string, len(allow)) // canonical → first spelling seen
	for _, n := range allow {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		c := canonicalToolName(strings.ToLower(n))
		if _, ok := want[c]; !ok {
			want[c] = n
		}
	}

	kept = make([]tool.Tool, 0, len(tools))
	matched := make(map[string]bool, len(want))
	for _, t := range tools {
		c := canonicalToolName(strings.ToLower(strings.TrimSpace(t.Name())))
		if _, ok := want[c]; ok {
			kept = append(kept, t)
			matched[c] = true
		}
	}

	for c, orig := range want {
		if !matched[c] {
			unknown = append(unknown, orig)
		}
	}
	sort.Strings(unknown)
	return kept, unknown
}
