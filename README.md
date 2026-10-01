# Pi-rate

[![CI](https://github.com/spa-skyson/pi-rate/actions/workflows/ci.yml/badge.svg)](https://github.com/spa-skyson/pi-rate/actions/workflows/ci.yml)
[![Go Version](https://img.shields.io/github/go-mod/go-version/spa-skyson/pi-rate)](go.mod)
[![License](https://img.shields.io/github/license/spa-skyson/pi-rate)](LICENSE)
[![Release](https://img.shields.io/github/v/release/spa-skyson/pi-rate?logo=github&label=latest)](https://github.com/spa-skyson/pi-rate/releases)
[![GitHub stars](https://img.shields.io/github/stars/spa-skyson/pi-rate?style=social)](https://github.com/spa-skyson/pi-rate)
[![GitHub issues](https://img.shields.io/github/issues/spa-skyson/pi-rate)](https://github.com/spa-skyson/pi-rate/issues)

Pi-rate is a terminal AI coding agent. You run it in your project directory, it
reads and edits code, runs shell commands and git, and turns a plain-English
task ("add tests for the parser", "fix the failing CI job") into a reviewed
change — all from the terminal, no IDE required. It works across many LLM
providers (Anthropic, OpenAI, Google, Mistral, xAI, OpenRouter, Azure, Ollama,
and any OpenAI-compatible or Anthropic endpoint you point it at), keeps
sessions resumable, and gates every destructive action behind permission
rules you control.

The name: a fork of [pi-go](https://github.com/dimetron/pi-go) named as a
piracy pun on Pi — Pi + rate = Pi-rate.

## Quick start

```bash
# Install (macOS / Linux)
curl -fsSL https://raw.githubusercontent.com/spa-skyson/pi-rate/main/scripts/install.sh | bash

# Point it at a provider
export ANTHROPIC_API_KEY="sk-ant-..."       # or OPENAI_API_KEY, GEMINI_API_KEY, …
# …or run the wizard: pirate setup

# Run it in your project
cd my-project
pirate
```

First steps inside the TUI: type a task and press Enter; `/help` lists every
command and hotkey; `/model` switches models; `@` mentions a file by path;
Shift+Tab switches the session agent; Ctrl+C twice quits. Other install
methods (Nix, `go install`, build from source, pre-built binaries) and release
verification: [docs/installation.md](docs/installation.md).

## Why this fork

Pi-rate is a fork of [dimetron/pi-go](https://github.com/dimetron/pi-go)
(inspired by [Pi (badlogic)](https://github.com/badlogic/pi-mono) and
[opencode](https://opencode.ai)); upstream commits are cherry-picked as
needed. On top of upstream it adds, in rough order of arrival:

- **Renamed identity** — the project, binary, VS Code extension and TUI became
  Pi-rate, with a pirate-flavoured mascot and animated sea.
- **Declared providers** — any OpenAI-compatible or Anthropic endpoint named in
  `config.json` (`providers` section) becomes a `name/model` prefix.
- **Per-agent frontmatter** — `model`/`role`, `tools`, sampling knobs
  (`temperature`, `reasoningEffort`, `steps`), `timeout`, `worktree`, `lsp`,
  and agent-scoped `permission` blocks.
- **Primary-agent switching** — `mode: primary`/`all` agents can run the main
  session; switch with Shift+Tab or `/agent` mid-conversation.
- **Permission subsystem** — `allow`/`deny`/`ask` rules on tool globs and bash
  patterns, with an interactive approval dialog in the TUI.
- **Todo plans** — a plan checklist the agent maintains with
  `todo_write`/`todo_read`, shown live in the sidebar.
- **Subagent monitor, steering, background runs** — Ctrl+T monitor with
  per-run cards, `s` to steer a running agent, detached runs collected via
  `agent_result`.
- **Prompt editor** — multiline input (Shift+Enter), paste summaries (Alt+V),
  `@`-file picker, queued prompts instead of dropped input.
- **Fullscreen `/diff`**, a fuzzy search popup, custom themes, Ctrl+Z suspend,
  terminal attention (bell + OSC 777).
- **JSONC config with `$schema`** autocompletion, and automatic migration from
  `~/.pi-go` to `~/.pirate`.
- **The question tool** — the model asks the user a multiple-choice question
  and waits, instead of guessing.
- **Windows CI fixes and performance work** — fork-only test stabilisation and
  hot-path optimisations.

The working plan behind most of these is
[docs/OPENCODE-PARITY.md](docs/OPENCODE-PARITY.md) (in Russian); details of
each feature are in the [documentation](#documentation).

## Features

- Multi-provider LLM: Anthropic, OpenAI, Google Gemini, Mistral, xAI (Grok), Azure OpenAI, OpenRouter, OpenCode, agentgateway, Ollama (local or cloud), plus user-declared providers.
- Model roles (`default`/`smol`/`slow`/`plan`/…) selected by flag or `/model`.
- Agents as markdown files with YAML frontmatter; a set of agents ships bundled.
- Primary agents switchable mid-session (Shift+Tab, `/agent`).
- Permissions: `allow`/`deny`/`ask` per tool and per bash command pattern, with a TUI approval dialog.
- Subagents with a live monitor (Ctrl+T), steering, background runs and a prompt queue.
- Todo plans with a live sidebar.
- Interactive TUI: Markdown rendering, multiline prompt editor, paste summaries, `@`-mentions, fullscreen `/diff`, fuzzy search popup, custom themes.
- Sandboxed tools — read, write, edit, shell, grep, find, tree, git — restricted to the project directory.
- LSP integration (Go, TypeScript/JS, Python, Rust, Java) with diagnostics and auto-format.
- Session persistence: JSONL event logs with branching, compaction and resume.
- AI git tools: repository overview, diffs, hunk parsing, `/commit`, `/pr-autofix`.
- Memory Palace — 4-layer persistent memory with semantic search and a knowledge graph.
- Extensions: hooks, skills, MCP servers, plugin marketplaces (`pirate plugin`), skills security audit (`pirate audit`).
- Editor integration over ACP: Zed, JetBrains, VS Code; Unix-socket JSON-RPC and kagent.

## Documentation

Detailed documentation lives in [docs/](docs/README.md):

| Page | Covers |
|---|---|
| [Installation](docs/installation.md) | Quick install, Nix/NixOS, `go install`, build from source, pre-built binaries, release verification (attestations, SBOM) |
| [Usage](docs/usage.md) | Interactive & non-interactive modes, CLI flags, roles, API keys, agents, prompt editor, subagents, permissions, todo plans, JSON mode, slash commands, hotkeys, plugin marketplaces, Memory Palace, security audit |
| [Configuration](docs/configuration.md) | Config files, JSONC comments, `$schema`, model roles, declared providers, base URLs, permissions, attention, themes, Ollama tuning, web search, MCP servers |
| [Editor integration](docs/editor-integration.md) | Zed, JetBrains, VS Code extension, Unix-socket JSON-RPC, kagent |

Also in the repository:

- [ARCHITECTURE.md](ARCHITECTURE.md) — the codebase, package by package.
- [CONTRIBUTING.md](CONTRIBUTING.md) — how to contribute.
- [MODELS.md](MODELS.md) — supported models per provider.
- [docs/AGENTS-HOWTO.md](docs/AGENTS-HOWTO.md) — skills, hooks and agent instructions, explained against Claude Code's `.claude/` setup.
- [docs/README.md](docs/README.md) — the full documentation index.

## Architecture

```
cmd/pirate/          Entry point — CLI parsing, output mode selection
internal/
├── agent/          ADK agent setup, retry logic, runner
├── cli/            Cobra CLI flags, output modes (interactive, print, json, rpc)
├── config/         Global and project config (roles, hooks, MCP, themes)
├── permission/     Tool-call gating — allow/deny/ask on tool globs and bash patterns
├── audit/          Security scanner for skills (hidden Unicode, supply-chain threats)
├── extension/      Hooks, skills, MCP server integration
├── plugin/         Plugin marketplaces — manifests, registry, install/update
├── lsp/            LSP JSON-RPC client, language registry, manager, hooks
├── palace/         Memory Palace — drawers, layers, KG, miners, embedder, search
├── provider/       LLM providers implementing genai model interface
├── rpc/            Unix socket JSON-RPC 2.0 server
├── session/        JSONL persistence, branching, compaction
├── subagent/       Process spawner, orchestrator, concurrency pool
├── tools/          Sandboxed tools (read, write, edit, bash, grep, find, git, lsp)
└── tui/            Bubble Tea v2 UI, slash commands, commit workflow
```

Request flow: `User input → CLI → Agent → LLM provider → Tool calls → Sandbox → Response → TUI`,
with the session store, Memory Palace and LSP servers attached alongside.
See [ARCHITECTURE.md](ARCHITECTURE.md) for detailed documentation.

## Requirements

- At least one LLM provider API key, or a running [Ollama](https://ollama.com) instance.
- Go 1.27+ when building from source.

## License

See [LICENSE](LICENSE) for details.
