# tavora-cli

Developer tools for the [Tavora](https://tavora.ai) agentic intelligence
platform. One binary, one module:

| Binary | Purpose |
|---|---|
| [`tavora`](./cmd/tavora) | The user CLI — manage projects, agents, skills, documents, MCP servers, evals, schedules, and the interactive `tavora tui` chat surface. Folder-aware against a local `tavora/` project. |

The previous standalone `tavora-tui` binary was retired 2026-05-17 —
the same code now lives in `internal/tui/` and runs via `tavora tui`.

Depends on the public Go SDK [`tavora-sdk-go`](https://github.com/tavora-ai/tavora-sdk-go).

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
   one tools repo (`tavora-cli`). Both public, both versioned
   independently.
2. **Release decoupling.** Bumping the CLI or TUI doesn't require a
   server release. Bumping the server doesn't force a tools release.
3. **CI parallelism.** Tools tests are fast; the server suite is heavy
   (testcontainers Postgres). Splitting keeps both lanes responsive.

## Install

Three channels:

```sh
# npm — wraps the Go binary; postinstall fetches the right artifact
npm i -g @tavora/cli            # or pnpm add -g, yarn global add

# Homebrew (tap not yet published — coming alongside first tagged release)
brew install tavora-ai/tap/tavora

# From source
git clone https://github.com/tavora-ai/tavora-cli
cd tavora-cli
go install ./cmd/tavora
```

The npm package (`./npm/`) is a thin shim over the same Go binary —
it downloads the platform-specific prebuilt on `postinstall`. See
[`npm/README.md`](./npm/README.md) for the install flow + release
pipeline (cross-compile via GOOS/GOARCH, gzip artifacts, attach to
the GitHub Release, then `npm publish`).

## First-run

The CLI prefers credentials in this order: command flags
(`--api-key`, `--url`), env vars (`TAVORA_API_KEY`, `TAVORA_URL`),
then the config file at `~/.tavora.yaml` written by `tavora login`.

```sh
tavora login                       # interactive — captures key + URL
tavora project show
tavora agents list
tavora tui                         # interactive chat surface
```

In folder mode (when cwd contains a `tavora/` project), `tavora tui`
auto-syncs the folder, scopes the agent picker to local agents,
defaults the session target to the dev draft, and downloads
emitted assets into `<project>/.assets/<session-id>/`.

## Layout

```
tavora-cli/
├── cmd/
│   └── tavora/             # CLI (Cobra, resty)
├── internal/
│   ├── codefirst/          # source loader / validator / runs / scaffold
│   └── tui/                # interactive TUI (Bubble Tea v2 + bubbles v2 + lipgloss v2)
├── go.mod
└── README.md
```

## Development

```sh
go build ./...                       # build the tavora binary
go test ./...                        # run tests
go run ./cmd/tavora project show
go run ./cmd/tavora tui              # TUI from source
```

### Working against a local SDK checkout

By default the build pulls `tavora-sdk-go` at its tagged version from
the public registry. To iterate against unreleased SDK changes, add a
local replace directive temporarily:

```sh
# from tavora-cli/
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
