package tui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tavora "github.com/tavora-ai/tavora-sdk-go"
	"github.com/tavora-ai/tavora-cli/internal/codefirst/source"
	"gopkg.in/yaml.v3"
)

// folderContext is what the TUI knows about the user's local
// tavora/ folder. Nil when --no-folder is set or there's no
// tavora.jsonc up the tree from cwd. Carries the resolved project
// plus a map of local-id → canonical source-hash so PreSync can be
// idempotent across launches.
//
// The struct deliberately doesn't hold a tavora.Client — the TUI
// constructs the client after acquireConfig, then hands it to
// PreSync. Keeps folderContext usable in tests that don't touch
// the network.
type folderContext struct {
	Root      string          // absolute path to the tavora/ folder
	Manifest  source.Manifest // parsed tavora.jsonc
	Agents    []*source.Agent // resolved local agents
	LocalIDs  []string        // sorted; useful for the picker
	syncHash  string          // last successfully synced source hash
	syncIDMap map[string]string
}

// detectFolder walks up from explicitDir (or cwd when empty)
// looking for tavora.jsonc. Returns nil when no folder is found,
// when source.Load fails, or when the folder has zero agents.
//
// Errors are logged but not returned — folder mode is opt-in by
// presence, and a malformed folder shouldn't block the legacy
// cross-project TUI from launching. The user can re-run with
// --no-folder to silence the noise.
func detectFolder(explicitDir string) *folderContext {
	start := explicitDir
	if start == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil
		}
		start = cwd
	}
	root := findTavoraRoot(start)
	if root == "" {
		return nil
	}
	p, err := source.Load(root)
	if err != nil {
		slog.Warn("folder detected but source.Load failed", "root", root, "err", err)
		return nil
	}
	if len(p.Agents) == 0 {
		slog.Warn("folder has no agents", "root", root)
		return nil
	}
	ids := make([]string, 0, len(p.Agents))
	for _, a := range p.Agents {
		ids = append(ids, a.Config.ID)
	}
	sort.Strings(ids)
	return &folderContext{
		Root:      p.Root,
		Manifest:  p.Manifest,
		Agents:    p.Agents,
		LocalIDs:  ids,
		syncIDMap: map[string]string{},
	}
}

// findTavoraRoot walks upward from start until it finds a directory
// containing tavora.jsonc, then returns that directory. Returns ""
// when the walk hits the filesystem root without finding it.
func findTavoraRoot(start string) string {
	dir, err := filepath.Abs(start)
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "tavora.jsonc")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// PreSync pushes the folder's current state to the server before
// the chat surface opens. Idempotent against the source hash so a
// re-launch with no edits is essentially a no-op on the wire.
// Returns the server-resolved (local-id → agent-uuid) map; the
// agent picker uses it to scope itself.
func (f *folderContext) PreSync(ctx context.Context, client *tavora.Client) error {
	manifest, content := f.buildManifest()
	if manifest.SourceHash == f.syncHash && len(f.syncIDMap) > 0 {
		return nil // already synced this session
	}
	res, err := client.SourceSync(ctx, tavora.SourceSyncManifest{
		Project:     manifest.Project,
		SourceHash:  manifest.SourceHash,
		GeneratedAt: time.Now().UTC(),
		Agents:      manifest.Agents,
	})
	_ = content // reserved for content-addressed uploads
	if err != nil {
		return err
	}
	f.syncHash = manifest.SourceHash
	for _, a := range res.Agents {
		f.syncIDMap[a.LocalID] = a.AgentID
	}
	return nil
}

// AgentUUIDFor returns the server-resolved UUID for a local-id, or
// "" if PreSync hasn't seen it yet.
func (f *folderContext) AgentUUIDFor(localID string) string {
	return f.syncIDMap[localID]
}

// LocalIDForUUID is the inverse of AgentUUIDFor — handy when the
// picker shows server agents and we want to mark which ones belong
// to the folder. Linear scan because the map is tiny.
func (f *folderContext) LocalIDForUUID(agentID string) string {
	for local, id := range f.syncIDMap {
		if id == agentID {
			return local
		}
	}
	return ""
}

// AssetDir returns where this folder's TUI sessions should drop
// downloaded assets: <root>/.assets/<session-id>/. Caller is
// responsible for MkdirAll'ing it; we just compute the path.
func (f *folderContext) AssetDir(sessionID string) string {
	return filepath.Join(f.Root, ".assets", sessionID)
}

// buildManifest is the lightweight equivalent of the CLI's
// buildManifest in cmd/tavora/codefirst.go — packs every loaded
// source file with sha256 hashes so SourceSync can dedupe.
// Returns (manifest, files-content-map) so future content-
// addressed upload can stream only missing files.
func (f *folderContext) buildManifest() (manifestSnapshot, map[string][]byte) {
	bytesByPath := map[string][]byte{}
	projectHasher := sha256.New()
	var manifestAgents []tavora.SourceAgent
	for _, a := range f.Agents {
		agentHasher := sha256.New()
		var files []tavora.SourceFile
		paths := make([]string, 0, len(a.SourceBytes))
		for k := range a.SourceBytes {
			paths = append(paths, k)
		}
		sort.Strings(paths)
		for _, k := range paths {
			b := a.SourceBytes[k]
			bytesByPath[k] = b
			h := sha256.Sum256(b)
			files = append(files, tavora.SourceFile{
				Path:    k,
				Hash:    "sha256:" + hex.EncodeToString(h[:]),
				Size:    len(b),
				Content: b,
			})
			agentHasher.Write([]byte(k))
			agentHasher.Write(b)
		}
		agentHash := "sha256:" + hex.EncodeToString(agentHasher.Sum(nil))
		manifestAgents = append(manifestAgents, tavora.SourceAgent{
			ID:         a.Config.ID,
			SourceHash: agentHash,
			Files:      files,
		})
		projectHasher.Write([]byte(a.Config.ID))
		projectHasher.Write([]byte(agentHash))
	}
	return manifestSnapshot{
		Project:    f.Manifest.Project,
		SourceHash: "sha256:" + hex.EncodeToString(projectHasher.Sum(nil)),
		Agents:     manifestAgents,
	}, bytesByPath
}

// manifestSnapshot is just enough of cmd/tavora's SyncManifest to
// feed tavora.SourceSyncManifest. Kept private to internal/tui so
// the TUI doesn't take a dependency on cmd/tavora.
type manifestSnapshot struct {
	Project    string
	SourceHash string
	Agents     []tavora.SourceAgent
}

// loadCLIConfig reads ~/.tavora.yaml — the file `tavora login`
// writes. Returns nil when the file doesn't exist or doesn't
// parse, so callers can fall through to the next credential
// source without special-casing errors.
func loadCLIConfig() *Config {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	path := filepath.Join(home, ".tavora.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var raw struct {
		APIKey string `yaml:"api_key"`
		URL    string `yaml:"url"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil
	}
	if strings.TrimSpace(raw.APIKey) == "" || strings.TrimSpace(raw.URL) == "" {
		return nil
	}
	return &Config{URL: raw.URL, APIKey: raw.APIKey}
}

// describe returns a one-line summary suitable for the chat
// header. Falls back to the manifest project name when there's no
// resolved sync map yet.
func (f *folderContext) describe() string {
	if f == nil {
		return ""
	}
	n := len(f.LocalIDs)
	switch n {
	case 1:
		return fmt.Sprintf("folder %s — agent %s", f.Manifest.Project, f.LocalIDs[0])
	default:
		return fmt.Sprintf("folder %s — %d agents", f.Manifest.Project, n)
	}
}
