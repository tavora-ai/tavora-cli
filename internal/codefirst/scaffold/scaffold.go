// Package scaffold writes a starter tavora/ folder to disk.
//
// The starter is intentionally minimal but complete: one agent, one
// JS module skill, one prompt skill, one eval case, plus the
// .gitignore that hides .runs/. The shape mirrors the canonical
// example in docs/code-first-agents-concept.md.
package scaffold

import (
	"fmt"
	"os"
	"path/filepath"
)

// Options controls what Write writes.
type Options struct {
	// Root is the directory that should contain tavora.jsonc.
	// Typically `<cwd>/tavora`.
	Root string
	// ProjectName lands in tavora.jsonc.
	ProjectName string
	// APIURL lands in tavora.jsonc (omitted if empty).
	APIURL string
	// Force overwrites existing files. When false, Write refuses to
	// touch any file that already exists.
	Force bool
}

// File represents a single file the scaffolder will write.
type File struct {
	RelPath string
	Body    string
}

// Plan returns the files Write would create, without writing them.
// Useful for `tavora init --dry-run` and tests.
func Plan(opt Options) []File {
	apiLine := ""
	if opt.APIURL != "" {
		apiLine = fmt.Sprintf("  \"apiUrl\": %q,\n", opt.APIURL)
	}
	manifest := fmt.Sprintf(`{
  // Tavora code-first project manifest.
  // Docs: https://docs.tavora.ai/code-first/manifest
  "$schema": "https://docs.tavora.ai/schemas/project.schema.json",
  "project": %q,
%s  "agents": {
    "discover": "agents/*/agent.jsonc"
  },
  "retention": {
    // .runs/ session log retention; server keeps full history.
    "runs": 50
  }
}
`, opt.ProjectName, apiLine)

	agent := `{
  // The starter agent. Edit this folder, or copy it to create a
  // second agent alongside ("agents/<id>/agent.jsonc").
  "$schema": "https://docs.tavora.ai/schemas/agent.schema.json",
  "id": "starter",
  "name": "Starter",
  "model": {
    "provider": "gemini",
    "name": "gemini-2.5-flash"
  },
  "persona": "./persona.md",
  "capabilities": ["search", "fetch", "ai"],

  // Skills are folders. Each folder needs a skill.md (the
  // LLM-facing prompt) and may include a main.js (require()'d
  // by the JS thinking-core when present).
  "skills": [
    "./skills/now/",
    "./skills/style/"
  ],

  // Indexes are referenced by name; ingestion lives in the UI/CLI.
  "indexes": [],

  // Eval cases live alongside the agent as JSON files. Each case
  // is {name?, input, criteria, pass_threshold?}; the server
  // upserts them at sync time and pins this agent's suite to the
  // resulting set. Run them via tavora-evals or from the UI.
  "evals": [
    "./evals/*.json"
  ]

  // MCP servers and schedules are still managed via the imperative
  // API for v0 (tavora mcp / tavora schedules). They were
  // previously fields here too, but the server didn't write them
  // through; they'll return once the wire-through lands.
}
`

	persona := `You are a helpful assistant.

Be concise, accurate, and direct. When you don't know something, say
so plainly rather than guessing. Prefer short, well-structured
answers over long ones.

Replace this file with your actual persona — it's the system prompt
your agent runs with.
`

	nowSkillMD := "# now — current ISO timestamp\n\n" +
		"Returns the current time in ISO 8601 format. Use whenever the\n" +
		"agent needs a timestamp (logs, \"as of …\" phrasing, freshness\n" +
		"checks).\n\n" +
		"Call from a think script with `require('now').iso()`.\n"

	nowSkillJS := `// now — return the current ISO timestamp.
//
// Module skills are CommonJS-style. The thinking-core require()'s
// this file from inside the JS reasoning snippet the LLM emits, then
// calls the exported function. Replace this stub with whatever
// domain logic you need (fetch your API, query a database, etc.).

module.exports = {
  /**
   * @returns {string} current time in ISO 8601 format
   */
  iso() {
    return new Date().toISOString();
  },
};
`

	styleSkillMD := `# Style

Write in plain language. Avoid jargon unless the user used it first.
One idea per paragraph; prefer concrete examples over abstractions.

Format guidance:

- Use bullet lists when enumerating three or more things.
- Use code blocks for code, commands, and file paths.
- Skip preambles like "Sure!" or "Great question!".

Edit this file to encode whatever writing conventions your agent
should follow — it gets concatenated into the system prompt.
`

	basicEval := `{
  "$schema": "https://docs.tavora.ai/schemas/evalcase.schema.json",
  "name": "responds-to-greeting",
  "input": "Hello — can you help me?",
  "criteria": "Response is a brief, polite acknowledgment that offers to help. Does not include preambles like 'Sure!' or 'Great question!'.",
  "pass_threshold": 7
}
`

	gitignore := `# Tavora session logs — regenerated on every dev-draft invocation.
# Studio keeps the full server-side history; the local copy is just
# for AI coding tools that read from disk.
.runs/

# Per-developer deployment binding (TAVORA_DEPLOYMENT). Each
# developer's clone gets their own; sharing this file would point
# two devs at the same dev draft and cause sync collisions.
.env.local

# Editor swap files
*.swp
.DS_Store
`

	// AGENTS.md is the convention AI coding tools (Cursor, Claude
	// Code, Aider, …) look for when they land in an unfamiliar
	// folder. We deliberately put it INSIDE tavora/ rather than at
	// the repo root so it stays scoped to this folder's workflow —
	// the user's own repo may already have its own AGENTS.md /
	// CLAUDE.md / README at the root, and we don't want to clobber
	// or compete with that.
	agentsMD := fmt.Sprintf(`# Tavora code-first agents — folder guide

This folder defines Tavora agents as code. Files here are the source
of truth; %[1]stavora dev%[1]s syncs them to a mutable dev draft on the
server, and %[1]stavora deploy%[1]s cuts an immutable published version.

## Layout

%[2]s
tavora/
  tavora.jsonc                          # project manifest
  agents/
    <agent-id>/
      agent.jsonc                       # model, capabilities, skill + index + eval bindings
      persona.md                        # system prompt
      skills/
        <name>/                         # one folder per skill
          skill.md                      # required — LLM-facing prompt + how to use
          main.js                       # optional — require()'d from the JS thinking-core
      evals/
        <case>.json                     # eval cases ({name?, input, criteria, pass_threshold?})
  .runs/                                # auto-generated session logs (gitignored)
%[2]s

## Workflow

%[2]ssh
tavora init                             # scaffolded this folder
tavora dev                              # watch + validate + sync a dev draft
tavora config show <agent-id>           # print the resolved config (smoke-test your edit parsed)
tavora run <agent-id> "<input>"         # invoke the draft, stream the trace, write .runs/<sid>.md
tavora session latest                   # print the most recent run's markdown to stdout
tavora session get <id-or-filename>     # print a specific run
tavora deploy                           # promote the draft to an immutable published version
%[2]s

Pass %[1]s--no-sync%[1]s to %[1]stavora dev%[1]s to validate locally without contacting
the server. Pass %[1]s--once%[1]s for a single pass (CI-friendly). Pass
%[1]s--live%[1]s to %[1]stavora run%[1]s to invoke the deployed version instead of the
draft.

## What each file means

- **tavora.jsonc** — project name, agent-discover glob, %[1]s.runs/%[1]s
  retention. JSONC = JSON with %[1]s//%[1]s and %[1]s/* */%[1]s comments and trailing
  commas; the schema URL gives IDE autocomplete.
- **agent.jsonc** — one per agent folder. %[1]sid%[1]s is the stable local
  identifier the server maps to its own agent UUID on first sync;
  don't rename it casually (use %[1]stavora rename%[1]s). Fields:
  %[1]smodel%[1]s, %[1]scapabilities%[1]s (search/fetch/ai/memory/indexes/require/
  remember), %[1]sskills%[1]s (file paths or globs), %[1]sindexes%[1]s (referenced
  by name; ingestion stays in the UI), %[1]sevals%[1]s (file paths or globs;
  upserted at sync time and pinned as this agent's suite). MCP
  servers and schedules are NOT here for v0 — manage them via
  %[1]stavora mcp%[1]s / %[1]stavora schedules%[1]s.
- **persona.md** — the system prompt. Plain markdown; the runtime
  passes it verbatim to the model.
- **skills/<name>/skill.md** — required for every skill. The
  LLM-facing prompt explaining what the skill does and when to use
  it. For a prompt-only skill this is the whole skill; for a module
  skill, this is how the LLM learns about %[1]srequire('<name>')%[1]s.
- **skills/<name>/main.js** — optional. Module entry point,
  CommonJS-shaped: %[1]smodule.exports = { fn(...) { ... } }%[1]s. The LLM
  emits JS in its %[1]sthink%[1]s loop and %[1]srequire('<name>')%[1]s's this
  module to compose tasks. Folder name = require() id.
- **evals/*.json** — eval cases. One per file with %[1]s{name?, input,
  criteria, pass_threshold?}%[1]s. Source-sync upserts them under the
  agent's suite (%[1]s__cf__/<agent-id>%[1]s namespace); the resulting
  suite is auto-pinned to the agent so %[1]stavora evals run%[1]s and the
  Evaluate tab find them.

## Editing tips for AI coding tools

The AI verification loop is a closed loop on disk:

1. Edit a file (persona.md, skills/<name>/skill.md or main.js, agent.jsonc, …).
2. %[1]stavora config show <agent>%[1]s — the cheap "did my change
   parse, and did the binding land?" check.
3. %[1]stavora run <agent> "<input>"%[1]s — invoke the just-synced draft.
   This auto-writes %[1]s.runs/<timestamp>-<agent>-<sid>.md%[1]s containing
   the input, every think snippet, every skill call + return, the
   final output, errors, tokens, and duration.
4. Read the markdown with your native file tools, then loop back to
   step 1.

Other useful facts:

- Errors include a file path, an issue %[1]scode%[1]s tag, a one-line
  message, and a %[1]shint:%[1]s line pointing at the concrete fix.
- Secrets and environment values: never write them to files.
  Use %[1]s${VAR_NAME}%[1]s in agent.jsonc; values resolve server-side
  from the dev environment's store.
- %[1]s.runs/%[1]s retention defaults to the most recent 50 files;
  override via %[1]stavora.jsonc → retention.runs%[1]s. Studio keeps the
  full server-side history.

## Things that DON'T belong here

- Document/index contents (managed via the UI/CLI; %[1]sindexes%[1]s in
  agent.jsonc is a *reference* by name only).
- Plain-text secrets (use secretRef + %[1]s${VAR}%[1]s).
- Per-developer credentials (those live in %[1]s~/.tavora.yaml%[1]s, set
  via %[1]stavora login%[1]s).

## Concept doc

See %[1]stavora-go/docs/code-first-agents-concept.md%[1]s for the design
rationale (why JSONC, why one folder per agent, why drafts and
published versions share %[1]sagent_versions%[1]s with a %[1]skind%[1]s column).
`, "`", "```")

	return []File{
		{"tavora.jsonc", manifest},
		{"AGENTS.md", agentsMD},
		{"agents/starter/agent.jsonc", agent},
		{"agents/starter/persona.md", persona},
		{"agents/starter/skills/now/skill.md", nowSkillMD},
		{"agents/starter/skills/now/main.js", nowSkillJS},
		{"agents/starter/skills/style/skill.md", styleSkillMD},
		{"agents/starter/evals/basic.json", basicEval},
		{".gitignore", gitignore},
	}
}

// Write materializes the planned files under opt.Root. Returns the
// list of paths actually written (skipped files are silently
// omitted when Force is false but already exist).
func Write(opt Options) ([]string, error) {
	if opt.Root == "" {
		return nil, fmt.Errorf("scaffold: empty Root")
	}
	if err := os.MkdirAll(opt.Root, 0o755); err != nil {
		return nil, fmt.Errorf("create %s: %w", opt.Root, err)
	}

	var written []string
	for _, f := range Plan(opt) {
		full := filepath.Join(opt.Root, f.RelPath)
		if !opt.Force {
			if _, err := os.Stat(full); err == nil {
				continue
			}
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return nil, fmt.Errorf("mkdir %s: %w", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(f.Body), 0o644); err != nil {
			return nil, fmt.Errorf("write %s: %w", full, err)
		}
		written = append(written, full)
	}
	return written, nil
}
