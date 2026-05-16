# tavora-tools

Developer tools for the [Tavora](https://tavora.ai) agentic intelligence
platform. Two binaries, one module:

| Binary | Purpose |
|---|---|
| [`tavora`](./cmd/tavora) | The user CLI — manage apps, agents, skills, documents, MCP servers, evals, schedules, and more from your terminal |
| [`tavora-tui`](./cmd/tavora-tui) | An interactive terminal UI for chatting with a configured Tavora agent — Claude-Code-style |

Both depend on the public Go SDK [`tavora-sdk-go`](https://github.com/tavora-ai/tavora-sdk-go).

## Code-first workflow (in flight)

The next major direction for `tavora` is a Convex-style code-first
authoring loop. Agent definitions live in a local `tavora/` folder
and three new verbs manage them:

```sh
tavora init       # scaffold tavora/ with one agent + persona + skill + eval
tavora dev        # watch + validate + sync a mutable dev draft
tavora deploy     # cut an immutable published version
```

Folder shape:

```
tavora/
  tavora.jsonc
  agents/
    support/
      agent.jsonc         # config: model, capabilities, skills, mcp, schedules
      persona.md          # system prompt
      skills/
        order-status.js   # module skill (.js)
        refund-policy.md  # prompt skill (.md)
      evals/
        happy-path.json
```

Skills are exactly three things: **`.js` files** (module skills,
sandboxed in Goja), **`.md` files** (prompt skills), and **MCP
config** inline in `agent.jsonc`. Indexes are referenced by name,
not authored in code. Secrets and `${VAR}` substitutions resolve
server-side from the target environment's store — never from a
local shell.

The driving value is that **AI coding tools (Cursor, Claude Code)
can edit Tavora agents the way they already edit the rest of a
codebase.** Git review, rollback, and shareable `require()`-able
skills follow as secondary benefits.

Existing CLI commands (`tavora agents list`, `tavora app show`,
`tavora evals run`, …) stay unchanged and operate on **runtime**
state. The new code-first commands manage **desired** state. The
browser UI under `platform.tavora.ai/` stays editable for
code-managed agents with a "managed in `tavora/agents/<id>/`" banner
— `tavora dev` reconciles on next sync.

### Verification loop

Authoring without verification is half a loop, so v0 ships a closed
feedback loop optimized for AI coding tools (Cursor, Claude Code):

- **Auto-written session logs.** Every dev-draft invocation writes a
  self-contained markdown file to `tavora/.runs/<ts>-<agent>-<sid>.md`
  — input, output, full trace (`think` snippets + skill calls +
  results), errors, tokens, version hash. The AI reads it with its
  native file-reading tools — matches the muscle memory it already
  has for `tsc` output, test reports, and coverage files. `.runs/`
  is gitignored and retention-capped (default 50).
- **Evals as pass/fail signal.** `evals/*.json` + `tavora test
  --draft` gives the AI a deterministic verification target — the
  agent equivalent of TDD. Failing cases dump their session log to
  `.runs/`.
- **Ad-hoc CLI inspection.** `tavora run <agent> "<input>" --draft`,
  `tavora session latest|<id>`, `tavora config show <agent>` (emits
  the resolved config so the AI can check "did my edit parse?"
  before spending tokens on a behavior test).

Status: design approved 2026-05-15; v0 implementation pending.
Going forward this is the primary integration path for SDK users.

## Why a separate repo

The tools live here, not in the closed-source app repo, because:

1. **Public surface coherence.** External customers see one library
   ([`tavora-sdk-go`](https://github.com/tavora-ai/tavora-sdk-go)) and
   one tools repo (`tavora-tools`). Both public, both versioned
   independently.
2. **Release decoupling.** Bumping the CLI or TUI doesn't require a
   server release. Bumping the server doesn't force a tools release.
3. **CI parallelism.** Tools tests are fast; the server suite is heavy
   (testcontainers Postgres). Splitting keeps both lanes responsive.

## Install

Pre-built binaries: see the [Releases](../../releases) page.

From source:

```sh
git clone https://github.com/tavora-ai/tavora-tools
cd tavora-tools
go install ./cmd/tavora      # CLI
go install ./cmd/tavora-tui  # TUI
```

## First-run

Both binaries pick up `TAVORA_URL` and `TAVORA_API_KEY` from the
environment. The TUI also persists them to `~/.config/tavora/agent-tui.json`
after first interactive setup.

```sh
export TAVORA_URL=https://api.tavora.ai
export TAVORA_API_KEY=tvr_...

tavora app show
tavora agents list
tavora-tui
```

## Layout

```
tavora-tools/
├── cmd/
│   ├── tavora/             # CLI (Cobra, resty)
│   └── tavora-tui/         # TUI (Bubble Tea + bubbles + lipgloss)
├── go.mod                  # single module; both bins build from here
└── README.md
```

## Development

```sh
go build ./...                          # build both
go test ./...                           # run tests
go run ./cmd/tavora app show      # CLI
go run ./cmd/tavora-tui                 # TUI
```

### Working against a local SDK checkout

By default the build pulls `tavora-sdk-go` at its tagged version from
the public registry. To iterate against unreleased SDK changes, add a
local replace directive temporarily:

```sh
# from tavora-tools/
go mod edit -replace github.com/tavora-ai/tavora-sdk-go=../tavora-sdk-go
go mod tidy
# … iterate …
# undo before commit:
go mod edit -dropreplace github.com/tavora-ai/tavora-sdk-go
go mod tidy
```

Pre-tag, the replace was permanent (no published v0.x.y existed). Now
that `v0.1.0+` ships, registry-resolved is the default and replace is
opt-in for development.

## License

[MIT](./LICENSE)
