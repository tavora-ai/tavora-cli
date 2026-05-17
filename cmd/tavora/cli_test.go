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
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rogpeppe/go-internal/testscript"
)

// fakeBackend is the per-package mock Tavora server. Routes are
// added inline (no fixture files yet) so the canned shapes live
// next to the tests that assert on them.
type fakeBackend struct {
	srv          *httptest.Server
	requestCount atomic.Int64 // observable but unused for now; cheap diagnostic if a script starts flaking
}

func newFakeBackend() *fakeBackend {
	fb := &fakeBackend{}
	mux := http.NewServeMux()

	// GET /api/sdk/project — used by `tavora init` to look up the
	// project slug for tavora.jsonc when no --project flag is set.
	// Returning a fixed slug lets init scripts assert on it.
	mux.HandleFunc("/api/sdk/project", func(w http.ResponseWriter, r *http.Request) {
		fb.requestCount.Add(1)
		writeJSON(w, http.StatusOK, map[string]any{
			"id":   "11111111-1111-1111-1111-111111111111",
			"slug": "fake-project",
			"name": "Fake Project",
		})
	})

	// GET /api/sdk/agents?limit=...&offset=... — used by
	// `tavora agents list`. Two-row response keeps the table-format
	// assertion meaningful (one row would also match an empty
	// response stringification).
	mux.HandleFunc("/api/sdk/agents", func(w http.ResponseWriter, r *http.Request) {
		fb.requestCount.Add(1)
		// agents list uses GET only; everything else (POST create,
		// DELETE) falls through to the catch-all 404 below.
		if r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
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
		// credentials/config.
		Setup: func(env *testscript.Env) error {
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
