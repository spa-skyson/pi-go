# Specs — Feature Design Documents

This directory contains Plan-Driven Design (PDD) specs for pi-rate features.
Each spec follows a phased workflow from rough idea through implementation.

## Organization

```
specs/
├── eval-orchestrator/      # Standalone evaluation orchestrator module
├── evaluations/            # Benchmark/evaluation specs
├── features/               # Feature implementations by component
│   ├── COD/             # Coding, testing & audit
│   ├── LLM/             # Language model research
│   ├── MEM/             # Memory system
│   ├── SOP/             # Standard Operating Procedures
│   ├── SUB/             # Subagents
│   ├── TOO/             # Tool specifications
│   ├── TST/             # Testing & audit
│   ├── TUI/             # Terminal UI
│   └── WEB/             # Web server
├── graph-agents/           # Graph-based agent execution
├── harness/                # Comparative harness reviews
├── issues/                 # Issue tracking/fixes
├── kagent/                 # kagent deployment manifests
├── memory/                 # Session memory specs
├── memory-fixes/           # Memory subsystem repair
├── research/               # Architecture improvements
├── run-coo-worker/         # Co-worker run concurrency design
└── sessio-errors/          # Session error analysis
```

## Phases

| Phase           | File                         | Description                                   |
|-----------------|------------------------------|-----------------------------------------------|
| 1. Idea         | `rough-idea.md`              | Initial concept, motivation, and source       |
| 2. Requirements | `requirements.md`            | Acceptance criteria and constraints           |
| 3. Research     | `research/`                  | Codebase exploration, gap analysis, prior art |
| 4. Design       | `design.md`                  | Architecture, data flow, interfaces           |
| 5. Plan         | `plan.md`                    | Step-by-step implementation tasks             |
| 6. Prompt       | `PROMPT.md`                  | Agent prompt to execute the plan              |
| 7. Summary      | `summary.md` or `SUMMARY.md` | Artifacts created, outcome, lessons learned   |

## Spec Index

### features/COD (Coding & Improvements)

| Spec                                                  | Phases        | Description                                  |
|-------------------------------------------------------|---------------|----------------------------------------------|
| 000-improve-test-coverage/                            | idea..summary | Test coverage improvements                   |
| 001-code-review-codex/                                | prompt        | Code review using OpenAI Codex CLI           |
| 002-research-coding-agents-session-log-optimizations/ | idea..summary | Session log error patterns and optimizations |

### features/LLM (Language Model Research)

| Spec                            | Phases   | Description                                       |
|---------------------------------|----------|---------------------------------------------------|
| 000-openai-sdk/                 | research | OpenAI SDK vs responses/completions API research  |
| 001-claude-models/              | research | Claude model catalogue and advisor tool reference |
| 002-ollama-sampling-parameters/ | research | Ollama sampling parameters reference              |

### features/MEM (Memory)

| Spec        | Phases        | Description                                |
|-------------|---------------|--------------------------------------------|
| claude-mem/ | idea..summary | Native claude-mem implementation in pi-rate |

### features/SOP (Standard Operating Procedures)

| Spec                   | Phases         | Description                                       |
|------------------------|----------------|---------------------------------------------------|
| enhance-from-oh-my-pi/ | idea..summary  | Enhance pi-go with oh-my-pi features              |
| plan-command-sop/      | idea..summary  | `/plan` and `/run` commands with PDD SOP workflow |
| plan-resume/           | design..prompt | Resume interrupted plan execution                 |

### features/SUB (Subagents)

| Spec                      | Phases        | Description                  |
|---------------------------|---------------|------------------------------|
| skills-subagents/         | idea..summary | Skills-based subagent system |
| subagent-execution-modes/ | prompt        | Subagent execution modes     |

### features/TOO (Tool Specifications)

| Spec                                                 | Phases              | Description                                                                                  |
|------------------------------------------------------|---------------------|----------------------------------------------------------------------------------------------|
| 000-mcp-support/                                     | prompt              | MCP (Model Context Protocol) support                                                         |
| 001-a2a-client/                                      | idea..summary       | A2A client tool specification                                                                |
| 002-context-references/                              | idea..summary       | Context reference resolution for tools                                                       |
| 003-large-files/                                     | design              | Large file handling strategy                                                                 |
| 004-acp-subagent/                                    | idea..summary       | ACP-based subagent tool specification                                                        |
| 005-otel-fixes/                                      | idea..summary       | OpenTelemetry instrumentation fixes for tools                                                |
| 007-adk-2-0-adoption/                                | idea..summary       | Adopt ADK for Go v2.0.0                                                                      |
| 008-run-task-list-generic-ui/                        | idea..prompt        | Generic `/run` task list: right-panel progress UI for agent task lists                       |
| 010-codex-direct-mode-app-server-subagent/           | idea..prompt        | Codex direct-mode app-server subagent                                                        |
| 011-ollama-com-direct-api/                           | idea, req           | Direct ollama.com API access                                                                 |
| 012-gemini-adk-search-grounding/                     | idea..summary       | Search grounding for the Gemini ADK provider                                                 |
| 013-antigravity-acp-support-agy/                     | idea..design        | Antigravity (AGY) ACP provider support                                                       |
| 014-evals-pi-go/                                     | idea, req, research | Evaluation scenarios for the agent                                                           |
| 015-opencode-go-provider-api/                        | idea..summary       | OpenCode Go provider API integration                                                         |
| 016-rtk-phase-2-command-rewriting/                   | idea..summary       | RTK compactor phase 2: language-aware source filtering and command rewriting                 |
| 017-canopy-embedded-code-index/                      | idea..prompt        | Canopy as an embedded structural code index; replaces LSP navigation, keeps LSP diagnostics  |
| 018-goal-command/                                    | idea..summary       | `/goal` slash command: persistent per-session objective                                      |
| 020-tui-allow-open-url-in-browser-on-click/          | idea..prompt        | Open URLs in the browser on click in the TUI                                                 |
| 022-plan-mode-phase-checklist/                       | idea..prompt        | Plan-mode right-panel phase checklist showing `/plan` SOP progress                           |
| 023-plan-always-merge-worktree/                      | idea..prompt        | Always merge the planning worktree after `/plan`, even on a final-turn error                 |
| 024-mistral-provider/                                | idea..prompt        | Mistral provider support                                                                     |
| 024-tui-visual-workflow-during-plan-run-and-workflow/ | idea..design        | Visual workflow indicators in the TUI during `/plan`, `/run` and workflow SOP                |
| 025-vscode-agent-host-protocol/                      | idea..prompt        | VS Code agent-host protocol                                                                  |

### features/TST (Testing & Audit)

| Spec                                  | Phases        | Description                                       |
|---------------------------------------|---------------|---------------------------------------------------|
| atif-support/                         | idea..summary | ATIF (Agent Trajectory Interchange Format) export |
| simple-ollama-test/                   | idea..summary | E2E test with actual Ollama provider              |
| skill-commands-list-create-load-pull/ | idea, req     | Skill CRUD commands                               |
| skills-audit/                         | idea..summary | Security audit for SKILL.md files                 |

### features/TUI (Terminal UI)

| Spec                                              | Phases        | Description                                                     |
|---------------------------------------------------|---------------|-----------------------------------------------------------------|
| 001-make-main-chat-always-open-and-for-each-task/ | idea..prompt  | Main chat always open; per-task queue drains as follow-up turns |
| better-completion-commands/                       | idea..prompt  | Improved completion/autocomplete commands                       |
| login-with-openai-codex/                          | idea, req     | OAuth login for OpenAI Codex provider                           |
| nanocoder-tui/                                    | idea..summary | TUI design patterns from nanocoder                              |
| sidebar-artifacts-section/                        | idea..summary | Sidebar artifacts section                                       |

### features/WEB (Web Server)

| Spec       | Phases        | Description                 |
|------------|---------------|-----------------------------|
| web-serve/ | idea..summary | Web-based serving interface |

### eval-orchestrator (Evaluation Orchestrator)

| Spec               | Phases       | Description                                                             |
|--------------------|--------------|-------------------------------------------------------------------------|
| eval-orchestrator/ | plan, prompt | Standalone evaluation orchestrator Go module (`artifacts/`, `golden/`)  |

### evaluations/

| Spec                           | Phases        | Description                       |
|--------------------------------|---------------|-----------------------------------|
| 000-evaluation-terminal-bench/ | idea..summary | Terminal-Bench evaluation harness |

### graph-agents (Graph-Based Agents)

| Spec          | Phases        | Description                  |
|---------------|---------------|------------------------------|
| graph-agents/ | idea..summary | Graph-based agent execution  |

### harness/

| Spec      | Phases | Description                                  |
|-----------|--------|----------------------------------------------|
| deepseek/ | review | Comparative review against the DeepSeek harness |

### issues/

| Spec                           | Phases                                 | Description                                                                  |
|--------------------------------|----------------------------------------|------------------------------------------------------------------------------|
| 000-issues-fix/                | plan                                   | General issue fixes                                                          |
| 001-session-errors/            | prompt                                 | Session error analysis                                                       |
| 002-code-review-codex/         | prompt                                 | Code review with Codex                                                       |
| 003-grok-build-review/         | review                                 | Feature comparison against the grok-build agent, with roadmap recommendations |
| 004-complexity-reduction/      | plan                                   | Complexity reduction and quality improvement plan                            |
| 004-memory-dimentions/         | readme                                 | Memory dimensions research                                                   |
| 006-lie-task-verification/     | readme                                 | Missing guardrail: subagents falsely reporting task completion               |
| 007-write-tool-data-loss/      | readme                                 | `write`/`edit` truncate before writing and never fsync — data loss on quit   |
| 008-adk-utils-go-review/       | review, prompt                         | adk-utils-go feature comparison & scored recommendations                     |
| 009-tui-review-followups/      | readme                                 | Deferred follow-ups from the TUI review and the PR #134 review               |
| 010-sol-pi-efficiency-lessons/ | readme, research, design, plan, prompt | Archive-before-reduce and unblocked fixes from the SoL-Pi harness study      |
| token-cost/                    | research                               | Token-cost study: batching, cache reporting, autocompaction, subagents       |

### kagent/

| Spec    | Phases     | Description                                                   |
|---------|------------|---------------------------------------------------------------|
| kagent/ | deploy kit | kagent harness deployment: Kind manifests, Dockerfile, custom harness |

### memory/ (Session Memory)

| Spec                                                | Phases       | Description                                            |
|-----------------------------------------------------|--------------|--------------------------------------------------------|
| 001-session-memory/                                 | idea, req    | Session memory                                         |
| 002-temporal-knowledge-session-memory-prd-temporal/ | idea..prompt | PRD: temporal knowledge memory for coding agents       |

### memory-fixes/

| Spec           | Phases       | Description                                                                     |
|----------------|--------------|---------------------------------------------------------------------------------|
| memory-fixes/  | idea..prompt | Repair the memory subsystem: dead after-tool callback chain, worker lifecycle, palace paths, TUI wiring |

### research/

| Spec                                        | Phases                 | Description                                                                        |
|---------------------------------------------|------------------------|------------------------------------------------------------------------------------|
| 000-rtk-hooks-optimizer/                    | idea..summary          | RTK output compactor hooks                                                         |
| 003-improvements/                           | research               | Architecture and gap analysis docs                                                 |
| 004-summarize-harness/                      | design                 | Iterative chunked-read summarization harness                                       |
| 005-critical-improvements/                  | report                 | Critical improvements report (lint, tests, coverage, architecture)                 |
| 006-project-overview-critical-improvements/ | overview, plan, prompt | Project overview and critical improvement points                                   |
| 007-codex-context-compaction/               | research..plan         | Expose upstream Codex app-server context compaction through `internal/codex/`      |

### run-coo-worker/

| Spec            | Phases         | Description                                                       |
|-----------------|----------------|-------------------------------------------------------------------|
| run-coo-worker/ | design..prompt | Co-worker run: concurrency budget that composes under nesting     |

### sessio-errors/

| Spec      | Phases | Description                   |
|-----------|--------|-------------------------------|
| PROMPT.md | prompt | Session error analysis prompt |

*`sessio-errors` is a historical typo for `session-errors`; the directory name is kept as-is.*

## Notes

- `features/TOO/` numbering has gaps (no `006-`, `009-`, `021-`) — harmless. It also has
  **two `024-` directories** (`024-mistral-provider/`, `024-tui-visual-workflow-during-plan-run-and-workflow/`)
  — a known violation of the numbering convention; do not renumber casually, existing
  references point at the current paths.
- `sessio-errors/` is a historical typo for `session-errors/`; the directory keeps the
  misspelled name.
- `memory/` is a separate, newer category (session-memory specs); `memory-fixes/` is a
  single spec for the memory-subsystem repair and is unrelated to it.
- Loose notes at category roots (`specs/pi-extensions-gaps-202609.md`, loose `*.md` files
  under `specs/issues/`) are not spec directories and are not indexed.

## Conventions

### Directory structure

```
specs/
├── features/
│   └── SOP/
│       └── plan-command-sop/
│           rough-idea.md
│           requirements.md
│           research/
│           design.md
│           plan.md
│           PROMPT.md
│           summary.md
```

### Naming

- Use kebab-case for folder names
- Number folders within categories: `000-`, `001-`, `002-`, ...
- Keep names descriptive but concise
- Phase files use specific names: `rough-idea.md`, `requirements.md`, `design.md`, `plan.md`, `PROMPT.md`, `summary.md`
  or `SUMMARY.md`
- Regenerate/verify the Spec Index against the tree with:
  `find specs -mindepth 2 -maxdepth 3 -type d | sort` (depth 3 reaches specs inside `features/`)
