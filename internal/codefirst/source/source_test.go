package source_test

// Fixture-driven tests for source.Load. Each subdirectory under
// testdata/ is a sample tavora/ project tree pinning one shape:
// either a happy-path success or a specific Issue.Code.
//
// Why fixtures rather than in-memory trees: the loader walks the
// real filesystem via filepath.Glob + os.ReadFile, and a chunk of
// the behavior (path-suffix matching, glob expansion, sort order)
// is most accurately verified against actual files. The fixtures
// double as living documentation — a contributor can `cd
// testdata/happy` and see exactly what a valid project looks like.
//
// New fixtures should be small and self-contained: one isolated
// failure mode per directory, named for the Issue.Code it
// produces. Avoid stuffing several diagnostics into one fixture —
// the test asserts on the issue list, and overlap makes diffs
// noisy when one diagnostic's wording changes.

import (
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tavora-ai/tavora-cli/internal/codefirst/source"
)

// TestLoad_Happy is the canonical valid-project assertion. Two
// agents, mixed skill kinds, evals — locks in the in-memory shape
// agents/CLI verbs work against. If this fails on a refactor,
// it's the first signal that the public Project struct drifted.
func TestLoad_Happy(t *testing.T) {
	p, err := source.Load(filepath.Join("testdata", "happy"))
	require.NoError(t, err)
	require.NotNil(t, p)

	// Project-level shape.
	assert.Equal(t, "happy-fixture", p.Manifest.Project)
	assert.Equal(t, "agents/*/agent.jsonc", p.Manifest.Agents.Discover,
		"discover pattern should be preserved verbatim from the manifest")
	require.NotNil(t, p.Manifest.Retention)
	assert.Equal(t, 25, p.Manifest.Retention.Runs)
	assert.Empty(t, p.Issues, "happy fixture must load with zero issues; got %+v", p.Issues)

	// Two agents, sorted by config-path so order is deterministic.
	require.Len(t, p.Agents, 2)
	ids := []string{p.Agents[0].Config.ID, p.Agents[1].Config.ID}
	sort.Strings(ids)
	assert.Equal(t, []string{"reporter", "support"}, ids)

	// Reach into one agent and check the per-file resolution.
	support := agentByID(t, p, "support")
	assert.Equal(t, "Support Bot", support.Config.Name)
	assert.Equal(t, "gemini", support.Config.Model.Provider)
	assert.NotEmpty(t, support.Persona, "persona.md content should be loaded")
	assert.Contains(t, support.Persona, "support bot")

	// Two skills, both folders. order-status has main.js (Module),
	// style does not (Prompt). This is the contract the agent
	// runtime uses to decide which path to take per skill.
	require.Len(t, support.Skills, 2)
	skillsByName := map[string]source.Skill{}
	for _, s := range support.Skills {
		skillsByName[s.Name] = s
	}
	require.Contains(t, skillsByName, "order-status")
	require.Contains(t, skillsByName, "style")
	assert.Equal(t, source.SkillModule, skillsByName["order-status"].Kind,
		"order-status has main.js → should be a module skill")
	assert.NotEmpty(t, skillsByName["order-status"].ModulePath)
	assert.Equal(t, source.SkillPrompt, skillsByName["style"].Kind,
		"style has only skill.md → should be a prompt skill")
	assert.Empty(t, skillsByName["style"].ModulePath,
		"prompt-only skill must have an empty ModulePath")

	// Eval glob expands.
	require.Len(t, support.Evals, 1)
	assert.Contains(t, support.Evals[0].RelPath, "greeting.json")

	// SourceBytes covers every file the manifest touches. The CLI
	// later hashes this set into the source_hash field that the
	// server's source-sync handler dedupes against.
	require.NotNil(t, support.SourceBytes)
	hasPersona := false
	for path := range support.SourceBytes {
		if filepath.Base(path) == "persona.md" {
			hasPersona = true
		}
	}
	assert.True(t, hasPersona, "SourceBytes should include the persona.md content")
}

// TestLoad_MissingSkillMd — a skill folder must contain skill.md.
// The loader skips the binding (no Skill row) and emits a
// missing-skill-md issue pointing at the agent.jsonc that
// declared it.
func TestLoad_MissingSkillMd(t *testing.T) {
	p, err := source.Load(filepath.Join("testdata", "missing-skill-md"))
	require.NoError(t, err, "Load itself should succeed; the diagnostic lands in Issues")
	require.Len(t, p.Agents, 1)
	assert.Empty(t, p.Agents[0].Skills,
		"a folder without skill.md must not contribute a Skill row")
	assertHasIssueCode(t, p, "missing-skill-md")
}

// TestLoad_MissingPersona — agent.jsonc references persona.md but
// the file isn't there. Loader still produces an Agent (with empty
// Persona) and records the missing-persona issue. Validators
// downstream may upgrade it to fatal; the loader stays permissive.
func TestLoad_MissingPersona(t *testing.T) {
	p, err := source.Load(filepath.Join("testdata", "missing-persona"))
	require.NoError(t, err)
	require.Len(t, p.Agents, 1)
	assert.Empty(t, p.Agents[0].Persona,
		"missing persona file should leave Agent.Persona empty rather than crashing")
	assertHasIssueCode(t, p, "missing-persona")
}

// TestLoad_DuplicateAgentID — two agents declaring the same `id`
// in agent.jsonc. Both get loaded (history rehydration depends on
// being able to enumerate every file the user wrote), but a
// duplicate-agent-id issue is emitted so the CLI can refuse to
// sync until the conflict is resolved.
func TestLoad_DuplicateAgentID(t *testing.T) {
	p, err := source.Load(filepath.Join("testdata", "duplicate-id"))
	require.NoError(t, err)
	assert.Len(t, p.Agents, 2, "both agents should still load — Issues drive the refusal, not loader truncation")
	assertHasIssueCode(t, p, "duplicate-agent-id")
}

// TestLoad_UnparseableAgentConfig — agent.jsonc that isn't valid
// JSONC. The agent is dropped (no Agent row), and a
// parse-agent-config issue is emitted with the file path so the
// developer can find the line to fix.
func TestLoad_UnparseableAgentConfig(t *testing.T) {
	p, err := source.Load(filepath.Join("testdata", "unparseable"))
	require.NoError(t, err, "an unparseable agent shouldn't break the whole project load")
	assert.Empty(t, p.Agents, "unparseable agent should not appear in Agents")
	assertHasIssueCode(t, p, "parse-agent-config")
}

// TestLoad_ExtraSkillFile — files other than skill.md / main.js
// inside a skill folder are not bundled into the skill, but they
// don't fail the load. The loader surfaces an extra-skill-file
// warning so the user knows the file is being ignored.
func TestLoad_ExtraSkillFile(t *testing.T) {
	p, err := source.Load(filepath.Join("testdata", "extra-skill-file"))
	require.NoError(t, err)
	require.Len(t, p.Agents, 1)
	require.Len(t, p.Agents[0].Skills, 1,
		"the skill loads successfully — the extra file is a warning, not a failure")
	assertHasIssueCode(t, p, "extra-skill-file")
}

// TestLoad_SkillWithTypedef pins the .d.ts contract: main.d.ts is
// a recognized skill file (no extra-skill-file warning), its bytes
// land in SourceBytes so the server can ingest them, and the path
// is recorded on the Skill struct so consumers know it's present.
func TestLoad_SkillWithTypedef(t *testing.T) {
	p, err := source.Load(filepath.Join("testdata", "skill-with-typedef"))
	require.NoError(t, err)
	require.Len(t, p.Agents, 1)

	// No extra-skill-file warning — main.d.ts is allowlisted.
	for _, i := range p.Issues {
		assert.NotEqual(t, "extra-skill-file", i.Code,
			"main.d.ts must not trigger extra-skill-file: %+v", i)
	}

	a := p.Agents[0]
	require.Len(t, a.Skills, 1)
	assert.NotEmpty(t, a.Skills[0].TypedefPath, "TypedefPath must point at main.d.ts")

	// SourceBytes carries main.d.ts so the server's source-sync can
	// ingest typedef_dts onto the skills row.
	found := false
	for path := range a.SourceBytes {
		if filepath.Base(path) == "main.d.ts" {
			found = true
			break
		}
	}
	assert.True(t, found, "main.d.ts must be bundled in SourceBytes")
}

// TestLoad_MissingManifest — pointing Load at a directory without
// a tavora.jsonc must return an error (not just a Project with
// issues). This is the one case where the user can't be expected
// to react to an Issue — the loader has no manifest to read at
// all.
func TestLoad_MissingManifest(t *testing.T) {
	_, err := source.Load(filepath.Join("testdata"))
	require.Error(t, err, "Load against a directory without tavora.jsonc must fail outright")
}

// agentByID is a small lookup helper so the test reads naturally;
// fails the test rather than returning nil + ok so the call site
// doesn't need an extra branch.
func agentByID(t *testing.T, p *source.Project, id string) *source.Agent {
	t.Helper()
	for _, a := range p.Agents {
		if a.Config.ID == id {
			return a
		}
	}
	t.Fatalf("no agent with id %q in %+v", id, p.Agents)
	return nil
}

// assertHasIssueCode asserts the project carries at least one
// Issue with the given Code. Prints the full issue list on
// failure so the test report names exactly what diagnostics
// landed instead, which is the slowest debug step otherwise.
func assertHasIssueCode(t *testing.T, p *source.Project, code string) {
	t.Helper()
	for _, i := range p.Issues {
		if i.Code == code {
			return
		}
	}
	t.Fatalf("expected an Issue with Code=%q in project; got %+v", code, p.Issues)
}
