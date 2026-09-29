// Package permission implements opencode-compatible tool permission rules:
// `permission:` blocks in agent frontmatter and the global `permission` key in
// config.json gate tool calls before execution.
//
// A rule names a tool (or a glob of tool names) and a directive: allow passes
// the call through, deny and ask both block it with an error the model
// receives in place of the tool result. ask is a hard denial here because the
// session has no interactive approver; the TUI approval dialog is layered on
// later (phase Б2) and headless and subagent runs never get one.
//
// The package is a leaf: stdlib only, so both internal/config and
// internal/subagent can hold Rules without import cycles.
package permission

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"
)

// Directive is what a matched rule instructs the gate to do.
type Directive string

const (
	Allow Directive = "allow"
	Deny  Directive = "deny"
	Ask   Directive = "ask"
)

// ParseDirective parses an allow/deny/ask spelling (case-insensitive). An
// unknown value is an error naming the offending spelling, so a typoed rule
// fails its load instead of silently meaning something else.
func ParseDirective(s string) (Directive, error) {
	switch d := Directive(strings.ToLower(strings.TrimSpace(s))); d {
	case Allow, Deny, Ask:
		return d, nil
	default:
		return "", fmt.Errorf("unknown permission directive %q (want allow, deny or ask)", s)
	}
}

// BashRule is one bash command pattern and its directive. Bash is a slice
// rather than a map because order carries meaning: on a specificity tie the
// later-declared rule wins.
type BashRule struct {
	Pattern   string    `json:"pattern"`
	Directive Directive `json:"directive"`
}

// Rules is a permission rule set: tool-name globs and bash command patterns.
// The zero value permits everything.
type Rules struct {
	Tools map[string]Directive `json:"tools,omitempty"`
	Bash  []BashRule           `json:"bash,omitempty"`
}

// Empty reports whether the rule set gates nothing at all.
func (r Rules) Empty() bool { return len(r.Tools) == 0 && len(r.Bash) == 0 }

// Merge layers override (an agent's own rules) on top of base (the global
// config rules). Same-named tool keys are replaced; bash rules concatenate,
// base first, so an agent can tighten what the config allows but the config's
// patterns still stand behind its own.
func Merge(base, override Rules) Rules {
	out := Rules{Tools: make(map[string]Directive, len(base.Tools)+len(override.Tools))}
	maps.Copy(out.Tools, base.Tools)
	maps.Copy(out.Tools, override.Tools)
	out.Bash = append(out.Bash, base.Bash...)
	out.Bash = append(out.Bash, override.Bash...)
	return out
}

// Decision is the outcome of checking one tool call against a rule set.
type Decision struct {
	Directive Directive
	// Rule is the human-readable pattern that decided the call ("serena*",
	// "git *"), for error messages. Empty for an allow-by-default.
	Rule string
	// Command is the bash command line that was evaluated. Set only for bash
	// decisions, so the denial can quote what was refused.
	Command string
}

// bashTool is the only tool whose arguments carry a command line to match
// against BashRule patterns.
const bashTool = "bash"

// Check resolves the directive for one tool call.
//
// Tool names match the Tools globs; the most specific match wins (longest
// literal part, stars excluded), so a specific allow coexists with a broad
// ask. For bash — and only bash — the command line is matched against the
// Bash globs first, and a match decides outright: `"git *": allow` allows
// `git status` even when the bash tool itself is denied. No bash match falls
// back to the Tools verdict for "bash", and no Tools entry at all allows.
func Check(r Rules, toolName string, args map[string]any) Decision {
	if toolName == bashTool {
		cmd := commandArg(args)
		if d, rule, ok := matchBash(r.Bash, cmd); ok {
			return Decision{Directive: d, Rule: rule, Command: cmd}
		}
	}
	if d, rule, ok := matchTools(r.Tools, toolName); ok {
		return Decision{Directive: d, Rule: rule}
	}
	return Decision{Directive: Allow}
}

// Error renders the tool-call error a blocking decision produces: what was
// refused, by which rule, and — for a denied bash call — the command itself.
func (d Decision) Error(toolName string) error {
	switch d.Directive {
	case Deny:
		msg := fmt.Sprintf("permission denied by rule %q for tool %s", d.Rule, toolName)
		if d.Command != "" {
			msg += fmt.Sprintf(" (command: %q)", d.Command)
		}
		return errors.New(msg)
	case Ask:
		// The wording tells the model that asking again cannot help: there is
		// no one to approve it in this session.
		return fmt.Errorf("tool %s requires interactive approval; denied in a non-interactive session", toolName)
	default:
		return nil
	}
}

// ApprovalRequest is one interactive approval round-trip. The gate (the
// before-tool callback) builds it when a decision is ask and hands it to the
// session's approver — the TUI dialog — which answers exactly once by sending
// to Reply. Reply is buffered to one and created by the sender, so the
// answerer never blocks even when the wait was abandoned (turn canceled while
// the dialog was up).
type ApprovalRequest struct {
	// Tool is the tool name the gate is ruling on.
	Tool string
	// Command is the bash command line being approved; empty for non-bash
	// tools.
	Command string
	// Rule is the pattern that produced the ask ("git *", "edit"). The
	// always-allow answer is scoped to it: an "always" on `git status` under
	// rule "git *" approves every command that rule matches, for this session
	// only, in memory only — persistence is a user decision made by editing
	// config.json, never a side effect of pressing "a".
	Rule string
	// Reply carries the user's answer. Buffered to 1.
	Reply chan ApprovalResult
}

// ApprovalResult is the answer to one ApprovalRequest.
type ApprovalResult struct {
	// Allowed grants this one call.
	Allowed bool
	// Always additionally records an allow override on the request's rule for
	// the rest of the session. Meaningful only with Allowed.
	Always bool
}

// ApplyOverride returns a copy of r in which every rule whose pattern is key
// carries d. Tool keys and bash patterns are separate namespaces, so both are
// rewritten when both exist: always-allowing pattern P means "stop asking
// about the rule named P", wherever that rule sits. The input is never
// mutated — Check is running against it concurrently from the gate.
func ApplyOverride(r Rules, key string, d Directive) Rules {
	if _, ok := r.Tools[key]; ok {
		tools := make(map[string]Directive, len(r.Tools))
		maps.Copy(tools, r.Tools)
		tools[key] = d
		r.Tools = tools
	}
	replaced := false
	for _, b := range r.Bash {
		if b.Pattern == key {
			replaced = true
			break
		}
	}
	if replaced {
		bash := make([]BashRule, len(r.Bash))
		for i, b := range r.Bash {
			if b.Pattern == key {
				b.Directive = d
			}
			bash[i] = b
		}
		r.Bash = bash
	}
	return r
}

// commandArg extracts the bash tool's command string. An absent or non-string
// field counts as the empty command, so a `"*": ask` pattern still gates a
// malformed call rather than waving it through.
func commandArg(args map[string]any) string {
	if args == nil {
		return ""
	}
	cmd, _ := args["command"].(string)
	return cmd
}

// matchTools picks the most specific Tools pattern matching name. A map
// carries no declaration order, so a specificity tie is broken by the
// lexicographically largest pattern — the deterministic stand-in for "later
// declared". Ties across different directives are pathological configurations.
func matchTools(rules map[string]Directive, name string) (Directive, string, bool) {
	var best string
	var bestD Directive
	found := false
	for pattern, d := range rules {
		if !globMatch(pattern, name) {
			continue
		}
		if !found || specificity(pattern) > specificity(best) ||
			(specificity(pattern) == specificity(best) && pattern > best) {
			best, bestD, found = pattern, d, true
		}
	}
	return bestD, best, found
}

// matchBash picks the most specific Bash pattern matching the whole command
// line; on a specificity tie the later-declared rule wins.
func matchBash(rules []BashRule, cmd string) (Directive, string, bool) {
	var best string
	var bestD Directive
	found := false
	for _, r := range rules {
		if !globMatch(r.Pattern, cmd) {
			continue
		}
		if !found || specificity(r.Pattern) >= specificity(best) {
			best, bestD, found = r.Pattern, r.Directive, true
		}
	}
	return bestD, best, found
}

// globMatch reports whether s matches the shell-style glob: `*` matches any
// run of characters including none ("serena*" is a prefix, "cd * && git *"
// has literals around two stars), everything else is literal. Matching is
// case-sensitive and anchored to the whole string.
func globMatch(pattern, s string) bool {
	parts := strings.Split(pattern, "*")
	for i, p := range parts {
		parts[i] = regexp.QuoteMeta(p)
	}
	re, err := regexp.Compile("^" + strings.Join(parts, ".*") + "$")
	if err != nil {
		return false // unreachable for QuoteMeta output; fail closed
	}
	return re.MatchString(s)
}

// specificity scores how precise a pattern is: its literal length, stars
// excluded — "git *" (4) beats "*" (0), "serena_find" (11) beats "serena*" (6).
func specificity(pattern string) int {
	return len(strings.ReplaceAll(pattern, "*", ""))
}

// EnvVar carries an agent's permission rules to its child pi process. The
// orchestrator serializes the agent's frontmatter rules into it; the child
// merges them over the global config rules.
const EnvVar = "PI_AGENT_PERMISSION"

// FromEnv returns the rules serialized into this process by its spawning
// parent. An unset variable means "no agent rules" and returns the zero Rules
// with a nil error; a set-but-unparsable payload is an error — the caller
// fails the run rather than silently dropping the gate.
func FromEnv() (Rules, error) {
	v := os.Getenv(EnvVar)
	if v == "" {
		return Rules{}, nil
	}
	var r Rules
	if err := json.Unmarshal([]byte(v), &r); err != nil {
		return Rules{}, fmt.Errorf("parsing %s: %w", EnvVar, err)
	}
	return r, nil
}

// bashKey is the one reserved key in the serialized shape: it carries the
// bash command rules rather than a tool directive.
const bashKey = "bash"

// MarshalJSON emits the opencode flat shape — one key per tool rule plus a
// "bash" entry holding the command rules as an ordered array, which is what
// preserves Bash order across the env round-trip:
//
//	{"edit":"deny","serena*":"deny","bash":[{"pattern":"git *","directive":"allow"}]}
//
// A tool rule actually named "bash" is shadowed by the command-rule array; a
// ruleset holding both comes only from a frontmatter file that uses the same
// key twice, which is a pathology this does not try to serialize faithfully.
func (r Rules) MarshalJSON() ([]byte, error) {
	out := make(map[string]any, len(r.Tools)+1)
	for k, v := range r.Tools {
		out[k] = string(v)
	}
	if len(r.Bash) > 0 {
		out[bashKey] = r.Bash
	}
	return json.Marshal(out)
}

// UnmarshalJSON accepts the opencode flat shape: tool directives as top-level
// keys and "bash" either as an object of patterns to directives or as an
// array of {pattern, directive}. JSON objects carry no order, so the object
// form is read in sorted pattern order — equal-specificity ties then resolve
// alphabetically, which is the best a map-shaped source can offer. Unknown
// directives error with the rule's name.
func (r *Rules) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	out := Rules{Tools: make(map[string]Directive, len(raw))}
	for _, k := range slices.Sorted(maps.Keys(raw)) {
		v := raw[k]
		if k == bashKey {
			bash, err := unmarshalBashRules(v)
			if err != nil {
				return fmt.Errorf("permission %q: %w", k, err)
			}
			out.Bash = bash
			continue
		}
		d, err := unmarshalDirective(v)
		if err != nil {
			return fmt.Errorf("permission rule %q: %w", k, err)
		}
		out.Tools[k] = d
	}
	*r = out
	return nil
}

// unmarshalDirective decodes one JSON string into a validated Directive.
func unmarshalDirective(data json.RawMessage) (Directive, error) {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return "", fmt.Errorf("want a directive string: %w", err)
	}
	return ParseDirective(s)
}

// unmarshalBashRules decodes the "bash" value in either accepted form.
func unmarshalBashRules(data json.RawMessage) ([]BashRule, error) {
	var arr []BashRule
	if err := json.Unmarshal(data, &arr); err == nil {
		for _, b := range arr {
			if _, err := ParseDirective(string(b.Directive)); err != nil {
				return nil, err
			}
		}
		return arr, nil
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("want an object of patterns to directives or an array: %w", err)
	}
	out := make([]BashRule, 0, len(m))
	for _, p := range slices.Sorted(maps.Keys(m)) {
		d, err := ParseDirective(m[p])
		if err != nil {
			return nil, fmt.Errorf("pattern %q: %w", p, err)
		}
		out = append(out, BashRule{Pattern: p, Directive: d})
	}
	return out, nil
}
