# agent-tui

An interactive terminal UI for talking to a configured Tavora agent —
Claude-Code-style. A scrolling output viewport sits on top, a prompt
input on the bottom, and SSE events from the agent (tool calls, JS
execution, sandbox events, the final response) stream into the
viewport live.

The TUI is intentionally narrow in scope: it talks to an agent that
already exists in the app. Persona, tools, skills, and store
bindings come from the agent's active version — the TUI doesn't author
or edit any of those. Skill upload, agent CRUD, and dashboard-style
observation live in the `tavora` CLI / admin UI.

Built on [Bubble Tea](https://github.com/charmbracelet/bubbletea) and
the extracted [`tavora-sdk-go`](https://github.com/tavora-ai/tavora-sdk-go)
so it doubles as a worked example for: config bootstrap, API-key
scoping, agent picking, version-bound session creation, document upload
from inside chat, and Bubble-Tea integration with the SDK's callback-
based `RunAgent` stream.

## Quick start

```bash
cd examples/agent-tui
go run .
```

First run drops you into a setup screen that asks for the Tavora URL
and an API key. The key is validated against `/api/sdk/space` before
anything is saved. On success the credentials persist to:

- macOS:  `~/Library/Application Support/tavora/agent-tui.json`
- Linux:  `$XDG_CONFIG_HOME/tavora/agent-tui.json` (or `~/.config/tavora/...`)
- Override: `TAVORA_TUI_CONFIG=/path/to/file`

API keys are app-scoped by construction. After credentials, the
TUI picks an agent:

- If the app has exactly one agent → auto-select.
- If 2+ agents → interactive picker (filter with `/`).
- If 0 agents → error pointing you at `tavora agents create <name>`.
- `--agent <id-or-name>` skips the picker.

The session is then created bound to the agent's active version, so
the runtime resolves persona, model, and the version's `skills_json`
filtering automatically. No agent → no TUI, by design.

## Subsequent runs

```bash
go run .                              # uses stored config
TAVORA_URL=... TAVORA_API_KEY=... go run .   # env wins; not persisted
go run . --reset-config               # rerun the setup screen
go run . --tools search,list_stores   # enable platform tools beyond the sandbox
go run . --session <id>               # resume a prior server-side session
```

Skill authoring and upload live in the `tavora` CLI, not the TUI:

```bash
tavora skills authoring-guide -o skill.md   # hand to Claude Code
tavora skills create --from-file ./mod.js   # upload a JS module skill
```

This split is intentional: chat-time work goes in the TUI, admin and
authoring work goes in the CLI. The TUI stays focused on driving an
already-configured agent.

When the TUI exits (`/quit`, `ctrl+c`, or fatal error) it prints the
current session ID and the exact `--session` invocation to resume it.
Sessions live server-side and retain their full history, so resuming
picks up where the conversation left off.

## In the chat

| Key / command          | Effect                                      |
|------------------------|---------------------------------------------|
| `enter`                | Send the prompt                             |
| `up` / `down`          | Browse prompt history (in-progress draft restored on `down` past newest) |
| `pgup` / `pgdown`      | Scroll the output viewport                  |
| `/help`                | Show command list                           |
| `/clear`               | Clear the output buffer                     |
| `/reset`               | Drop the current session and start a new one |
| `/upload <path> [store]` | Upload a document to a knowledge store the agent can `search()`. Auto-resolves to the agent's bound store if exactly one; otherwise specify the store explicitly by ID or name. |
| `/log`                 | Print the path of the active session log    |
| `/quit`, `ctrl+c`      | Exit (prints session ID + resume command)   |

The session is created once on startup and reused across turns, so the agent
remembers prior exchanges. `/reset` is the way to drop history.

## Logging

Every run writes to its own log file — alt-screen mode swallows `stderr`, so
errors and trace events go to disk where you can `tail -f` them in another
terminal. Files live alongside the config:

- macOS:    `~/Library/Application Support/tavora/logs/agent-tui-YYYYMMDD-HHMMSS.log`
- Linux:    `$XDG_CONFIG_HOME/tavora/logs/agent-tui-YYYYMMDD-HHMMSS.log`
- Override: `TAVORA_TUI_LOG_DIR=/path/to/dir`

The 10 most recent files are kept; older ones are pruned on startup. The
TUI prints the active log path as a system message after the session is
ready, and `/log` reprints it. On fatal startup errors (e.g. an invalid
stored API key with `--reset-config` not set) the path is also written to
stderr after the TUI exits.

```bash
tail -f "$(ls -t ~/Library/Application\ Support/tavora/logs/agent-tui-*.log | head -1)"
```

## Architecture

```
main.go         entry point, logger setup, config resolution, program wiring
config.go       load/save ~/.config/tavora/agent-tui.json + env-var fallback
logger.go       per-session slog file under <config>/tavora/logs/, prunes oldest
setup.go        Bubble Tea model: two-field setup (URL + key) with live validation
agent_picker.go Bubble Tea model: interactive agent picker via bubbles/list
tui.go          Bubble Tea model: viewport + textinput + spinner for the chat surface
runner.go       bridge from SDK RunAgent (callback) to a Go channel the TUI consumes
upload.go       /upload command: resolve store, async UploadDocument, render result
```

The interesting glue is `runner.go` + `waitForStream` in `tui.go`: the SDK
calls a callback per SSE event, but Bubble Tea wants `tea.Msg`s on its
update loop. So `runAgent` spawns a goroutine that funnels each callback
into a buffered channel, and the TUI issues a recursive `tea.Cmd` that
blocks on one channel read at a time and re-issues itself until the
stream closes. The UI thread never blocks on the network.
