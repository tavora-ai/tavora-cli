package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// `tavora agents versions` is the version-history surface. Listing
// and reading agent versions is a script-friendly way to inspect
// what's been published. Direct version creation and set-active
// were removed when code-first took over — promote a draft via
// `tavora deploy` (CLI) or the publish flow (UI); both append a
// kind='published' row through the same internal path.

var (
	agentVersionsAgentID string
	agentVersionsLimit   int
)

var agentVersionsCmd = &cobra.Command{
	Use:   "versions",
	Short: "Inspect agent version history (read-only)",
	Long: `agent versions are immutable snapshots of an agent config, appended
on every publish. This command lists and reads them. To create new
versions, edit the local tavora/ folder and run 'tavora deploy', or
use the Publish button in the UI.`,
}

var agentVersionsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List versions of an agent config",
	RunE: func(cmd *cobra.Command, args []string) error {
		versions, err := client.ListAgentVersions(cmd.Context(), agentVersionsAgentID)
		if err != nil {
			return err
		}
		if isJSON() {
			return printJSON(versions)
		}
		if len(versions) == 0 {
			fmt.Println("No versions found.")
			return nil
		}
		t := newTable("ID", "SEMVER", "MODEL", "CREATED_BY", "CREATED_AT")
		for _, v := range versions {
			t.row(v.ID, v.Semver, v.Model, v.CreatedBy, v.CreatedAt.Format("2006-01-02 15:04"))
		}
		return t.flush()
	},
}

var agentVersionsGetCmd = &cobra.Command{
	Use:   "get [version-id]",
	Short: "Get an agent version",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := client.GetAgentVersion(cmd.Context(), agentVersionsAgentID, args[0])
		if err != nil {
			return err
		}
		if isJSON() {
			return printJSON(v)
		}
		fields := []kv{
			field("ID", v.ID),
			field("Agent", v.AgentID),
			field("Semver", v.Semver),
			field("Provider", v.Provider),
			field("Model", v.Model),
			field("Created by", v.CreatedBy),
			field("Created at", v.CreatedAt.Format("2006-01-02 15:04:05")),
		}
		if v.EvalSuiteID != nil {
			fields = append(fields, field("Eval suite", *v.EvalSuiteID))
		}
		if v.EvalSuiteVersion != nil {
			fields = append(fields, field("Eval suite version", *v.EvalSuiteVersion))
		}
		if len(v.SkillsJSON) > 0 {
			fields = append(fields, field("Skills", string(v.SkillsJSON)))
		}
		if len(v.StoresJSON) > 0 {
			fields = append(fields, field("Stores", string(v.StoresJSON)))
		}
		detail("Agent Version", fields...)
		if v.PersonaMD != "" {
			fmt.Println("\n--- Persona ---")
			fmt.Println(v.PersonaMD)
		}
		return nil
	},
}

func init() {
	agentVersionsCmd.PersistentFlags().StringVar(&agentVersionsAgentID, "agent", "", "Agent config UUID (required)")
	_ = agentVersionsCmd.MarkPersistentFlagRequired("agent")

	agentVersionsListCmd.Flags().IntVar(&agentVersionsLimit, "limit", 50, "Max versions to return")

	agentVersionsCmd.AddCommand(agentVersionsListCmd)
	agentVersionsCmd.AddCommand(agentVersionsGetCmd)

	agentsCmd.AddCommand(agentVersionsCmd)
}
