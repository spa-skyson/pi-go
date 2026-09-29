package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/dimetron/pi-go/internal/subagent"
)

// handleAgentCommand handles /agent: no args lists the switchable agents with
// the active one marked; an argument applies that agent — or "default" — to
// the main session.
func (m *model) handleAgentCommand(args []string) (tea.Model, tea.Cmd) {
	if len(args) == 0 {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: m.formatAgentList(),
		})
		return m, nil
	}
	name := strings.TrimSpace(args[0])
	if strings.EqualFold(name, "default") {
		name = ""
	}
	return m.applyAgent(name)
}

// cycleAgent advances the Shift+Tab cycle: default (the built-in pi-go
// agent) → primary agents alphabetically → back to default.
func (m *model) cycleAgent() (tea.Model, tea.Cmd) {
	return m.applyAgent(nextAgentName(m.activeAgent, primaryAgentNames(m.cfg.PrimaryAgents)))
}

// applyAgent switches the main session to the named primary agent ("" = the
// built-in default agent), shared by /agent <name> and Shift+Tab. Like
// /model, it refuses while a response is running: the agent object is being
// rebuilt underneath the loop.
func (m *model) applyAgent(name string) (tea.Model, tea.Cmd) {
	if m.running {
		return m, m.setFlash("Cannot switch agent while a response is running")
	}
	if m.cfg.Agent == nil || m.cfg.AgentSwitcher == nil {
		m.chatModel.AppendNotice("Agent switching is not available in this context.")
		return m, nil
	}

	sw, err := m.cfg.AgentSwitcher(m.ctx, name)
	if err != nil {
		m.chatModel.AppendNotice(fmt.Sprintf("Agent switch failed: %v", err))
		return m, nil
	}
	if err := m.cfg.Agent.RebuildWithSession(sw.Instruction, sw.LLM, sw.BeforeTool, sw.AfterTool); err != nil {
		m.chatModel.AppendNotice(fmt.Sprintf("Failed to rebuild agent: %v", err))
		return m, nil
	}

	// A nil LLM from the switcher means "no model change" — keep the names.
	if sw.LLM != nil {
		m.cfg.LLM = sw.LLM
		m.cfg.ModelName = sw.ModelName
		m.cfg.ProviderName = sw.Provider
	}
	m.activeAgent = name

	var b strings.Builder
	if name == "" {
		b.WriteString("Switched to the default agent.")
	} else {
		fmt.Fprintf(&b, "Switched to agent **%s**.", name)
	}
	if sw.LLM != nil {
		fmt.Fprintf(&b, " Model: **%s** (%s).", sw.ModelName, sw.Provider)
	}
	m.chatModel.AppendNotice(b.String())
	return m, nil
}

// primaryAgentNames returns the switchable agents' names, sorted for a
// stable cycle and listing.
func primaryAgentNames(agents []subagent.AgentConfig) []string {
	names := make([]string, 0, len(agents))
	for _, ac := range agents {
		names = append(names, ac.Name)
	}
	sort.Strings(names)
	return names
}

// nextAgentName returns the next stop of the Shift+Tab cycle: default ("")
// hands off to the first agent, the last agent wraps back to default. An
// agent no longer on the list restarts the cycle from its beginning.
func nextAgentName(current string, names []string) string {
	if len(names) == 0 {
		return ""
	}
	if current == "" {
		return names[0]
	}
	for i, n := range names {
		if n == current {
			if i+1 < len(names) {
				return names[i+1]
			}
			return ""
		}
	}
	return names[0]
}

// formatAgentList renders the /agent listing: every switchable agent with a
// one-line description, the active one marked.
func (m *model) formatAgentList() string {
	var b strings.Builder
	b.WriteString("**Agents** — Shift+Tab cycles, `/agent <name>` switches\n\n")

	marker := ""
	if m.activeAgent == "" {
		marker = " ←"
	}
	fmt.Fprintf(&b, "- **default**: built-in pi-go agent%s\n", marker)

	desc := make(map[string]string, len(m.cfg.PrimaryAgents))
	for _, ac := range m.cfg.PrimaryAgents {
		if ac.Description != "" {
			desc[ac.Name] = ac.Description
		}
	}
	for _, name := range primaryAgentNames(m.cfg.PrimaryAgents) {
		marker = ""
		if name == m.activeAgent {
			marker = " ←"
		}
		d := desc[name]
		if d == "" {
			d = "(no description)"
		}
		fmt.Fprintf(&b, "- **%s**: %s%s\n", name, d, marker)
	}
	if len(m.cfg.PrimaryAgents) == 0 {
		b.WriteString("\nNo primary agents found — set `mode: primary` in an agent's frontmatter under `~/.pi-go/agents/`.\n")
	}
	return b.String()
}
