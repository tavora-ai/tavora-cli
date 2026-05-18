package main

// CLI test scaffolding via rogpeppe/go-internal/testscript.
//
// Scripts live under cmd/tavora/testdata/script/*.txtar. Each script
// runs the real `tavora` binary (via testscript.RunMain re-entering
// this test binary as if it were `tavora`) against:
//
//   - A fresh tempdir as cwd (testscript's default).
//   - An isolated HOME (so `tavora login` doesn't touch the dev's
//     real ~/.tavora.yaml).
//   - A per-test httptest.Server stamped into TAVORA_URL, returning
//     canned JSON for the routes the CLI hits.
//
// What this scaffolding covers vs leaves out:
//   - Covers: flag parsing, output formatting, code-first scaffolding
//     (init's file plan), the shape of API responses the CLI must
//     accept. Roughly the surface that breaks when someone renames
//     a flag, edits a help string, or changes a JSON field name.
//   - Leaves out: SSE streaming (tavora agents run), websocket-ish
//     interactive prompts (tui), the actual sandbox runtime. Those
//     live in their own suites — tavora-go for runtime, internal/tui
//     for picker UX.
//
// Subprocess vs in-process: testscript invokes a fresh copy of the
// test binary for every `tavora ...` line, paying ~50ms per
// invocation in exchange for no global-state leakage between
// scripts. With ~50 scripts × ~5 commands, that's a few seconds —
// cheap for CI, and immune to the cobra-globals fragility that an
// in-process Cobra reset would have to navigate.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rogpeppe/go-internal/testscript"
)

// fakeBackend is the per-package mock Tavora server. Routes are
// added inline (no fixture files yet) so the canned shapes live
// next to the tests that assert on them.
//
// State: stateless for read endpoints (same canned response each
// call); the agents-CRUD path holds a tiny in-memory map so the
// "create → get → delete → 404" round-trip is meaningful. The map
// is guarded by mu and reset between TestCLI invocations via the
// reset method (called from TestMain on each test entry — see
// comments in TestCLI).
type fakeBackend struct {
	srv          *httptest.Server
	requestCount atomic.Int64 // observable but unused for now; cheap diagnostic if a script starts flaking

	mu        sync.Mutex
	sessions  map[string]map[string]any // agent_session_id → session JSON
	sessionID atomic.Int64              // monotonic, not derived from map length — so create-after-delete still increments
}

func newFakeBackend() *fakeBackend {
	fb := &fakeBackend{
		sessions: make(map[string]map[string]any),
	}
	mux := http.NewServeMux()

	// GET /api/sdk/project — used by `tavora init` (for the project
	// slug fallback) and `tavora project show`. Full Project shape
	// per tavora-sdk-go/projects.go so the SDK decoder doesn't lose
	// fields the human-format output reads (Name, Slug, Description,
	// CreatedAt).
	mux.HandleFunc("GET /api/sdk/project", func(w http.ResponseWriter, r *http.Request) {
		fb.requestCount.Add(1)
		writeJSON(w, http.StatusOK, map[string]any{
			"id":          "11111111-1111-1111-1111-111111111111",
			"team_id":     "22222222-2222-2222-2222-222222222222",
			"slug":        "fake-project",
			"name":        "Fake Project",
			"description": "Mocked project for testscript",
			"created_at":  time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC),
			"updated_at":  time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC),
		})
	})

	// GET /api/sdk/skills — `tavora skills list`. Two-row response
	// (module + prompt) keeps the table check non-trivial AND lines
	// up with the canonical post-code-first shape (skills are now
	// authored under tavora/agents/<id>/skills/, types `module` or
	// `prompt`).
	mux.HandleFunc("GET /api/sdk/skills", func(w http.ResponseWriter, r *http.Request) {
		fb.requestCount.Add(1)
		now := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
		writeJSON(w, http.StatusOK, map[string]any{
			"skills": []map[string]any{
				{
					"id":          "33333333-3333-3333-3333-333333333333",
					"project_id":  "11111111-1111-1111-1111-111111111111",
					"name":        "__cf__/support/now",
					"description": "current ISO timestamp",
					"type":        "module",
					"prompt":      "# now\nReturns the current time.",
					"enabled":     true,
					"created_at":  now,
					"updated_at":  now,
				},
				{
					"id":          "44444444-4444-4444-4444-444444444444",
					"project_id":  "11111111-1111-1111-1111-111111111111",
					"name":        "__cf__/support/style",
					"description": "style guide",
					"type":        "prompt",
					"prompt":      "# Style\nWrite plainly.",
					"enabled":     true,
					"created_at":  now,
					"updated_at":  now,
				},
			},
		})
	})

	// GET /api/sdk/agents?limit=...&offset=... — `tavora agents
	// list`. Stateless: same two rows each call. Two rows keep the
	// table-format assertion non-trivial.
	mux.HandleFunc("GET /api/sdk/agents", func(w http.ResponseWriter, r *http.Request) {
		fb.requestCount.Add(1)
		now := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
		writeJSON(w, http.StatusOK, map[string]any{
			"sessions": []map[string]any{
				{
					"id":         "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
					"project_id": "11111111-1111-1111-1111-111111111111",
					"title":      "First session",
					"status":     "active",
					"model":      "gemini-2.5-flash",
					"created_at": now,
					"updated_at": now,
				},
				{
					"id":         "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
					"project_id": "11111111-1111-1111-1111-111111111111",
					"title":      "Second session",
					"status":     "completed",
					"model":      "gemini-2.5-pro",
					"created_at": now,
					"updated_at": now,
				},
			},
		})
	})

	// POST /api/sdk/agents — `tavora agents create`. Reads the
	// title from the request body, mints a deterministic-ish id
	// (sequence-based so two creates in one script get distinct
	// ids), stores in the in-memory map, returns the row.
	mux.HandleFunc("POST /api/sdk/agents", func(w http.ResponseWriter, r *http.Request) {
		fb.requestCount.Add(1)
		var input struct {
			Title        string `json:"title"`
			SystemPrompt string `json:"system_prompt"`
			Model        string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&input)
		if input.Model == "" {
			input.Model = "gemini-2.5-flash"
		}
		now := time.Now().UTC()
		id := fmt.Sprintf("ccccc%03d-cccc-cccc-cccc-cccccccccccc", fb.sessionID.Add(1))
		fb.mu.Lock()
		session := map[string]any{
			"id":            id,
			"project_id":    "11111111-1111-1111-1111-111111111111",
			"title":         input.Title,
			"system_prompt": input.SystemPrompt,
			"status":        "active",
			"model":         input.Model,
			"created_at":    now,
			"updated_at":    now,
		}
		fb.sessions[id] = session
		fb.mu.Unlock()
		writeJSON(w, http.StatusCreated, session)
	})

	// GET /api/sdk/agents/{id} — `tavora agents get`. The detail
	// shape is {session, steps[]}; empty steps for the mock since
	// none of the scripts assert on step content.
	mux.HandleFunc("GET /api/sdk/agents/{id}", func(w http.ResponseWriter, r *http.Request) {
		fb.requestCount.Add(1)
		id := r.PathValue("id")
		fb.mu.Lock()
		session, ok := fb.sessions[id]
		fb.mu.Unlock()
		if !ok {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"session": session,
			"steps":   []any{},
		})
	})

	// DELETE /api/sdk/agents/{id} — `tavora agents delete`. 204
	// on success matches the SDK contract (delete returns no body).
	mux.HandleFunc("DELETE /api/sdk/agents/{id}", func(w http.ResponseWriter, r *http.Request) {
		fb.requestCount.Add(1)
		id := r.PathValue("id")
		fb.mu.Lock()
		_, ok := fb.sessions[id]
		delete(fb.sessions, id)
		fb.mu.Unlock()
		if !ok {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	// Catch-all: unknown routes get a clear 404 so a script that
	// accidentally hits an un-mocked endpoint fails loudly with the
	// path included, rather than producing a confusing CLI error.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fb.requestCount.Add(1)
		http.Error(w, "fake-backend: no route registered for "+r.Method+" "+r.URL.Path, http.StatusNotFound)
	})

	fb.srv = httptest.NewServer(mux)
	return fb
}

// reset clears the per-test in-memory state. Called between
// testscript scripts so one script's leftover sessions don't make
// another script's "fresh project" assertions flake. Resets the
// session id counter too so scripts asserting on specific ids
// (`ccccc001…`) don't drift when run alongside others.
func (fb *fakeBackend) reset() {
	fb.mu.Lock()
	defer fb.mu.Unlock()
	fb.sessions = make(map[string]map[string]any)
	fb.sessionID.Store(0)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// fakeServer is package-scoped so TestMain can start it once and
// every script can read its URL from TAVORA_URL. Sharing one server
// across scripts is fine because routes are stateless (same canned
// response every call).
var fakeServer *fakeBackend

func TestMain(m *testing.M) {
	fakeServer = newFakeBackend()
	defer fakeServer.srv.Close()

	// RunMain re-invokes this test binary as if it were `tavora`,
	// dispatching to the function below. The first map entry wins
	// when an arg matches a registered name; everything else falls
	// through to `go test`'s normal main.
	exit := testscript.RunMain(m, map[string]func() int{
		"tavora": run,
	})
	os.Exit(exit)
}

// TestCLI is the testscript entry point. Each .txtar under
// testdata/script/ becomes a subtest named after the file.
func TestCLI(t *testing.T) {
	testscript.Run(t, testscript.Params{
		Dir: "testdata/script",
		// Setup runs per-script, before any commands. Use it to
		// pin env so tests don't see the developer's real
		// credentials/config, and to reset any per-script backend
		// state from the previous script.
		Setup: func(env *testscript.Env) error {
			fakeServer.reset()
			home := env.WorkDir + "/home"
			if err := os.MkdirAll(home, 0o755); err != nil {
				return err
			}
			env.Setenv("HOME", home)
			env.Setenv("TAVORA_URL", fakeServer.srv.URL)
			// Force English locale so error messages and time
			// formatting are predictable across CI environments.
			env.Setenv("LC_ALL", "C")
			// Make sure no stray TAVORA_* from the dev's shell
			// leaks into the subprocess. testscript already starts
			// with an empty env, but cobra reads TAVORA_API_KEY in
			// PersistentPreRunE — being explicit beats a flaky
			// "passes on my machine".
			env.Setenv("TAVORA_API_KEY", "")
			env.Setenv("TAVORA_CONFIG", "")
			env.Setenv("TAVORA_DEPLOYMENT", "")
			return nil
		},
		// TestWork=true would keep the workdir around for debugging.
		// Leave off by default; set TESTSCRIPT_KEEP_WORK=1 to opt in.
		TestWork: strings.EqualFold(os.Getenv("TESTSCRIPT_KEEP_WORK"), "1"),
	})
}
