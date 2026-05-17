package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	tavora "github.com/tavora-ai/tavora-sdk-go"
	"github.com/tavora-ai/tavora-cli/internal/codefirst/runs"
	"github.com/tavora-ai/tavora-cli/internal/codefirst/source"
	"github.com/tavora-ai/tavora-cli/internal/codefirst/validate"
)

// `tavora run <agent> "<input>" --draft` is the AI verification
// inner loop. It:
//
//   1. Loads the local tavora/ folder.
//   2. Validates (fatal issues abort; warnings print and continue).
//   3. Syncs a fresh draft so the run sees the working-tree contents.
//   4. Resolves the local agent id (`support`) to the server agent
//      UUID via the sync result's mapping.
//   5. Creates an agent session with target=draft.
//   6. Streams events to stdout.
//
// .runs/ markdown writing is gap #2 in the deferred list — landing
// it without the runtime-draft path was pointless, but it's the
// natural next step now that drafts run.

var (
	runDraftDir       string
	runDraftLive      bool
	runDraftTitle     string
	runDraftAssetsDir string
)

var codefirstRunCmd = &cobra.Command{
	Use:   "run [agent] [input...]",
	Short: "Run an agent against the current dev draft and stream the trace",
	Long: `tavora run is the AI verification loop. It syncs the working tree as
a fresh dev draft, then invokes the agent with the supplied input and
streams events to stdout.

By default the session targets the dev draft so file edits show up
on the next run without a deploy. Pass --live to run against the
deployed version instead.`,
	Example: `  tavora run support "Can I refund order #12345?"
  tavora run support "ping" --live
  tavora run support "summarize this thread" --output json`,
	Args: cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		localID := args[0]
		input := strings.Join(args[1:], " ")

		if client == nil {
			return fmt.Errorf("no API key configured — run 'tavora login' first")
		}

		p, err := loadProjectOrFail(runDraftDir)
		if err != nil {
			return err
		}
		// Confirm the requested agent exists locally before we touch
		// the network.
		var localAgent *source.Agent
		for _, a := range p.Agents {
			if a.Config.ID == localID {
				localAgent = a
				break
			}
		}
		if localAgent == nil {
			return fmt.Errorf("agent %q not found in project (have: %s)", localID, agentIDs(p))
		}

		// Validate. Fatal issues block the run; warnings already
		// printed by printIssues.
		issues := validate.Project(p)
		printIssues(p, issues)
		if validate.HasFatal(issues) {
			return fmt.Errorf("run refused: %d fatal validation issue(s) — fix before invoking", validate.CountFatal(issues))
		}

		// Sync so the draft on the server matches the working tree.
		// The sync result carries the local→server agent_id mapping
		// we need to call CreateAgentSession.
		manifest := buildManifest(p)
		sdkManifest := toSDKManifest(manifest, p)
		syncResult, err := client.SourceSync(globalCtx(), sdkManifest)
		if err != nil {
			return fmt.Errorf("source-sync failed: %w", err)
		}
		var serverAgentID string
		for _, a := range syncResult.Agents {
			if a.LocalID == localID {
				serverAgentID = a.AgentID
				break
			}
		}
		if serverAgentID == "" {
			return fmt.Errorf("sync did not return a server id for local agent %q", localID)
		}

		target := "draft"
		if runDraftLive {
			target = "live"
		}
		title := runDraftTitle
		if title == "" {
			title = fmt.Sprintf("tavora run %s (%s)", localID, target)
		}

		session, err := client.CreateAgentSession(cmd.Context(), tavora.CreateAgentSessionInput{
			AgentID: serverAgentID,
			Target:  target,
			Title:   title,
		})
		if err != nil {
			return fmt.Errorf("create session: %w", err)
		}
		status("session %s — agent %s, target %s", session.ID, localID, target)

		recorder := runs.New()

		// Resolve the assets-dir: explicit flag wins, otherwise default
		// to <project>/.assets/<session-id>/ so each run keeps its
		// output isolated and the AI coding loop can grep across runs.
		assetsDir := runDraftAssetsDir
		if assetsDir == "" {
			assetsDir = filepath.Join(p.Root, ".assets", session.ID)
		}

		downloadAsset := func(evt tavora.AgentEvent) {
			meta := evt.AsAsset()
			if meta == nil || meta.ID == "" {
				return
			}
			if err := os.MkdirAll(assetsDir, 0o755); err != nil {
				status("asset %s: mkdir failed: %v", meta.Name, err)
				return
			}
			bytes, _, err := client.GetAgentAsset(cmd.Context(), meta.ID)
			if err != nil {
				status("asset %s: download failed: %v", meta.Name, err)
				return
			}
			dest := filepath.Join(assetsDir, meta.Name)
			if err := os.WriteFile(dest, bytes, 0o644); err != nil {
				status("asset %s: write failed: %v", meta.Name, err)
				return
			}
			status("asset → %s (%d bytes, %s)", dest, meta.Size, meta.Mime)
		}

		var handler func(evt tavora.AgentEvent)
		if isJSON() {
			handler = func(evt tavora.AgentEvent) {
				recorder.Handle(evt)
				downloadAsset(evt)
				printJSON(evt) //nolint:errcheck
			}
		} else {
			handler = func(evt tavora.AgentEvent) {
				recorder.Handle(evt)
				downloadAsset(evt)
				printAgentEvent(evt)
			}
		}
		runErr := client.RunAgent(cmd.Context(), session.ID, input, handler)

		// Always write the markdown log, even on failure — the AI
		// loop wants to inspect the trace exactly when something
		// went wrong. The recorder already captured any error event.
		runsDir := filepath.Join(p.Root, ".runs")
		path, writeErr := recorder.Write(runsDir, runs.Meta{
			AgentLocalID: localID,
			AgentID:      serverAgentID,
			SessionID:    session.ID,
			DraftHash:    syncResult.DraftHash,
			Target:       target,
			Input:        input,
		})
		if writeErr != nil {
			status("warning: failed to write session log: %v", writeErr)
		} else {
			status("log: %s", path)
			keep := 50
			if p.Manifest.Retention != nil && p.Manifest.Retention.Runs > 0 {
				keep = p.Manifest.Retention.Runs
			}
			if removed, err := runs.PruneOld(runsDir, keep); err == nil && len(removed) > 0 {
				status("pruned %d old session log(s) (retention=%d)", len(removed), keep)
			}
		}
		return runErr
	},
}

func init() {
	codefirstRunCmd.Flags().StringVar(&runDraftDir, "dir", "", "Project directory containing tavora.jsonc")
	codefirstRunCmd.Flags().BoolVar(&runDraftLive, "live", false, "Run against the deployed version instead of the dev draft")
	codefirstRunCmd.Flags().StringVar(&runDraftTitle, "title", "", "Session title (default: \"tavora run <agent> (<target>)\")")
	codefirstRunCmd.Flags().StringVar(&runDraftAssetsDir, "assets-dir", "", "Where to write agent-generated assets (default: <project>/.assets/<session-id>/)")

	rootCmd.AddCommand(codefirstRunCmd)
}
