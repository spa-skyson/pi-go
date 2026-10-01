# Pi-rate documentation

Reference documentation for [Pi-rate](../README.md), a terminal coding agent.
The README stays short; the depth lives here.

| Page | What it covers |
|---|---|
| [Installation](installation.md) | Quick install, Nix/NixOS, `go install`, building from source, pre-built binaries, release verification (attestations, SBOM) |
| [Usage](usage.md) | Interactive and non-interactive modes, CLI flags, model roles, agents, prompt editor, `@`-mentions, subagents and the monitor, permissions, todo plans, JSON mode, slash commands, hotkeys, plugin marketplaces, Memory Palace, security audit |
| [Configuration](configuration.md) | Config file locations, JSONC comments, `$schema`, model roles, declared providers, base URLs, attention, themes, Ollama tuning, web search, custom OpenAI-compatible endpoints, MCP servers |
| [Editor integration](editor-integration.md) | Zed, JetBrains IDEs, VS Code extension, Unix-socket JSON-RPC server, kagent |
| [Architecture](../ARCHITECTURE.md) | Package map and design of the codebase (repo root) |

## Project-level documents

| Document | What it is |
|---|---|
| [ARCHITECTURE.md](../ARCHITECTURE.md) | Codebase architecture (repo root, English) |
| [CONTRIBUTING.md](../CONTRIBUTING.md) | How to contribute |
| [MODELS.md](../MODELS.md) | Supported models per provider |
| [AGENTS-HOWTO.md](AGENTS-HOWTO.md) | Configuring skills, agent instructions, hooks and subagents — written for readers coming from Claude Code's `.claude/` setup |
| [OPENCODE-PARITY.md](OPENCODE-PARITY.md) | Working plan (in Russian) for reaching feature parity with opencode — the origin of most fork features |
| [kagent-harness.md](kagent-harness.md) | Running Pi-rate as a kagent agent on Agent Substrate via the A2A adapter |
| [complexity-refactor.md](complexity-refactor.md) | Evidence log of the cyclomatic-complexity refactor |
| [performance-analysis-260711-0038.md](performance-analysis-260711-0038.md) | One-off live performance analysis of session `260711-0038` (2026-07-11): slow LLM gaps from cloud latency, thinking overhead and context growth |
| [DESIGN-COMPARISON.md](DESIGN-COMPARISON.md) | Design comparison: pi-go (Go) vs the original pi coding-agent (TypeScript) |
| [DESCRIPTION.md](DESCRIPTION.md) | Older architecture write-up (predates `ARCHITECTURE.md`; kept for reference) |
| [articles/](articles/) | Spec articles: memory systems, sandbox, web search, ACP end-of-task, self-improving agents |
| [superpowers/](superpowers/) | Specs imported with the Superpowers plugin |
| [../specs/](../specs/) | Design specs for features under development |

## Quick pointers

- API keys and providers: [Usage → API keys](usage.md#api-keys), [Configuration → Declared providers](configuration.md#declared-providers)
- Permissions (`allow`/`deny`/`ask`): [Usage → Permissions](usage.md#permissions), [Configuration → Permission rules](configuration.md#permission-rules)
- Custom themes: [Configuration → Custom themes](configuration.md#custom-themes)
- Verifying a release binary: [Installation → Verifying a release](installation.md#verifying-a-release)
