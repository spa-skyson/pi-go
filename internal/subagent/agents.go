package subagent

import (
	"bufio"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dimetron/pi-go/internal/permission"
)

// minAgentTimeoutMs is the smallest frontmatter `timeout:` treated as a real
// value. Below this the author almost certainly meant seconds; honoring it
// would kill the agent before it could produce anything.
const minAgentTimeoutMs = 1000

// AgentScope defines the scope for agent discovery.
type AgentScope string

const (
	ScopeBoth    AgentScope = "both"    // Bundled + user/project
	ScopeBundled AgentScope = "bundled" // Only embedded agents
	ScopeProject AgentScope = "project" // Only user/project agents
)

// AgentConfig represents a parsed agent definition from markdown.
type AgentConfig struct {
	Name        string // Agent identifier (e.g., "explore", "plan")
	Description string // One-line description from frontmatter
	Role        string // Config role name for model resolution (e.g., "smol", "plan", "slow")
	// Model names the model this agent runs on, set via frontmatter `model:`.
	// It overrides `role:` and may carry a provider prefix — a built-in one
	// ("openai/gpt-5.6") or a declared provider's name from the config.json
	// `providers` section ("corp-codex/gpt-5.6-sol"). Empty means `role:`
	// decides.
	Model       string
	Worktree    bool     // Whether this agent runs in an isolated git worktree
	Timeout     int      // Absolute timeout in milliseconds (0 = use default)
	Instruction string   // System prompt (markdown body)
	Tools       []string // Allowed tool names (empty = all tools)
	// LSP selects this agent's language-server surface: "off", "min" or
	// "full". Empty means inherit the child process default (min). Set it in
	// frontmatter on agents that navigate code, so the wide LSP surface is
	// bought per-agent instead of by every session.
	LSP string
	// Temperature is the sampling temperature passed to the child as
	// --temperature. Zero means unset: frontmatter temperatures are
	// practically always > 0 (0.2-1.0), so 0 is safe as the "not asked for"
	// sentinel — an agent that genuinely wants 0 sampling temperature is
	// rare enough to lose the tie against keeping the plain float field.
	Temperature float64
	// ReasoningEffort is the raw frontmatter `reasoningEffort:` value
	// (trimmed, lowercased). It is normalized to a thinking level at spawn
	// time by normalizeReasoningEffort; empty means inherit.
	ReasoningEffort string
	// Steps caps the child's tool-call iterations (0 = no limit).
	Steps int
	// Permission holds the agent's frontmatter `permission:` rules. They gate
	// the child's tool calls; the orchestrator serializes them into the
	// child's environment and the child layers them over the global config
	// rules. Zero value = no agent-specific rules.
	Permission permission.Rules
	Source     string // "bundled", "user", or "project"
}

// AgentDiscoveryResult contains all discovered agents.
type AgentDiscoveryResult struct {
	Bundled []AgentConfig
	User    []AgentConfig
	Project []AgentConfig
	All     []AgentConfig // Merged with priority: project > user > bundled
}

// ParseAgentFile parses a single agent markdown file.
// Expected format:
// ---
// name: agent-name
// description: One-line description
// role: smol
// model: corp-codex/gpt-5.6-sol
// worktree: false
// tools: read, write, edit
// temperature: 0.3
// reasoningEffort: high
// steps: 150
// ---
// Markdown instruction body...
//
// `model:` overrides `role:` when both are set; it may name a built-in
// provider's model or a declared one ("provider/model", see the `providers`
// section of config.json). `temperature:`, `reasoningEffort:` and `steps:`
// tune the child's sampling and iteration budget; unusable values warn and
// leave the field at its zero value (= inherit / unlimited).
func ParseAgentFile(path string) (AgentConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return AgentConfig{}, err
	}
	return parseAgentContent(string(data), path)
}

// ParseAgentFileFromFS parses an agent file from an embedded filesystem.
func ParseAgentFileFromFS(fsys fs.FS, path string) (AgentConfig, error) {
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		return AgentConfig{}, err
	}
	return parseAgentContent(string(data), path)
}

// parseAgentContent is the shared parsing logic for agent markdown content.
func parseAgentContent(content, path string) (AgentConfig, error) {
	// Derive default name from filename: explore.md → explore
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))

	cfg := AgentConfig{Name: name}

	scanner := bufio.NewScanner(strings.NewReader(content))
	inFrontmatter := false
	frontmatterDone := false
	var body strings.Builder
	var perm permissionParser

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if trimmed == "---" && !frontmatterDone {
			if !inFrontmatter {
				inFrontmatter = true
				continue
			}
			// End of frontmatter.
			inFrontmatter = false
			frontmatterDone = true
			continue
		}

		if inFrontmatter {
			// Inside an open `permission:` block every indented line belongs
			// to it; the block ends at the first non-indented line, which is
			// then processed as a regular frontmatter key.
			if perm.open() && perm.feed(cfg.Name, line) {
				continue
			}
			if key, value, ok := parseAgentFrontmatterLine(line); ok {
				if key == "permission" && value == "" {
					perm.begin()
					continue
				}
				applyAgentFrontmatterKey(&cfg, key, value)
			}
		} else {
			body.WriteString(line)
			body.WriteString("\n")
		}
	}

	if err := scanner.Err(); err != nil {
		return AgentConfig{}, err
	}

	cfg.Instruction = strings.TrimSpace(body.String())
	cfg.Permission = perm.rules
	return cfg, nil
}

// applyAgentFrontmatterKey sets the field of cfg named by a frontmatter key.
// Unknown keys are ignored.
func applyAgentFrontmatterKey(cfg *AgentConfig, key, value string) {
	switch key {
	case "name":
		cfg.Name = value
	case "description":
		cfg.Description = value
	case "role":
		cfg.Role = value
	case "model":
		cfg.Model = strings.TrimSpace(value)
	case "worktree":
		cfg.Worktree = strings.ToLower(value) == "true"
	case "timeout":
		if ms, ok := parseAgentTimeout(cfg.Name, value); ok {
			cfg.Timeout = ms
		}
	case "lsp":
		cfg.LSP = strings.ToLower(strings.TrimSpace(value))
	case "tools":
		cfg.Tools = append(cfg.Tools, parseAgentToolList(value)...)
	case "temperature":
		if t, ok := parseAgentTemperature(cfg.Name, value); ok {
			cfg.Temperature = t
		}
	case "reasoningEffort":
		cfg.ReasoningEffort = strings.ToLower(strings.TrimSpace(value))
	case "steps":
		if n, ok := parseAgentSteps(cfg.Name, value); ok {
			cfg.Steps = n
		}
	case "permission":
		// The block form is consumed by parseAgentContent before this switch
		// sees the key; reaching it here means a scalar value like
		// `permission: deny`, which is not a rule set.
		if value != "" {
			slog.Warn("subagent: permission must be a block of rules; scalar value ignored",
				"agent", cfg.Name, "value", value)
		}
	}
}

// permissionParser accumulates rules from an indented `permission:` block in
// agent frontmatter. It understands exactly the two levels the opencode
// permission format uses — tool-name rules and a nested `bash:` block of
// command-pattern rules — not YAML in general:
//
//	permission:
//	  edit: deny
//	  serena*: deny
//	  bash:
//	    "*": ask
//	    "git *": allow
type permissionParser struct {
	rules      permission.Rules
	begun      bool // saw the opening `permission:` line
	inBash     bool // inside the nested bash: block
	bashIndent int  // indent of the `bash:` line itself
}

// open reports whether a `permission:` block is being read.
func (p *permissionParser) open() bool { return p.begun }

// begin marks the parser as inside a `permission:` block.
func (p *permissionParser) begin() {
	p.begun = true
	p.inBash = false
}

// feed consumes one line while the block is open, reporting whether the line
// belonged to it. Blank lines stay inside; a non-indented line closes the
// block and is returned to the regular frontmatter keys.
func (p *permissionParser) feed(agentName, line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return true
	}
	indent := len(line) - len(strings.TrimLeft(line, " \t"))
	if indent == 0 {
		return false
	}
	// A line deeper than the `bash:` key itself is a command pattern.
	if p.inBash && indent > p.bashIndent {
		p.addBash(agentName, trimmed)
		return true
	}
	p.inBash = false

	key, value, ok := parseAgentFrontmatterLine(trimmed)
	if !ok {
		slog.Warn("subagent: unusable permission line ignored", "agent", agentName, "line", trimmed)
		return true
	}
	key = unquoteFrontmatter(key)
	value = unquoteFrontmatter(value)
	if key == "bash" {
		if value == "" {
			p.inBash = true
			p.bashIndent = indent
			return true
		}
		// A scalar `bash: deny` gates the tool wholesale — the command
		// patterns are the block form above.
		p.addTool(agentName, "bash", value)
		return true
	}
	p.addTool(agentName, key, value)
	return true
}

// addTool records one tool-name rule; an unusable directive warns and is
// skipped, matching how the other frontmatter values behave.
func (p *permissionParser) addTool(agentName, pattern, value string) {
	d, err := permission.ParseDirective(value)
	if err != nil {
		slog.Warn("subagent: permission rule ignored", "agent", agentName, "rule", pattern, "error", err)
		return
	}
	if p.rules.Tools == nil {
		p.rules.Tools = make(map[string]permission.Directive)
	}
	p.rules.Tools[pattern] = d
}

// addBash records one command-pattern rule from inside the bash: block.
func (p *permissionParser) addBash(agentName, trimmed string) {
	pattern, value, ok := parseAgentFrontmatterLine(trimmed)
	if !ok {
		slog.Warn("subagent: unusable bash permission line ignored", "agent", agentName, "line", trimmed)
		return
	}
	d, err := permission.ParseDirective(value)
	if err != nil {
		slog.Warn("subagent: permission rule ignored", "agent", agentName, "rule", pattern, "error", err)
		return
	}
	p.rules.Bash = append(p.rules.Bash, permission.BashRule{
		Pattern:   unquoteFrontmatter(pattern),
		Directive: d,
	})
}

// unquoteFrontmatter strips one pair of matching surrounding quotes, so both
// `"git *": allow` and `git *: allow` parse to the same pattern.
func unquoteFrontmatter(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && (s[0] == '"' && s[len(s)-1] == '"' || s[0] == '\'' && s[len(s)-1] == '\'') {
		return s[1 : len(s)-1]
	}
	return s
}

// parseAgentTimeout reads a frontmatter `timeout:` value, reporting ok=false
// when it is unusable and the default should stand instead.
//
// The unit is milliseconds, which reads as seconds at a glance — a bundled
// agent shipped `timeout: 30` and was SIGKILLed 30ms in, every time, unable to
// emit a single token. Anything under a second cannot be deliberate, so treat
// it as the unit mistake it is rather than honoring a value that guarantees the
// agent never runs.
func parseAgentTimeout(agentName, value string) (int, bool) {
	ms, err := strconv.Atoi(value)
	if err != nil || ms <= 0 {
		return 0, false
	}
	if ms < minAgentTimeoutMs {
		slog.Warn("subagent: implausibly small timeout ignored; the unit is milliseconds",
			"agent", agentName, "timeout_ms", ms, "using", "default")
		return 0, false
	}
	return ms, true
}

// parseAgentTemperature reads a frontmatter `temperature:` value, reporting
// ok=false when it is unusable and the field should stay zero (= unset, child
// inherits). Sampling temperatures are 0..2 by every provider's contract, so a
// negative value is the same unit-class mistake as a non-numeric one.
func parseAgentTemperature(agentName, value string) (float64, bool) {
	t, err := strconv.ParseFloat(value, 64)
	if err != nil || t < 0 {
		slog.Warn("subagent: unusable temperature ignored",
			"agent", agentName, "temperature", value, "using", "default")
		return 0, false
	}
	return t, true
}

// parseAgentSteps reads a frontmatter `steps:` value — the cap on the child's
// tool-call iterations — reporting ok=false when it is unusable and the field
// should stay zero (= no limit).
func parseAgentSteps(agentName, value string) (int, bool) {
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 {
		slog.Warn("subagent: unusable steps ignored",
			"agent", agentName, "steps", value, "using", "unlimited")
		return 0, false
	}
	return n, true
}

// normalizeReasoningEffort maps a raw `reasoningEffort:` value onto the
// thinking levels the provider layer understands. "minimal" is OpenAI's
// spelling of "none"; everything else passes through lowercased. Unknown
// values warn and come back empty so the child inherits instead of failing.
func normalizeReasoningEffort(value string) string {
	switch v := strings.ToLower(strings.TrimSpace(value)); v {
	case "":
		return ""
	case "minimal":
		return "none"
	case "none", "low", "medium", "high", "max":
		return v
	default:
		slog.Warn("subagent: unknown reasoningEffort ignored",
			"reasoningEffort", value, "known", "none, minimal, low, medium, high, max")
		return ""
	}
}

// parseAgentToolList splits a comma-separated frontmatter `tools:` value,
// dropping empty entries.
func parseAgentToolList(value string) []string {
	var tools []string
	for _, t := range strings.Split(value, ",") {
		if t = strings.TrimSpace(t); t != "" {
			tools = append(tools, t)
		}
	}
	return tools
}

// parseAgentFrontmatterLine parses "key: value" from a frontmatter line.
func parseAgentFrontmatterLine(line string) (key, value string, ok bool) {
	parts := strings.SplitN(line, ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), true
}

// LoadAgentsFromDir loads all agent markdown files from a directory.
// Returns empty slice if directory doesn't exist (not an error).
func LoadAgentsFromDir(dir string) ([]AgentConfig, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading agents dir %s: %w", dir, err)
	}

	var agents []AgentConfig
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		agent, err := ParseAgentFile(path)
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", path, err)
		}
		agents = append(agents, agent)
	}
	return agents, nil
}

// findNearestProjectAgentsDir walks up from cwd looking for .pi-go/agents/.
func findNearestProjectAgentsDir(cwd string) (string, error) {
	dir := cwd
	for {
		agentsDir := filepath.Join(dir, ".pi-go", "agents")
		if info, err := os.Stat(agentsDir); err == nil && info.IsDir() {
			return agentsDir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break // Reached root
		}
		dir = parent
	}
	return "", os.ErrNotExist
}

// loadAgentsWithSource loads a directory of agent files and stamps each one
// with the source it came from ("user" or "project").
func loadAgentsWithSource(dir, source string) ([]AgentConfig, error) {
	agents, err := LoadAgentsFromDir(dir)
	if err != nil {
		return nil, err
	}
	for i := range agents {
		agents[i].Source = source
	}
	return agents, nil
}

// mergeAgentsByName appends agents to all, replacing in place any entry already
// present under the same name. seen maps agent name → index in all and is
// updated as entries are appended, so successive calls override earlier ones.
func mergeAgentsByName(all []AgentConfig, seen map[string]int, agents []AgentConfig) []AgentConfig {
	for _, agent := range agents {
		if idx, ok := seen[agent.Name]; ok {
			all[idx] = agent
			continue
		}
		seen[agent.Name] = len(all)
		all = append(all, agent)
	}
	return all
}

// DiscoverAgents loads agents from bundled, user, and project directories.
// Priority: project > user > bundled (later sources override earlier ones by name).
func DiscoverAgents(cwd string, scope AgentScope) (*AgentDiscoveryResult, error) {
	result := &AgentDiscoveryResult{}

	// Load bundled agents
	bundledAgents, err := LoadBundledAgents()
	if err != nil {
		return nil, fmt.Errorf("loading bundled agents: %w", err)
	}
	result.Bundled = bundledAgents

	// Load user agents (~/.pi-go/agents/)
	if homeDir, homeErr := os.UserHomeDir(); homeErr == nil {
		userAgents, err := loadAgentsWithSource(filepath.Join(homeDir, ".pi-go", "agents"), "user")
		if err != nil {
			return nil, fmt.Errorf("loading user agents: %w", err)
		}
		result.User = userAgents
	}

	// Load project agents (.pi-go/agents/ in nearest ancestor)
	if projectDir, findErr := findNearestProjectAgentsDir(cwd); findErr == nil {
		projectAgents, err := loadAgentsWithSource(projectDir, "project")
		if err != nil {
			return nil, fmt.Errorf("loading project agents: %w", err)
		}
		result.Project = projectAgents
	}

	// Merge all agents with priority: project > user > bundled — each pass
	// overrides same-named entries left by the passes before it.
	seen := make(map[string]int) // name → index in All
	result.All = mergeAgentsByName(result.All, seen, result.Bundled)
	result.All = mergeAgentsByName(result.All, seen, result.User)
	result.All = mergeAgentsByName(result.All, seen, result.Project)

	// Filter based on scope
	switch scope {
	case ScopeBundled:
		result.All = result.Bundled
	case ScopeProject:
		result.All = append(result.Project, result.User...)
	}

	return result, nil
}

// FindAgent looks up an agent by name from a discovery result.
func FindAgent(result *AgentDiscoveryResult, name string) (AgentConfig, bool) {
	for _, agent := range result.All {
		if agent.Name == name {
			return agent, true
		}
	}
	return AgentConfig{}, false
}
