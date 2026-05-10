package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	tavora "github.com/tavora-ai/tavora-sdk-go"
)

// uploadResultMsg is the tea.Msg returned by the upload Cmd. It either
// carries a successfully-uploaded Document or an error explaining why
// the upload didn't go through.
type uploadResultMsg struct {
	doc *tavora.Document
	err error
}

// resolveStoreForUpload returns the store ID the upload should target.
//
// Resolution order:
//   1. If the user typed `/upload <path> <store>`, look it up by ID
//      first, then by name (case-insensitive) within the product's
//      stores list.
//   2. Otherwise, fetch the active version once and cache its
//      stores_json. If exactly one store is bound, use it. If zero or
//      many are bound, return an error pointing the user at the
//      longer form so they can disambiguate.
//
// The "exactly-one" rule is a deliberate UX choice: with multiple
// stores bound, picking one silently would dump the doc into the wrong
// place. Better to make the user say which.
func (m *mainModel) resolveStoreForUpload(ctx context.Context, explicit string) (string, error) {
	if explicit != "" {
		stores, err := m.client.ListIndexes(ctx)
		if err != nil {
			return "", fmt.Errorf("listing stores: %w", err)
		}
		for _, s := range stores {
			if s.ID == explicit {
				return s.ID, nil
			}
		}
		for _, s := range stores {
			if strings.EqualFold(s.Name, explicit) {
				return s.ID, nil
			}
		}
		return "", fmt.Errorf("no store matched %q (try `tavora stores list`)", explicit)
	}

	if m.agent == nil {
		return "", fmt.Errorf("/upload without an explicit store needs an agent — pass `--agent` at startup")
	}

	stores, err := m.agentIndexIDs(ctx)
	if err != nil {
		return "", err
	}
	switch len(stores) {
	case 0:
		return "", fmt.Errorf("agent %q has no stores bound — `/upload <path> <store>` to specify one explicitly", m.agent.Name)
	case 1:
		return stores[0], nil
	default:
		return "", fmt.Errorf("agent %q has %d stores bound — `/upload <path> <store>` to disambiguate", m.agent.Name, len(stores))
	}
}

// agentIndexIDs lazily fetches the active version's stores_json and
// caches the parsed list. The version is immutable, so caching is safe
// for the lifetime of the TUI session.
func (m *mainModel) agentIndexIDs(ctx context.Context) ([]string, error) {
	if m.agentStoresCached {
		return m.agentIndexIDsCache, nil
	}
	if m.agent == nil || m.agent.ActiveVersionID == nil {
		return nil, fmt.Errorf("no active version on agent %q", m.agent.Name)
	}
	version, err := m.client.GetAgentVersion(ctx, m.agent.ID, *m.agent.ActiveVersionID)
	if err != nil {
		return nil, fmt.Errorf("fetching agent version: %w", err)
	}
	var ids []string
	if len(version.StoresJSON) > 0 {
		if err := json.Unmarshal(version.StoresJSON, &ids); err != nil {
			return nil, fmt.Errorf("parsing stores_json: %w", err)
		}
	}
	m.agentIndexIDsCache = ids
	m.agentStoresCached = true
	return ids, nil
}

// uploadCmd performs the actual upload off the UI thread. The result
// comes back as uploadResultMsg and is rendered as a system message.
func uploadCmd(client *tavora.Client, indexID, path string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		doc, err := client.UploadDocument(ctx, tavora.UploadDocumentInput{
			FilePath: path,
			IndexID:  indexID,
		})
		return uploadResultMsg{doc: doc, err: err}
	}
}

// parseUploadArgs splits the /upload command body into (path, storeArg).
// Supports `/upload <path>` and `/upload <path> <store>`. Quoted paths
// (`/upload "foo bar.pdf"`) get the surrounding quotes stripped.
func parseUploadArgs(rest string) (string, string, error) {
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return "", "", fmt.Errorf("usage: /upload <path> [store-id-or-name]")
	}
	var path string
	if strings.HasPrefix(rest, `"`) {
		end := strings.Index(rest[1:], `"`)
		if end < 0 {
			return "", "", fmt.Errorf("unterminated quoted path")
		}
		path = rest[1 : 1+end]
		rest = strings.TrimSpace(rest[1+end+1:])
	} else if i := strings.IndexAny(rest, " \t"); i >= 0 {
		path = rest[:i]
		rest = strings.TrimSpace(rest[i:])
	} else {
		path, rest = rest, ""
	}
	if path == "" {
		return "", "", fmt.Errorf("usage: /upload <path> [store-id-or-name]")
	}
	// Resolve to an absolute path so error messages and audit rows are
	// unambiguous regardless of the user's pwd at TUI launch.
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return path, rest, nil
}
