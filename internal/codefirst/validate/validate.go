// Package validate runs local pre-flight checks on a loaded
// source.Project. The rule of thumb: any check that can be done
// without a server round-trip lives here. Schema-level, file-level,
// and cross-file checks are all fair game.
//
// Errors are formatted for AI repair: each Issue includes the file,
// a short Code identifier (so tests can match on it), a one-line
// human-readable Message, and an optional Hint pointing at the
// concrete fix. See docs/code-first-agents-concept.md §Validation.
package validate

import (
	"fmt"
	"sort"
	"strings"

	"github.com/tavora-ai/tavora-cli/internal/codefirst/source"
)

// Known capability names. Server-side authoritative; this list is a
// fast-path catch for typos in the editor. Keep in sync with the
// sandbox packs in tavora-go/internal/sandbox/.
var knownCapabilities = map[string]bool{
	"search":   true,
	"fetch":    true,
	"ai":       true,
	"memory":   true,
	"indexes":  true,
	"require":  true,
	"remember": true,
}

// Severity controls whether an issue stops a sync/deploy.
type Severity string

const (
	Fatal Severity = "fatal"
	Warn  Severity = "warn"
)

// Issue extends source.Issue with a severity so the caller can
// distinguish "will not deploy" from "you probably want to fix this".
type Issue struct {
	source.Issue
	Severity Severity
}

// Project runs every check. Returns Issues sorted by file/line for
// stable output. The returned slice always includes the issues
// already recorded by the loader (those are Fatal).
func Project(p *source.Project) []Issue {
	var out []Issue

	for _, i := range p.Issues {
		out = append(out, Issue{Issue: i, Severity: Fatal})
	}

	if p.Manifest.Project == "" {
		out = append(out, Issue{
			Issue: source.Issue{
				File:    "tavora.jsonc",
				Code:    "missing-project-name",
				Message: `top-level "project" is required`,
				Hint:    `set "project": "<short-name>" in tavora.jsonc`,
			},
			Severity: Fatal,
		})
	}

	for _, a := range p.Agents {
		out = append(out, checkAgent(p, a)...)
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
	return out
}

func checkAgent(p *source.Project, a *source.Agent) []Issue {
	var out []Issue
	rel := relConfig(p.Root, a.ConfigPath)

	if a.Config.ID == "" {
		out = append(out, fatal(rel, "missing-agent-id", `"id" is required`, `add "id": "<short-name>" — must be unique within the project`))
	}
	if a.Config.Name == "" {
		out = append(out, warn(rel, "missing-agent-name", `"name" is empty`, `add "name": "<human label>" — used in the UI and trace headers`))
	}
	if a.Config.Model.Name == "" {
		out = append(out, fatal(rel, "missing-model", `"model.name" is required`, `set "model": { "provider": "gemini", "name": "gemini-2.5-flash" }`))
	}

	// Capability check (typo-only; server is authoritative).
	for _, c := range a.Config.Capabilities {
		if !knownCapabilities[c] {
			suggestions := suggestionsFor(c, mapKeys(knownCapabilities))
			hint := "known capabilities: " + strings.Join(mapKeysSorted(knownCapabilities), ", ")
			if len(suggestions) > 0 {
				hint = fmt.Sprintf("did you mean %s? (known: %s)", strings.Join(suggestions, " or "), strings.Join(mapKeysSorted(knownCapabilities), ", "))
			}
			out = append(out, warn(rel, "unknown-capability",
				fmt.Sprintf("unknown capability %q — typo?", c),
				hint))
		}
	}

	// Skill-side checks beyond what the loader already records. The
	// loader resolves each skill to a folder; module skills carry a
	// non-empty ModulePath pointing at main.js.
	for _, s := range a.Skills {
		if s.Kind == source.SkillModule && s.ModulePath != "" {
			if err := smokeCheckJS(s.ModulePath); err != nil {
				out = append(out, fatal(s.RelPath+"/main.js", "skill-js-parse",
					fmt.Sprintf("skill JS does not parse: %s", err),
					"the dev runtime treats this as a syntax error before any LLM sees it"))
			}
		}
	}

	// mcp/schedules/evals validators were dropped on 2026-05-16: those
	// stanzas were parsed by the CLI but ignored by the server, so the
	// fields plus their validators have left agent.jsonc for v0. The
	// imperative API (tavora mcp / tavora schedules / tavora evals)
	// still manages those resources.

	return out
}

func smokeCheckJS(path string) error {
	// We deliberately avoid bringing in a full JS parser as a CLI
	// dependency. A balanced-brace + balanced-quote check catches
	// 80% of accidental syntax mistakes (unclosed strings, stray
	// curly braces) with zero footprint. The authoritative parse
	// happens server-side when the draft is uploaded.
	b, err := readAll(path)
	if err != nil {
		return err
	}
	return balancedJSSmoke(b)
}

// --- helpers ---

func fatal(file, code, msg, hint string) Issue {
	return Issue{
		Issue: source.Issue{
			File:    file,
			Code:    code,
			Message: msg,
			Hint:    hint,
		},
		Severity: Fatal,
	}
}

func warn(file, code, msg, hint string) Issue {
	return Issue{
		Issue: source.Issue{
			File:    file,
			Code:    code,
			Message: msg,
			Hint:    hint,
		},
		Severity: Warn,
	}
}

// HasFatal returns true if at least one Fatal issue is present.
func HasFatal(issues []Issue) bool {
	for _, i := range issues {
		if i.Severity == Fatal {
			return true
		}
	}
	return false
}

// CountFatal returns how many Fatal issues are present.
func CountFatal(issues []Issue) int {
	n := 0
	for _, i := range issues {
		if i.Severity == Fatal {
			n++
		}
	}
	return n
}

// CountWarn returns how many warnings are present.
func CountWarn(issues []Issue) int {
	n := 0
	for _, i := range issues {
		if i.Severity == Warn {
			n++
		}
	}
	return n
}

func relConfig(root, path string) string {
	// Mirrors source.relTo but kept private to avoid the cross-pkg
	// dependency-direction wobble.
	if root == "" {
		return path
	}
	if strings.HasPrefix(path, root) {
		return strings.TrimPrefix(strings.TrimPrefix(path, root), "/")
	}
	return path
}

func mapKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func mapKeysSorted(m map[string]bool) []string {
	out := mapKeys(m)
	sort.Strings(out)
	return out
}

// suggestionsFor returns up to 2 entries from candidates that are
// "close enough" to s by edit distance.
func suggestionsFor(s string, candidates []string) []string {
	type scored struct {
		name string
		dist int
	}
	var ranked []scored
	for _, c := range candidates {
		d := levenshtein(s, c)
		if d <= 2 && d > 0 {
			ranked = append(ranked, scored{c, d})
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].dist < ranked[j].dist })
	var out []string
	for i, r := range ranked {
		if i >= 2 {
			break
		}
		out = append(out, r.name)
	}
	return out
}

func levenshtein(a, b string) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min3(curr[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}
