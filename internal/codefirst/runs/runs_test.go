package runs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tavora "github.com/tavora-ai/tavora-sdk-go"
)

func TestRecorder_RenderHappyPath(t *testing.T) {
	r := &Recorder{StartedAt: time.Date(2026, 5, 16, 15, 30, 45, 0, time.UTC)}
	r.Handle(tavora.AgentEvent{
		Type:    tavora.EventTypeExecuteJS,
		Content: `return ai("hi");`,
	})
	r.Handle(tavora.AgentEvent{
		Type:    tavora.EventTypeExecuteJSResult,
		Content: `"hello!"`,
	})
	r.Handle(tavora.AgentEvent{
		Type:    tavora.EventTypeResponse,
		Content: "Hello, friend.",
		Tokens:  &tavora.CallTokens{Prompt: 100, Completion: 12},
	})
	r.Handle(tavora.AgentEvent{
		Type: tavora.EventTypeDone,
		Summary: &tavora.RunSummary{
			SessionID: "sess-1234",
			Steps:     2,
			Tokens: struct {
				Prompt     int32 `json:"prompt"`
				Completion int32 `json:"completion"`
			}{Prompt: 100, Completion: 12},
		},
	})

	out := r.Render(Meta{
		AgentLocalID: "support",
		AgentID:      "7f2a-uuid",
		SessionID:    "sess-1234",
		DraftHash:    "sha256:abcdef0123456789",
		Target:       "draft",
		Input:        "Hi there",
	})

	mustContain(t, out, "# Session 2026-05-16T15:30:45Z")
	mustContain(t, out, "**Agent:** support (7f2a-uuid) (draft@abcdef012345)")
	mustContain(t, out, "**Input:** Hi there")
	mustContain(t, out, "**Output:** Hello, friend.")
	mustContain(t, out, "prompt=100 completion=12")
	mustContain(t, out, "**Steps:** 2")
	mustContain(t, out, "### Step 1 — execute_js")
	mustContain(t, out, "return ai(\"hi\");")
	mustContain(t, out, "→ \"hello!\"")
	mustContain(t, out, "### Step 2 — response")
	mustContain(t, out, "## Errors\n\n(none)")
}

func TestRecorder_RenderError(t *testing.T) {
	r := New()
	r.Handle(tavora.AgentEvent{
		Type:    tavora.EventTypeExecuteJS,
		Content: "throw new Error('boom');",
	})
	r.Handle(tavora.AgentEvent{
		Type:    tavora.EventTypeError,
		Content: "uncaught: boom (skill order-status.js line 4)",
	})

	out := r.Render(Meta{
		AgentLocalID: "support",
		SessionID:    "sess",
		Input:        "ping",
	})

	mustContain(t, out, "## Errors")
	mustContain(t, out, "uncaught: boom")
	mustNotContain(t, out, "(none)")
}

func TestRecorder_WriteCreatesFile(t *testing.T) {
	dir := t.TempDir()
	r := New()
	r.Handle(tavora.AgentEvent{Type: tavora.EventTypeResponse, Content: "ok"})

	path, err := r.Write(dir, Meta{
		AgentLocalID: "support",
		SessionID:    "sess-abc123",
		Input:        "ping",
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if filepath.Dir(path) != dir {
		t.Fatalf("expected file under %s, got %s", dir, path)
	}
	name := filepath.Base(path)
	if !strings.HasSuffix(name, ".md") {
		t.Fatalf("expected .md suffix, got %s", name)
	}
	if !strings.Contains(name, "support") {
		t.Fatalf("expected agent slug in filename: %s", name)
	}
	if !strings.Contains(name, "sess-a") {
		t.Fatalf("expected short session id in filename: %s", name)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(body), "**Output:** ok") {
		t.Fatalf("file body missing output line: %s", body)
	}
}

func TestPruneOld_KeepsNewest(t *testing.T) {
	dir := t.TempDir()
	// Create 5 fake files; touch them with ascending mtimes so the
	// sort order is deterministic.
	base := time.Now().Add(-time.Hour)
	var paths []string
	for i := 0; i < 5; i++ {
		p := filepath.Join(dir, "session-"+string(rune('a'+i))+".md")
		if err := os.WriteFile(p, []byte("body"), 0o644); err != nil {
			t.Fatal(err)
		}
		mod := base.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(p, mod, mod); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}

	removed, err := PruneOld(dir, 2)
	if err != nil {
		t.Fatalf("PruneOld: %v", err)
	}
	if len(removed) != 3 {
		t.Fatalf("expected 3 removals, got %d: %v", len(removed), removed)
	}
	// The two newest (e and d) should survive.
	survivors := mustListDir(t, dir)
	if len(survivors) != 2 {
		t.Fatalf("expected 2 survivors, got %d: %v", len(survivors), survivors)
	}
	wantSurvive := map[string]bool{
		filepath.Base(paths[3]): true,
		filepath.Base(paths[4]): true,
	}
	for _, s := range survivors {
		if !wantSurvive[s] {
			t.Fatalf("unexpected survivor: %s (wanted only the 2 newest)", s)
		}
	}
}

func TestResolveSession_ExactAndPartial(t *testing.T) {
	dir := t.TempDir()
	mk := func(name string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	a := mk("2026-05-16T15-30-00Z-support-a3f9b1.md")
	b := mk("2026-05-16T15-31-00Z-support-b7c2d4.md")
	_ = a
	_ = b

	got, err := ResolveSession(dir, "a3f9b1")
	if err != nil {
		t.Fatalf("partial match: %v", err)
	}
	if got != a {
		t.Fatalf("expected match on %q to return %s, got %s", "a3f9b1", a, got)
	}

	_, err = ResolveSession(dir, "support")
	if err == nil {
		t.Fatalf("expected ambiguity error on shared substring 'support'")
	}
}

func mustContain(t *testing.T, s, sub string) {
	t.Helper()
	if !strings.Contains(s, sub) {
		t.Fatalf("expected %q in:\n---\n%s\n---", sub, s)
	}
}

func mustNotContain(t *testing.T, s, sub string) {
	t.Helper()
	if strings.Contains(s, sub) {
		t.Fatalf("did not expect %q in:\n---\n%s\n---", sub, s)
	}
}

func mustListDir(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}
