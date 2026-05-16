package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	tavora "github.com/tavora-ai/tavora-sdk-go"
)

// `tavora rename` and `tavora delete` are the bookkeeping verbs the
// concept doc (docs/code-first-agents-concept.md §"Design Decisions"
// → "Mapping from local id to server agent_id") promises. The
// server keys on `(project, local_id) → agent_id`; without these
// verbs, a bare edit to agent.jsonc:id or a folder deletion looks
// like delete+create on the next sync, fragmenting state.

var (
	renameDir     string
	renameProject string
)

var codefirstRenameCmd = &cobra.Command{
	Use:   "rename <old-id> <new-id>",
	Short: "Tell the server you're renaming an agent's local id (preserves the binding)",
	Long: `tavora rename preserves the server-side mapping when you change an
agent's id field. Run it BEFORE editing agent.jsonc to "new-id" —
the server updates its (project, local_id) → agent_id record, then
your next 'tavora dev' sync continues against the same agent row.

Without this verb, the server treats the renamed id as a brand-new
agent — sessions, audit history, and published versions stop
following the file you're editing.`,
	Example: `  tavora rename support triage
  # then edit agents/support/agent.jsonc — change "id": "support" to "id": "triage"
  # (optionally rename the folder too; the binding is in the id, not the path)`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		oldID, newID := args[0], args[1]
		if oldID == newID {
			return fmt.Errorf("old and new ids are identical — nothing to do")
		}
		project, err := resolveProjectName(renameDir, renameProject)
		if err != nil {
			return err
		}
		if client == nil {
			return fmt.Errorf("no API key configured — run 'tavora login' first")
		}

		result, err := client.SourceRename(cmd.Context(), tavora.SourceRenameInput{
			Project:    project,
			OldLocalID: oldID,
			NewLocalID: newID,
		})
		if err != nil {
			return err
		}
		if isJSON() {
			return printJSON(result)
		}
		fmt.Printf("Renamed %q → %q (agent %s)\n", oldID, newID, result.AgentID)
		fmt.Println()
		fmt.Println("Next step:")
		fmt.Printf("  Edit agents/<folder>/agent.jsonc — set \"id\": %q\n", newID)
		fmt.Println("  Then run 'tavora dev' to confirm the sync round-trips.")
		return nil
	},
}

var (
	deleteDir     string
	deleteProject string
	deleteForce   bool
	deleteYes     bool
)

var codefirstDeleteCmd = &cobra.Command{
	Use:   "delete <id>",
	Short: "Destroy a source-managed agent on the server (cascades to versions, sessions, evals)",
	Long: `tavora delete drops the server agent identified by the local id,
along with every row that depends on it (agent_versions, sessions,
eval runs, audit pointers). Irreversible.

Use this when you've removed an agent folder from the project and
want the server side to disappear too. Deleting a folder alone does
NOT auto-delete the server agent — that's by design, since folder
deletion is too easy to do by accident.

Requires explicit confirmation: either --yes on the command line, or
an interactive 'yes' at the prompt.`,
	Example: `  tavora delete legacy-bot --yes
  tavora delete experimental         # interactive prompt`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		localID := args[0]
		project, err := resolveProjectName(deleteDir, deleteProject)
		if err != nil {
			return err
		}
		if client == nil {
			return fmt.Errorf("no API key configured — run 'tavora login' first")
		}
		if !deleteYes {
			fmt.Fprintf(os.Stderr,
				"This will permanently delete agent %q in project %q,\n"+
					"including every version, every session, and every eval run.\n"+
					"Type 'yes' to confirm: ", localID, project)
			reader := bufio.NewReader(os.Stdin)
			line, _ := reader.ReadString('\n')
			if strings.TrimSpace(line) != "yes" {
				return fmt.Errorf("delete aborted")
			}
		}

		result, err := client.SourceDelete(cmd.Context(), tavora.SourceDeleteInput{
			Project: project,
			LocalID: localID,
			Force:   true,
		})
		if err != nil {
			return err
		}
		if isJSON() {
			return printJSON(result)
		}
		fmt.Printf("Deleted agent %q (server id %s)\n", result.LocalID, result.AgentID)
		return nil
	},
}

// resolveProjectName returns the project string for a code-first
// command. Order of precedence:
//   1. --project flag (when set)
//   2. project field of the discovered tavora.jsonc
//
// Walks up from --dir / cwd to find tavora.jsonc the same way every
// other code-first verb does.
func resolveProjectName(dirOverride, projectOverride string) (string, error) {
	if projectOverride != "" {
		return projectOverride, nil
	}
	p, err := loadProjectOrFail(dirOverride)
	if err != nil {
		return "", fmt.Errorf("could not determine project (no tavora.jsonc): %w\n  hint: pass --project explicitly", err)
	}
	if p.Manifest.Project == "" {
		return "", fmt.Errorf(`tavora.jsonc has no "project" field — pass --project explicitly`)
	}
	return p.Manifest.Project, nil
}

func init() {
	codefirstRenameCmd.Flags().StringVar(&renameDir, "dir", "", "Project directory containing tavora.jsonc")
	codefirstRenameCmd.Flags().StringVar(&renameProject, "project", "", "Override the project name (default: read from tavora.jsonc)")

	codefirstDeleteCmd.Flags().StringVar(&deleteDir, "dir", "", "Project directory containing tavora.jsonc")
	codefirstDeleteCmd.Flags().StringVar(&deleteProject, "project", "", "Override the project name (default: read from tavora.jsonc)")
	codefirstDeleteCmd.Flags().BoolVar(&deleteForce, "force", false, "Deprecated alias for --yes — kept so old scripts don't break")
	codefirstDeleteCmd.Flags().BoolVarP(&deleteYes, "yes", "y", false, "Skip the interactive confirmation prompt")

	rootCmd.AddCommand(codefirstRenameCmd)
	rootCmd.AddCommand(codefirstDeleteCmd)
}
