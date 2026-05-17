package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// loadDeploymentSlug returns the value of TAVORA_DEPLOYMENT to send
// on outgoing API requests as X-Tavora-Deployment, or "" if no
// binding is configured. Resolution order:
//
//  1. TAVORA_DEPLOYMENT environment variable (explicit override).
//  2. ./tavora/.env.local (canonical location written by `tavora init`).
//  3. ./.env.local (when invoked from inside the tavora/ folder).
//
// Returns the raw value, including any "dev:" / "staging:" / "prod:"
// prefix. The server's middleware strips the prefix at lookup time.
// Empty string means "no header" — the server falls back to the
// project's prod deployment (or auto-creates one) via the resolver.
func loadDeploymentSlug() string {
	if v := strings.TrimSpace(os.Getenv("TAVORA_DEPLOYMENT")); v != "" {
		return v
	}
	for _, candidate := range []string{"tavora/.env.local", ".env.local"} {
		if v := readDeploymentFromEnvFile(candidate); v != "" {
			return v
		}
	}
	return ""
}

// readDeploymentFromEnvFile parses a tiny KEY=value file and returns
// the TAVORA_DEPLOYMENT value if present. Tolerates blank lines,
// comments (#), and surrounding quotes around the value. Returns ""
// on any error (file missing, parse failure) — this is an optional
// configuration source, not a load-bearing config file.
func readDeploymentFromEnvFile(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if strings.TrimSpace(key) != "TAVORA_DEPLOYMENT" {
			continue
		}
		v := strings.TrimSpace(value)
		// Strip surrounding double or single quotes if present.
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		return v
	}
	return ""
}

// CurrentSyncSource is set by dev/deploy before invoking source-sync
// so the deploymentHeaderTransport can stamp X-Tavora-Source on the
// outgoing request. Empty means "don't send the header" — older
// commands that don't care about source-flip detection are unaffected.
//
// Package-level rather than a per-client option because the SDK
// doesn't expose request-scoped headers; the alternative (rebuilding
// the SDK client per call) is uglier than this small bit of mutable
// state.
var CurrentSyncSource string

// deploymentHeaderTransport wraps another RoundTripper and adds the
// X-Tavora-Deployment header (and optionally X-Tavora-Source) to
// every outgoing request. The deployment slug is captured at
// client-construction time — a long-running `tavora dev` doesn't see
// .env.local edits mid-flight, matching Convex's CLI lifetime
// expectation. The source path reads from CurrentSyncSource on
// every request so dev/deploy can set it per-watch.
type deploymentHeaderTransport struct {
	slug string
	base http.RoundTripper
}

func (t *deploymentHeaderTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	src := CurrentSyncSource
	if t.slug == "" && src == "" {
		return t.base.RoundTrip(req)
	}
	// Clone before mutating to avoid surprising any caller that
	// reuses the request struct (resty doesn't, but the RoundTripper
	// contract says we shouldn't).
	clone := req.Clone(req.Context())
	if t.slug != "" {
		clone.Header.Set("X-Tavora-Deployment", t.slug)
	}
	if src != "" {
		clone.Header.Set("X-Tavora-Source", src)
	}
	return t.base.RoundTrip(clone)
}

// httpClientForDeployment returns an *http.Client whose Transport
// injects X-Tavora-Deployment when a binding is configured. Pass
// the result to tavora.WithHTTPClient when constructing the SDK
// client.
func httpClientForDeployment() *http.Client {
	return &http.Client{
		Transport: &deploymentHeaderTransport{
			slug: loadDeploymentSlug(),
			base: http.DefaultTransport,
		},
	}
}

// bindCloudDeployment calls POST /api/sdk/deployments to mint (or
// reuse) the API key's project's prod deployment, then writes its slug
// to <root>/.env.local so subsequent CLI invocations attach the
// X-Tavora-Deployment header automatically.
//
// Best-effort by contract: callers should treat any error as a
// non-fatal "binding skipped" condition. `tavora init`'s primary
// job is the local scaffold, and the next `tavora dev` will hit
// the server's resolver fallback regardless of whether .env.local
// exists.
func bindCloudDeployment(root string) error {
	url, key := resolveAPIConfig()
	if key == "" {
		return fmt.Errorf("no API key configured (run `tavora login` first)")
	}

	// kind='dev' is per-developer: server derives owner_user_id from
	// the API key's recorded creator (project_api_keys.created_by_user_id,
	// migration 00091). Two devs running `tavora init` against the
	// same project each get their own deployment because they're each
	// using a key minted via their own `tavora login`. Legacy keys
	// without a recorded owner get a 400 with a "recreate the key"
	// hint — the bindCloudDeployment caller treats it as a soft skip
	// and continues with offline scaffold.
	body := bytes.NewBufferString(`{"kind":"dev"}`)
	req, err := http.NewRequest(http.MethodPost, url+"/api/sdk/deployments", body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("X-API-Key", key)
	req.Header.Set("Content-Type", "application/json")

	// Short timeout — init is interactive and a slow server shouldn't
	// hang the command. If the user is offline they get a quick fail
	// and the offline-only init path still works.
	httpc := &http.Client{Timeout: 10 * time.Second}
	resp, err := httpc.Do(req)
	if err != nil {
		return fmt.Errorf("call %s: %w", req.URL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(b)))
	}

	var got struct {
		Slug string `json:"slug"`
		Kind string `json:"kind"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if got.Slug == "" || got.Kind == "" {
		return fmt.Errorf("server returned empty slug/kind")
	}

	envPath := filepath.Join(root, ".env.local")
	content := fmt.Sprintf(
		`# Auto-written by `+"`tavora init`"+`. Per-developer deployment binding —
# the CLI attaches X-Tavora-Deployment: <value> on every API request.
# Do not commit (already in .gitignore); a teammate cloning the repo
# runs `+"`tavora init`"+` to get their own.
TAVORA_DEPLOYMENT=%s:%s
`, got.Kind, got.Slug)
	if err := os.WriteFile(envPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", envPath, err)
	}
	return nil
}
