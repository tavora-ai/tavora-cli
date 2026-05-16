// Package source loads and represents a tavora/ project tree.
//
// A Project corresponds to a single tavora.jsonc plus the agents it
// discovers via glob. Each Agent has its own folder (agent.jsonc,
// persona.md, skills/*, evals/*).
//
// Loading is intentionally permissive: missing files and bad globs
// surface as Issues rather than hard errors, so the `tavora dev`
// watcher and the validator can keep working with partial state
// while the developer fixes the next thing.
package source

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tavora-ai/tavora-tools/internal/codefirst/jsonc"
)

// Project is the in-memory shape of a tavora.jsonc file plus all
// agents it discovers.
type Project struct {
	Root     string  // absolute path to the dir containing tavora.jsonc
	Manifest Manifest
	Agents   []*Agent
	// Issues collected during load. Validation appends more.
	Issues []Issue
}

// Manifest is the top-level tavora.jsonc payload.
type Manifest struct {
	Schema    string         `json:"$schema,omitempty"`
	Project   string         `json:"project"`
	APIURL    string         `json:"apiUrl,omitempty"`
	Agents    AgentsDiscover `json:"agents,omitempty"`
	Retention *Retention     `json:"retention,omitempty"`
}

type AgentsDiscover struct {
	Discover string `json:"discover,omitempty"`
}

type Retention struct {
	Runs int `json:"runs,omitempty"`
}

// Agent is a single agents/<id>/agent.jsonc plus the files it pulls
// in.
type Agent struct {
	Dir        string      // absolute folder for the agent
	ConfigPath string      // absolute path to agent.jsonc
	Config     AgentConfig // raw parsed config
	Persona    string      // resolved persona markdown contents
	Skills     []Skill
	Evals      []Eval

	// SourceBytes keeps the raw bytes of every file the agent
	// pulls in, keyed by repo-relative path. The CLI hashes this
	// to build the sync manifest.
	SourceBytes map[string][]byte
}

// AgentConfig mirrors agent.jsonc.
//
// v0 scope: config covers identity (id/name/persona), model selection,
// skills (file paths/globs), capabilities (sandbox allowlist), index
// references, and eval-case globs (re-added 2026-05-16 after the
// server-side wire-through landed). mcp + schedules remain on the
// imperative API for v0 — same parsed-but-ignored concern blocked
// them from returning yet.
type AgentConfig struct {
	Schema       string            `json:"$schema,omitempty"`
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Model        ModelRef          `json:"model"`
	Persona      string            `json:"persona,omitempty"`
	Capabilities []string          `json:"capabilities,omitempty"`
	Skills       []string          `json:"skills,omitempty"`
	Indexes      []string          `json:"indexes,omitempty"`
	Evals        []string          `json:"evals,omitempty"`
	Env          map[string]string `json:"env,omitempty"`
}

type ModelRef struct {
	Provider string `json:"provider"`
	Name     string `json:"name"`
}

// Eval is a resolved eval JSON file. The file shape is defined in
// tavora-docs/public/schemas/evalcase.schema.json:
//
//	{ "name": "<optional>", "input": "...", "criteria": "...",
//	  "pass_threshold": 7 }
//
// The server parses the file content at source-sync time and
// upserts it into eval_cases (namespaced under
// `__cf__/<agent-local-id>`, same pattern as code-first skills).
type Eval struct {
	Path    string
	RelPath string
}

// Skill is a resolved skill file — either a .js module skill or a
// .md prompt skill.
type Skill struct {
	Kind       SkillKind
	BindingRaw string // the path as written in agent.jsonc
	Path       string // absolute resolved path
	RelPath    string // path relative to project root
}

type SkillKind string

const (
	SkillModule SkillKind = "module"
	SkillPrompt SkillKind = "prompt"
)

// Issue is a load-time problem the CLI wants to surface to the user
// with file/line context (line is 0 if unknown).
type Issue struct {
	File    string
	Line    int
	Col     int
	Code    string // short identifier like "missing-skill-file" — useful for tests
	Message string
	Hint    string // optional one-line repair suggestion
}

func (i Issue) String() string {
	loc := i.File
	if i.Line > 0 {
		loc = fmt.Sprintf("%s:%d", i.File, i.Line)
		if i.Col > 0 {
			loc = fmt.Sprintf("%s:%d", loc, i.Col)
		}
	}
	out := fmt.Sprintf("%s\n  %s", loc, i.Message)
	if i.Hint != "" {
		out += "\n  hint: " + i.Hint
	}
	return out
}

// Load reads tavora.jsonc at the given path (file or directory) and
// returns a Project. Returns an error only for cases where loading
// can't begin (root missing, manifest unparseable); per-agent
// problems land in Project.Issues.
func Load(start string) (*Project, error) {
	manifestPath, err := findManifest(start)
	if err != nil {
		return nil, err
	}
	root := filepath.Dir(manifestPath)

	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", manifestPath, err)
	}

	var m Manifest
	if err := jsonc.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", manifestPath, err)
	}
	if m.Project == "" {
		return nil, fmt.Errorf("%s: missing required field \"project\"", manifestPath)
	}
	if m.Agents.Discover == "" {
		m.Agents.Discover = "agents/*/agent.jsonc"
	}

	p := &Project{
		Root:     root,
		Manifest: m,
	}
	p.SourceFiles() // ensure map type initialized — see Agent.SourceBytes

	pattern := filepath.Join(root, filepath.FromSlash(m.Agents.Discover))
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid agent discover pattern %q: %w", m.Agents.Discover, err)
	}
	sort.Strings(matches)

	for _, ac := range matches {
		agent, agentIssues := loadAgent(root, ac)
		p.Issues = append(p.Issues, agentIssues...)
		if agent != nil {
			p.Agents = append(p.Agents, agent)
		}
	}

	// Sweep for duplicate agent IDs.
	seen := map[string]string{}
	for _, a := range p.Agents {
		if prev, ok := seen[a.Config.ID]; ok {
			p.Issues = append(p.Issues, Issue{
				File:    relTo(root, a.ConfigPath),
				Code:    "duplicate-agent-id",
				Message: fmt.Sprintf("duplicate agent id %q (also defined at %s)", a.Config.ID, relTo(root, prev)),
				Hint:    "agent ids must be unique within a project",
			})
		} else {
			seen[a.Config.ID] = a.ConfigPath
		}
	}

	return p, nil
}

// SourceFiles returns a flat map of repo-relative path → raw bytes
// covering the manifest, every agent config, every persona, every
// skill, and every eval the project loaded. The manifest sync uses
// this to compute file-level hashes.
func (p *Project) SourceFiles() map[string][]byte {
	out := map[string][]byte{}
	// Manifest
	if b, err := os.ReadFile(filepath.Join(p.Root, "tavora.jsonc")); err == nil {
		out["tavora.jsonc"] = b
	}
	for _, a := range p.Agents {
		for k, v := range a.SourceBytes {
			out[k] = v
		}
	}
	return out
}

func findManifest(start string) (string, error) {
	info, err := os.Stat(start)
	if err != nil {
		return "", fmt.Errorf("path %q not found: %w", start, err)
	}
	if !info.IsDir() {
		// File handed in directly
		return filepath.Abs(start)
	}
	// Walk up from start until we find tavora.jsonc or hit root.
	cur, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		cand := filepath.Join(cur, "tavora.jsonc")
		if _, err := os.Stat(cand); err == nil {
			return cand, nil
		}
		// Also try ./tavora/tavora.jsonc as a courtesy from the repo root
		cand2 := filepath.Join(cur, "tavora", "tavora.jsonc")
		if _, err := os.Stat(cand2); err == nil {
			return cand2, nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", fmt.Errorf("no tavora.jsonc found at or above %s", start)
		}
		cur = parent
	}
}

func loadAgent(root, agentConfigPath string) (*Agent, []Issue) {
	var issues []Issue
	raw, err := os.ReadFile(agentConfigPath)
	if err != nil {
		issues = append(issues, Issue{
			File:    relTo(root, agentConfigPath),
			Code:    "read-agent-config",
			Message: err.Error(),
		})
		return nil, issues
	}
	var cfg AgentConfig
	if err := jsonc.Unmarshal(raw, &cfg); err != nil {
		issues = append(issues, Issue{
			File:    relTo(root, agentConfigPath),
			Code:    "parse-agent-config",
			Message: err.Error(),
			Hint:    "check JSON commas, quotes, and the $schema reference",
		})
		return nil, issues
	}
	if cfg.ID == "" {
		issues = append(issues, Issue{
			File:    relTo(root, agentConfigPath),
			Code:    "missing-agent-id",
			Message: `agent.jsonc is missing required field "id"`,
		})
	}

	dir := filepath.Dir(agentConfigPath)
	a := &Agent{
		Dir:         dir,
		ConfigPath:  agentConfigPath,
		Config:      cfg,
		SourceBytes: map[string][]byte{},
	}
	a.SourceBytes[relTo(root, agentConfigPath)] = raw

	// Persona
	if cfg.Persona != "" {
		personaPath := resolveRelative(dir, cfg.Persona)
		if b, err := os.ReadFile(personaPath); err != nil {
			issues = append(issues, Issue{
				File:    relTo(root, agentConfigPath),
				Code:    "missing-persona",
				Message: fmt.Sprintf("persona file %q not found", cfg.Persona),
				Hint:    "create the file or remove the \"persona\" field",
			})
		} else {
			a.Persona = string(b)
			a.SourceBytes[relTo(root, personaPath)] = b
		}
	}

	// Skills (globs allowed; each binding may match multiple files)
	for _, binding := range cfg.Skills {
		matches, err := resolveGlob(dir, binding)
		if err != nil {
			issues = append(issues, Issue{
				File:    relTo(root, agentConfigPath),
				Code:    "invalid-skill-glob",
				Message: fmt.Sprintf("invalid skill pattern %q: %s", binding, err),
			})
			continue
		}
		if len(matches) == 0 {
			issues = append(issues, Issue{
				File:    relTo(root, agentConfigPath),
				Code:    "missing-skill-file",
				Message: fmt.Sprintf("skill binding %q matches no files", binding),
				Hint:    `expected one of "./skills/<name>.js" (module) or "./skills/<name>.md" (prompt)`,
			})
			continue
		}
		for _, m := range matches {
			kind, ok := skillKindFor(m)
			if !ok {
				issues = append(issues, Issue{
					File:    relTo(root, agentConfigPath),
					Code:    "bad-skill-extension",
					Message: fmt.Sprintf("skill file %q has unsupported extension (only .js and .md)", relTo(root, m)),
				})
				continue
			}
			b, err := os.ReadFile(m)
			if err != nil {
				issues = append(issues, Issue{
					File:    relTo(root, agentConfigPath),
					Code:    "read-skill-file",
					Message: err.Error(),
				})
				continue
			}
			a.Skills = append(a.Skills, Skill{
				Kind:       kind,
				BindingRaw: binding,
				Path:       m,
				RelPath:    relTo(root, m),
			})
			a.SourceBytes[relTo(root, m)] = b
		}
	}

	// Evals (globs allowed). Each match becomes both a manifest file
	// entry (so the server can parse the JSON) and an Agent.Evals
	// pointer (so `tavora config show` and validators can iterate).
	for _, binding := range cfg.Evals {
		matches, err := resolveGlob(dir, binding)
		if err != nil {
			issues = append(issues, Issue{
				File:    relTo(root, agentConfigPath),
				Code:    "invalid-eval-glob",
				Message: fmt.Sprintf("invalid eval pattern %q: %s", binding, err),
			})
			continue
		}
		if len(matches) == 0 {
			issues = append(issues, Issue{
				File:    relTo(root, agentConfigPath),
				Code:    "missing-eval-file",
				Message: fmt.Sprintf("eval binding %q matches no files", binding),
				Hint:    `expected one or more "./evals/<name>.json" files`,
			})
			continue
		}
		for _, m := range matches {
			if !strings.HasSuffix(strings.ToLower(m), ".json") {
				issues = append(issues, Issue{
					File:    relTo(root, agentConfigPath),
					Code:    "bad-eval-extension",
					Message: fmt.Sprintf("eval file %q must be .json", relTo(root, m)),
				})
				continue
			}
			b, err := os.ReadFile(m)
			if err != nil {
				issues = append(issues, Issue{
					File:    relTo(root, agentConfigPath),
					Code:    "read-eval-file",
					Message: err.Error(),
				})
				continue
			}
			a.Evals = append(a.Evals, Eval{Path: m, RelPath: relTo(root, m)})
			a.SourceBytes[relTo(root, m)] = b
		}
	}

	return a, issues
}

func skillKindFor(path string) (SkillKind, bool) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".js":
		return SkillModule, true
	case ".md":
		return SkillPrompt, true
	default:
		return "", false
	}
}

// resolveRelative resolves a path relative to the agent's folder.
// Absolute paths and explicit "./" / "../" prefixes are both honored.
func resolveRelative(base, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Clean(filepath.Join(base, p))
}

// resolveGlob expands a glob pattern relative to base. It returns a
// sorted list of matches and an error only for malformed patterns;
// no matches is a valid empty result.
func resolveGlob(base, pattern string) ([]string, error) {
	full := resolveRelative(base, pattern)
	matches, err := filepath.Glob(full)
	if err != nil {
		return nil, err
	}
	// If the pattern contains no glob magic and matched nothing, fall
	// back to a plain-stat existence check so the missing-file path
	// produces a clean error.
	if len(matches) == 0 && !containsGlobMagic(pattern) {
		if _, err := os.Stat(full); err == nil {
			matches = []string{full}
		}
	}
	sort.Strings(matches)
	return matches, nil
}

func containsGlobMagic(p string) bool {
	return strings.ContainsAny(p, "*?[")
}

func relTo(root, p string) string {
	r, err := filepath.Rel(root, p)
	if err != nil {
		return p
	}
	return filepath.ToSlash(r)
}

// PrettyJSON marshals v as indented JSON for human (and AI) review.
func PrettyJSON(v any) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}

// WalkFiles is a deterministic walk of every file under tavora/
// matching .jsonc, .md, .js, .json. Useful for the dev watcher.
func WalkFiles(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			// Skip the .runs/ folder; it's auto-generated.
			if name == ".runs" || name == ".git" || strings.HasPrefix(name, "node_modules") {
				return fs.SkipDir
			}
			return nil
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".jsonc", ".md", ".js", ".json":
			out = append(out, path)
		}
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}
