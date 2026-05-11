package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var appCmd = &cobra.Command{
	Use:   "app",
	Short: "App operations",
}

var appShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show the current app",
	RunE: func(cmd *cobra.Command, args []string) error {
		app, err := client.GetApp(cmd.Context())
		if err != nil {
			return err
		}

		if isJSON() {
			return printJSON(app)
		}

		detail(fmt.Sprintf("App: %s", app.Name),
			field("ID", app.ID),
			field("Slug", app.Slug),
			field("Description", app.Description),
			field("Created", app.CreatedAt.Format("2006-01-02 15:04:05")),
		)
		return nil
	},
}

var appSeedCmd = &cobra.Command{
	Use:   "seed",
	Short: "Ensure the app has the platform-invariant default agent (idempotent)",
	Long: `Apps created via signup get a default agent + version + eval suite
auto-provisioned. Apps created via SDK or admin paths sometimes don't
(historical gap). This command runs the same SeedStarter that signup runs,
idempotently — if any agent already exists, it reports already_seeded and
mutates nothing.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		res, err := client.SeedApp(cmd.Context())
		if err != nil {
			return err
		}
		if isJSON() {
			return printJSON(res)
		}
		if res.AlreadySeeded {
			fmt.Printf("App already has agent %q (%s); no changes made.\n", res.AgentName, res.AgentID)
		} else {
			fmt.Printf("Seeded app with default agent %q (%s).\n", res.AgentName, res.AgentID)
		}
		return nil
	},
}

func init() {
	appCmd.AddCommand(appShowCmd)
	appCmd.AddCommand(appSeedCmd)
}
