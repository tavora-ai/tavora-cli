package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var workspaceCmd = &cobra.Command{
	Use:   "workspace",
	Short: "Workspace operations",
}

var workspaceShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show the current workspace",
	RunE: func(cmd *cobra.Command, args []string) error {
		space, err := client.GetWorkspace(cmd.Context())
		if err != nil {
			return err
		}

		if isJSON() {
			return printJSON(space)
		}

		detail(fmt.Sprintf("Workspace: %s", space.Name),
			field("ID", space.ID),
			field("Slug", space.Slug),
			field("Description", space.Description),
			field("Created", space.CreatedAt.Format("2006-01-02 15:04:05")),
		)
		return nil
	},
}

var workspaceSeedCmd = &cobra.Command{
	Use:   "seed",
	Short: "Ensure the workspace has the platform-invariant default agent (idempotent)",
	Long: `Workspaces created via signup get a default agent + version + eval suite
auto-provisioned. Workspaces created via SDK or admin paths sometimes don't
(historical gap). This command runs the same SeedStarter that signup runs,
idempotently — if any agent already exists, it reports already_seeded and
mutates nothing.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		res, err := client.SeedWorkspace(cmd.Context())
		if err != nil {
			return err
		}
		if isJSON() {
			return printJSON(res)
		}
		if res.AlreadySeeded {
			fmt.Printf("Workspace already has agent %q (%s); no changes made.\n", res.AgentName, res.AgentID)
		} else {
			fmt.Printf("Seeded workspace with default agent %q (%s).\n", res.AgentName, res.AgentID)
		}
		return nil
	},
}

func init() {
	workspaceCmd.AddCommand(workspaceShowCmd)
	workspaceCmd.AddCommand(workspaceSeedCmd)
}
