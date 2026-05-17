package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var appCmd = &cobra.Command{
	Use:   "project",
	Short: "Project operations",
}

var appShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show the current project",
	RunE: func(cmd *cobra.Command, args []string) error {
		project, err := client.GetProject(cmd.Context())
		if err != nil {
			return err
		}

		if isJSON() {
			return printJSON(project)
		}

		detail(fmt.Sprintf("Project: %s", project.Name),
			field("ID", project.ID),
			field("Slug", project.Slug),
			field("Description", project.Description),
			field("Created", project.CreatedAt.Format("2006-01-02 15:04:05")),
		)
		return nil
	},
}

var appSeedCmd = &cobra.Command{
	Use:   "seed",
	Short: "Ensure the project has the platform-invariant default agent (idempotent)",
	Long: `Projects created via signup get a default agent + version + eval suite
auto-provisioned. Projects created via SDK or admin paths sometimes don't
(historical gap). This command runs the same SeedStarter that signup runs,
idempotently — if any agent already exists, it reports already_seeded and
mutates nothing.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		res, err := client.SeedProject(cmd.Context())
		if err != nil {
			return err
		}
		if isJSON() {
			return printJSON(res)
		}
		if res.AlreadySeeded {
			fmt.Printf("Project already has agent %q (%s); no changes made.\n", res.AgentName, res.AgentID)
		} else {
			fmt.Printf("Seeded project with default agent %q (%s).\n", res.AgentName, res.AgentID)
		}
		return nil
	},
}

func init() {
	appCmd.AddCommand(appShowCmd)
	appCmd.AddCommand(appSeedCmd)
}
