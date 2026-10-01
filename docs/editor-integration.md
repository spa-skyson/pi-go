# Editor integration

Pi-rate can run as an Agent Client Protocol (ACP) server, so editors drive the
same agent — same tools, same memory, same sessions — that the terminal TUI
does. It also exposes a JSON-RPC 2.0 server over a Unix socket for anything
that speaks that instead.

- [Zed](#zed)
- [JetBrains IDEs](#jetbrains-ides)
- [VS Code](#vs-code)
- [Sessions survive the server](#sessions-survive-the-server)
- [Unix-socket JSON-RPC server](#unix-socket-json-rpc-20-server)
- [kagent](#kagent)

## Zed

Add pirate to Zed's `agent_servers` in your settings:

```json
{
  "agent_servers": {
    "pirate": {
      "type": "custom",
      "command": "pirate",
      "args": ["acp-server", "--model", "glm-5.2:cloud"],
      "env": {}
    }
  }
}
```

Then invoke via Zed's agent panel (`⌘⇧A` / `Ctrl+Shift+A`) and select "pirate". The agent runs in the current Zed project
directory with full access to pirate's tools and memory.

## JetBrains IDEs

JetBrains IDEs (IntelliJ IDEA, GoLand, PyCharm, WebStorm, …) discover ACP agents
from `~/.jetbrains/acp.json`. Add pirate under `agent_servers`:

```json
{
  "agent_servers": {
    "Pi-rate": {
      "command": "pirate",
      "args": ["acp-server", "--model", "agentgateway/ollama/glm-5.3-flash:cloud"]
    }
  }
}
```

Restart the IDE so it picks up the file, then open the AI Assistant / agent panel and select "Pi-rate". The agent runs in
the current project directory. `pirate acp-server` accepts `--model` plus `--url`, `--header key=value` (repeatable) and
`--insecure`; with no `--model` it falls back to `glm-5.2:cloud`.

## VS Code

The `vscode/` directory contains a VS Code extension that drives Pi-rate over
ACP, surfacing it as a native agent in VS Code's Chat/Agent Sessions UI. See
[vscode/README.md](../vscode/README.md) for installation and usage.

## Sessions survive the server

Every ACP session's transcript is written to the same store the terminal uses
(`~/.pirate/sessions/<session-id>/`, or `$PI_SESSIONS_DIR`), keyed by the ACP
session id. The server implements the protocol's session lifecycle on top of it:

| Method | What pirate does |
|---|---|
| `session/load` | Replays the stored transcript to the client, then continues it |
| `session/resume` | Continues the transcript without replaying it |
| `session/list` | Lists stored sessions, newest first, optionally filtered by `cwd` |

So an editor can restart pirate — or the machine — and pick a thread up where it
left off, and `pirate --session <id>` reopens the same conversation from the terminal.

## Unix-socket JSON-RPC 2.0 server

`--mode socket` serves JSON-RPC 2.0 over a Unix socket — the same request
surface as the TUI, for tooling that wants to drive Pi-rate programmatically:

```bash
pirate --mode socket --socket /tmp/pi-go.sock
```

For pi-compatible integrations there is also `--mode rpc`: NDJSON over stdio,
as used by pi-acp.

## kagent

Run Pi-rate as a custom agent inside [kagent](https://kagent.dev) on Agent Substrate via the A2A
adapter image. See [kagent-harness.md](kagent-harness.md) for the deployment guide and
[`specs/kagent/`](../specs/kagent/) for the Dockerfile, manifests, and step-by-step deploy notes.
