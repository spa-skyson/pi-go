package permission

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestGlobMatch(t *testing.T) {
	tests := []struct {
		pattern string
		s       string
		want    bool
	}{
		{"serena*", "serena", true},      // star matches empty tail
		{"serena*", "serena_find", true}, // prefix
		{"serena*", "serpentine", false}, // prefix must be literal
		{"*", "", true},                  // star matches nothing too
		{"*", "anything", true},
		{"edit", "edit", true},
		{"edit", "edit_file", false}, // no implicit substring
		{"read*write", "read_write", true},
		{"read*write", "readwritex", false}, // anchored at the end
		{"x*read", "prefixread", false},     // anchored at the start
		{"git.status", "gitXstatus", false}, // dot is literal, not regex any-char
		{"a+b", "aab", false},               // regex metachars are quoted
		{"web_search", "web_search", true},
		// Case-sensitive: tool names are lowercase; a mixed-case pattern only
		// matches itself.
		{"Serena*", "serena_find", false},
		{"serena*", "Serena_find", false},
	}
	for _, tt := range tests {
		if got := globMatch(tt.pattern, tt.s); got != tt.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", tt.pattern, tt.s, got, tt.want)
		}
	}
}

// The user's real shape: spaces and && inside bash patterns, literals around
// the stars.
func TestGlobMatch_BashCommandPatterns(t *testing.T) {
	tests := []struct {
		pattern string
		cmd     string
		want    bool
	}{
		{"git *", "git status", true},
		{"git *", "git status --short", true},
		{"git *", "sudo git status", false},
		{"git *", "gitx status", false},
		{"cd * && git *", "cd /repo && git status", true},
		{"cd * && git *", "cd /repo &&git status", false},
		{"cd * && ast-index *", "cd /x && ast-index usages foo", true},
		{"*", "", true}, // empty command still gated by the catch-all
		{"*", "rm -rf /", true},
		{"git *", "", false},
	}
	for _, tt := range tests {
		if got := globMatch(tt.pattern, tt.cmd); got != tt.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", tt.pattern, tt.cmd, got, tt.want)
		}
	}
}

func TestCheck_ToolNames(t *testing.T) {
	rules := Rules{Tools: map[string]Directive{
		"*":       Ask,
		"serena*": Deny,
		"edit":    Deny,
		"read":    Allow,
	}}

	tests := []struct {
		tool     string
		want     Directive
		wantRule string
	}{
		{"read", Allow, "read"},
		{"edit", Deny, "edit"}, // exact beats the ask catch-all
		{"serena_find", Deny, "serena*"},
		{"web_search", Ask, "*"}, // only the catch-all matches
		{"unknown_tool", Ask, "*"},
	}
	for _, tt := range tests {
		got := Check(rules, tt.tool, nil)
		if got.Directive != tt.want || got.Rule != tt.wantRule {
			t.Errorf("Check(%q) = {%v %q}, want {%v %q}", tt.tool, got.Directive, got.Rule, tt.want, tt.wantRule)
		}
	}

	// No rule at all allows.
	if got := Check(Rules{}, "anything", nil); got.Directive != Allow {
		t.Errorf("empty rules: got %v, want allow", got.Directive)
	}
}

func TestCheck_TieBreaksDeterministically(t *testing.T) {
	// Same specificity, different directives: one deterministic winner,
	// whichever way the map iterates.
	rules := Rules{Tools: map[string]Directive{"aa": Deny, "bb": Allow}}
	for range 50 {
		if got := Check(rules, "aa", nil); got.Directive != Deny {
			t.Fatalf("tie between \"aa\" and \"bb\": got %v for \"aa\", want deny", got.Directive)
		}
	}
}

func TestCheck_BashCommandRules(t *testing.T) {
	rules := Rules{
		Tools: map[string]Directive{
			"bash": Deny, // bash itself is denied wholesale...
		},
		Bash: []BashRule{
			{Pattern: "*", Directive: Ask},
			{Pattern: "git *", Directive: Allow},
			{Pattern: "ast-index *", Directive: Allow},
			{Pattern: "cd * && git *", Directive: Allow},
			{Pattern: "cd * && ast-index *", Directive: Allow},
		},
	}

	tests := []struct {
		name     string
		args     map[string]any
		want     Directive
		wantRule string
	}{
		{"allowed by command pattern", map[string]any{"command": "git status"}, Allow, "git *"},
		{"compound allowed", map[string]any{"command": "cd /repo && git status --short"}, Allow, "cd * && git *"},
		{"catch-all asks", map[string]any{"command": "rm -rf build"}, Ask, "*"},
		{"catch-all also gates an empty command", map[string]any{"command": ""}, Ask, "*"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Check(rules, "bash", tt.args)
			if got.Directive != tt.want || got.Rule != tt.wantRule {
				t.Fatalf("got {%v %q}, want {%v %q}", got.Directive, got.Rule, tt.want, tt.wantRule)
			}
		})
	}

	// With no catch-all, an unmatched command falls through to the Tools
	// verdict for the bash tool itself.
	noCatchAll := Rules{
		Tools: map[string]Directive{"bash": Deny},
		Bash:  []BashRule{{Pattern: "git *", Directive: Allow}},
	}
	if got := Check(noCatchAll, "bash", map[string]any{"command": "rm -rf build"}); got.Directive != Deny {
		t.Errorf("fall-through to tools verdict: got %v, want deny", got.Directive)
	}

	// The command rules must not leak onto other tools: a tool literally
	// named "git status" is judged by its name.
	if got := Check(rules, "git status", nil); got.Directive != Allow {
		t.Errorf("bash rules applied to non-bash tool: got %v, want allow", got.Directive)
	}
}

func TestCheck_BashPatternsBeatToolsVerdict(t *testing.T) {
	// The pattern allow outranks the wholesale bash deny.
	rules := Rules{
		Tools: map[string]Directive{"bash": Deny},
		Bash:  []BashRule{{Pattern: "git *", Directive: Allow}},
	}
	if got := Check(rules, "bash", map[string]any{"command": "git log"}); got.Directive != Allow {
		t.Errorf("pattern allow lost to tools deny: %v", got.Directive)
	}
}

func TestCheck_BashSpecificityAndOrder(t *testing.T) {
	// Longer literal wins over the shorter one, regardless of order.
	rules := Rules{Bash: []BashRule{
		{Pattern: "git *", Directive: Allow},
		{Pattern: "git push*", Directive: Deny},
	}}
	if got := Check(rules, "bash", map[string]any{"command": "git push --force"}); got.Directive != Deny {
		t.Errorf("specific allow lost: %v", got.Directive)
	}
	if got := Check(rules, "bash", map[string]any{"command": "git log"}); got.Directive != Allow {
		t.Errorf("generic allow lost: %v", got.Directive)
	}

	// Equal specificity: the later-declared rule wins.
	tie := Rules{Bash: []BashRule{
		{Pattern: "git status", Directive: Deny},
		{Pattern: "git status", Directive: Allow},
	}}
	if got := Check(tie, "bash", map[string]any{"command": "git status"}); got.Directive != Allow {
		t.Errorf("tie did not resolve to the later rule: %v", got.Directive)
	}
}

func TestCheck_BashMissingArgs(t *testing.T) {
	rules := Rules{Bash: []BashRule{{Pattern: "*", Directive: Deny}}}
	// A call with no command is still gated by the catch-all, which matches
	// the empty string.
	for _, args := range []map[string]any{nil, {}, {"command": 42}} {
		if got := Check(rules, "bash", args); got.Directive != Deny {
			t.Errorf("args %v: got %v, want deny", args, got.Directive)
		}
	}
}

func TestMerge(t *testing.T) {
	base := Rules{
		Tools: map[string]Directive{"edit": Deny, "read": Allow, "*": Ask},
		Bash:  []BashRule{{Pattern: "git *", Directive: Allow}},
	}
	override := Rules{
		Tools: map[string]Directive{"edit": Allow},
		Bash:  []BashRule{{Pattern: "rm *", Directive: Deny}},
	}

	got := Merge(base, override)

	if len(got.Tools) != 3 {
		t.Fatalf("merged Tools = %v, want 3 keys", got.Tools)
	}
	if got.Tools["edit"] != Allow {
		t.Errorf("agent rule did not override global: edit = %v", got.Tools["edit"])
	}
	if got.Tools["read"] != Allow || got.Tools["*"] != Ask {
		t.Errorf("global rules lost: %v", got.Tools)
	}
	wantBash := []BashRule{
		{Pattern: "git *", Directive: Allow},
		{Pattern: "rm *", Directive: Deny},
	}
	if !reflect.DeepEqual(got.Bash, wantBash) {
		t.Errorf("merged Bash = %v, want %v", got.Bash, wantBash)
	}

	// Merge must not mutate its inputs.
	if _, changed := base.Tools["edit"]; base.Tools["edit"] != Deny || changed == false {
		t.Errorf("Merge mutated base: %v", base.Tools)
	}

	// Empty sides pass through.
	if got := Merge(Rules{}, override); !reflect.DeepEqual(got.Tools, override.Tools) {
		t.Errorf("Merge(empty, override) Tools = %v, want %v", got.Tools, override.Tools)
	}
}

func TestRules_JSONRoundTrip(t *testing.T) {
	want := Rules{
		Tools: map[string]Directive{"edit": Deny, "serena*": Deny, "question": Allow, "external_directory": Allow},
		Bash: []BashRule{
			{Pattern: "*", Directive: Ask},
			{Pattern: "git *", Directive: Allow},
			{Pattern: "cd * && git *", Directive: Allow},
		},
	}

	blob, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got Rules
	if err := json.Unmarshal(blob, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip = %+v, want %+v", got, want)
	}
}

// The config.json spelling a user actually writes: bash as a pattern map.
func TestRules_UnmarshalObjectBash(t *testing.T) {
	var got Rules
	blob := []byte(`{"edit":"deny","bash":{"git *":"allow","*":"ask"}}`)
	if err := json.Unmarshal(blob, &got); err != nil {
		t.Fatal(err)
	}
	if got.Tools["edit"] != Deny {
		t.Errorf("Tools = %v, want edit:deny", got.Tools)
	}
	wantBash := []BashRule{
		{Pattern: "*", Directive: Ask},
		{Pattern: "git *", Directive: Allow},
	}
	if !reflect.DeepEqual(got.Bash, wantBash) {
		t.Errorf("Bash = %v, want %v", got.Bash, wantBash)
	}
}

func TestRules_UnmarshalErrors(t *testing.T) {
	tests := []struct {
		name string
		blob string
	}{
		{"unknown directive", `{"edit":"denny"}`},
		{"non-string directive", `{"edit":17}`},
		{"unknown bash directive", `{"bash":{"git *":"maybe"}}`},
		{"bash of wrong shape", `{"bash":true}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var r Rules
			if err := json.Unmarshal([]byte(tt.blob), &r); err == nil {
				t.Errorf("Unmarshal(%s) succeeded, want error", tt.blob)
			}
		})
	}
}

func TestDecision_Error(t *testing.T) {
	deny := Decision{Directive: Deny, Rule: "serena*"}
	if got, want := deny.Error("serena_find").Error(),
		`permission denied by rule "serena*" for tool serena_find`; got != want {
		t.Errorf("deny text = %q, want %q", got, want)
	}

	denyBash := Decision{Directive: Deny, Rule: "*", Command: "rm -rf build"}
	if got, want := denyBash.Error("bash").Error(),
		`permission denied by rule "*" for tool bash (command: "rm -rf build")`; got != want {
		t.Errorf("deny bash text = %q, want %q", got, want)
	}

	ask := Decision{Directive: Ask, Rule: "*"}
	if got, want := ask.Error("web_search").Error(),
		"tool web_search requires interactive approval; denied in a non-interactive session"; got != want {
		t.Errorf("ask text = %q, want %q", got, want)
	}

	if (Decision{Directive: Allow}).Error("read") != nil {
		t.Error("allow must not produce an error")
	}
}

func TestParseDirective(t *testing.T) {
	for _, s := range []string{"allow", "deny", "ask", "ALLOW", "  Deny  "} {
		if _, err := ParseDirective(s); err != nil {
			t.Errorf("ParseDirective(%q) = %v, want nil", s, err)
		}
	}
	if _, err := ParseDirective("maybe"); err == nil {
		t.Error("ParseDirective(\"maybe\") succeeded, want error")
	}
}

func TestParseDirectiveErrors_NamesTheRule(t *testing.T) {
	var r Rules
	err := json.Unmarshal([]byte(`{"serena*":"denny"}`), &r)
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), `permission rule "serena*"`) {
		t.Errorf("error %q does not name the rule", err)
	}
}

func TestFromEnv(t *testing.T) {
	t.Setenv(EnvVar, `{"edit":"deny","bash":[{"pattern":"git *","directive":"allow"}]}`)
	got, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if got.Tools["edit"] != Deny || len(got.Bash) != 1 {
		t.Errorf("FromEnv = %+v", got)
	}

	t.Setenv(EnvVar, "")
	if got, err := FromEnv(); err != nil || !got.Empty() {
		t.Errorf("unset env: got %+v, %v; want zero rules, nil", got, err)
	}

	t.Setenv(EnvVar, "{not json")
	if _, err := FromEnv(); err == nil {
		t.Error("corrupt payload: want error")
	}
}

func TestEmpty(t *testing.T) {
	if !(Rules{}).Empty() {
		t.Error("zero value must be empty")
	}
	if (Rules{Tools: map[string]Directive{"read": Allow}}).Empty() {
		t.Error("tool rules are not empty")
	}
	if (Rules{Bash: []BashRule{{Pattern: "*", Directive: Ask}}}).Empty() {
		t.Error("bash rules are not empty")
	}
}

func TestApplyOverride(t *testing.T) {
	rules := Rules{
		Tools: map[string]Directive{
			"edit":  Ask,
			"read":  Allow,
			"bash":  Deny,
			"grep*": Ask,
		},
		Bash: []BashRule{
			{Pattern: "git *", Directive: Ask},
			{Pattern: "rm *", Directive: Deny},
			{Pattern: "git *", Directive: Ask}, // duplicate pattern: both must flip
		},
	}

	got := ApplyOverride(rules, "edit", Allow)
	if got.Tools["edit"] != Allow {
		t.Errorf("Tools[edit] = %q, want allow", got.Tools["edit"])
	}
	if got.Tools["read"] != Allow || got.Tools["bash"] != Deny || got.Tools["grep*"] != Ask {
		t.Errorf("untouched keys drifted: %+v", got.Tools)
	}
	if len(got.Bash) != 3 || got.Bash[0].Directive != Ask || got.Bash[1].Directive != Deny {
		t.Errorf("bash rules drifted: %+v", got.Bash)
	}

	// Every bash rule with the pattern flips, order preserved.
	got = ApplyOverride(rules, "git *", Allow)
	if got.Bash[0].Directive != Allow || got.Bash[2].Directive != Allow {
		t.Errorf("bash override missed duplicate pattern: %+v", got.Bash)
	}
	if got.Bash[1].Pattern != "rm *" || got.Bash[1].Directive != Deny {
		t.Errorf("neighbouring bash rule drifted: %+v", got.Bash[1])
	}
	if rules.Bash[0].Directive != Ask {
		t.Errorf("input mutated in place: %+v", rules.Bash[0])
	}
	if rules.Tools["edit"] != Ask {
		t.Errorf("input map mutated in place: %+v", rules.Tools)
	}

	// A key matching nothing changes nothing.
	unchanged := ApplyOverride(rules, "no-such-rule", Deny)
	if !reflect.DeepEqual(unchanged, rules) {
		t.Errorf("unknown key changed rules: %+v", unchanged)
	}
}
