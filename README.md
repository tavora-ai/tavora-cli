# tavora-tools

Developer tools for the [Tavora](https://tavora.ai) agentic intelligence
platform. Two binaries, one module:

| Binary | Purpose |
|---|---|
| [`tavora`](./cmd/tavora) | The user CLI — manage products, agents, skills, documents, MCP servers, evals, schedules, and more from your terminal |
| [`tavora-tui`](./cmd/tavora-tui) | An interactive terminal UI for chatting with a configured Tavora agent — Claude-Code-style |

Both depend on the public Go SDK [`tavora-sdk-go`](https://github.com/tavora-ai/tavora-sdk-go).

## Why a separate repo

The tools live here, not in the closed-source product repo, because:

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

tavora product show
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
go run ./cmd/tavora product show      # CLI
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
