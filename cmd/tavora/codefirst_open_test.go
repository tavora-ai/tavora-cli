package main

import (
	"path/filepath"
	"testing"
)

// Unit-test the ref → file path mapper. Real filesystem and editor
// invocation live outside the test surface — those are covered by
// e2e/cli when it grows.
func TestResolveReference(t *testing.T) {
	t.Parallel()
	root := "/tmp/projectroot"
	cases := []struct {
		ref  string
		want string
	}{
		// Bare agent id → agent.jsonc.
		{"support", filepath.Join(root, "agents/support/agent.jsonc")},
		// Aliases for the same file.
		{"support/agent.jsonc", filepath.Join(root, "agents/support/agent.jsonc")},
		{"support/config", filepath.Join(root, "agents/support/agent.jsonc")},
		// Persona variants.
		{"support/persona", filepath.Join(root, "agents/support/persona.md")},
		{"support/persona.md", filepath.Join(root, "agents/support/persona.md")},
		// Skills folder vs file.
		{"support/skills", filepath.Join(root, "agents/support/skills")},
		{"support/skills/greet.js", filepath.Join(root, "agents/support/skills/greet.js")},
		// Evals: implicit .json on the name leaf.
		{"support/evals", filepath.Join(root, "agents/support/evals")},
		{"support/evals/greeting", filepath.Join(root, "agents/support/evals/greeting.json")},
		// Pass-through for non-canonical sub-paths.
		{"support/notes.md", filepath.Join(root, "agents/support/notes.md")},
		// Trailing slash is fine.
		{"support/", filepath.Join(root, "agents/support/agent.jsonc")},
	}
	for _, tc := range cases {
		t.Run(tc.ref, func(t *testing.T) {
			got, err := resolveReference(root, tc.ref)
			if err != nil {
				t.Fatalf("resolveReference(%q) errored: %v", tc.ref, err)
			}
			if got != tc.want {
				t.Errorf("resolveReference(%q) = %q, want %q", tc.ref, got, tc.want)
			}
		})
	}

	// Negative: empty ref errors.
	if _, err := resolveReference(root, ""); err == nil {
		t.Errorf("expected error for empty ref, got nil")
	}
}
