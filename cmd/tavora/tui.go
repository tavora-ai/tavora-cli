package main

import (
	"github.com/spf13/cobra"

	"github.com/tavora-ai/tavora-cli/internal/tui"
)

// `tavora tui` is the agent TUI — interactive chat-with-an-agent
// surface backed by internal/tui.Run. A single install
// (`go install ./cmd/tavora`) covers the whole workflow: edit code,
// `tavora dev` to sync, `tavora tui` to chat with the draft,
// `tavora deploy` to publish. The previous standalone `tavora-tui`
// binary was retired 2026-05-17.
//
// Folder-aware by default: when cwd has a tavora.jsonc up the
// tree, the TUI reads ~/.tavora.yaml (so `tavora login` covers
// both the CLI and the TUI), syncs the folder, scopes the agent
// picker to local agents, defaults the session target to the dev
// draft, and downloads emitted assets into <project>/.assets/.

var (
	tuiAgent    string
	tuiSession  string
	tuiResetCfg bool
	tuiDir      string
	tuiNoFolder bool
)

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Open the agent chat TUI",
	Long: `tavora tui opens the interactive terminal UI for chatting with an
agent.

When run from inside a tavora/ folder, the TUI auto-detects the
project, syncs once on startup, scopes the agent picker to local
agents, defaults to the dev draft target, and downloads emitted
assets into <project>/.assets/<session-id>/.

Pass --no-folder to disable folder mode and use the legacy
cross-project picker with the live target.`,
	Example: `  tavora tui                      # auto-detects folder, picks the agent
  tavora tui --agent copilot      # bind to a specific local agent
  tavora tui --session <id>       # resume a prior session
  tavora tui --no-folder          # cross-project mode`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return tui.Run(tui.Options{
			AgentFlag:   tuiAgent,
			SessionFlag: tuiSession,
			ResetConfig: tuiResetCfg,
			ProjectDir:  tuiDir,
			NoFolder:    tuiNoFolder,
		})
	},
}

func init() {
	tuiCmd.Flags().StringVar(&tuiAgent, "agent", "", "Agent local-id (or UUID/name) to bind to")
	tuiCmd.Flags().StringVar(&tuiSession, "session", "", "Resume a prior server-side session by ID")
	tuiCmd.Flags().BoolVar(&tuiResetCfg, "reset-config", false, "Ignore stored config and rerun setup")
	tuiCmd.Flags().StringVar(&tuiDir, "dir", "", "tavora/ folder root (default: walk up from cwd)")
	tuiCmd.Flags().BoolVar(&tuiNoFolder, "no-folder", false, "Disable folder auto-detection (legacy cross-project mode)")
	rootCmd.AddCommand(tuiCmd)
}
