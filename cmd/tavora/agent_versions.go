package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
	tavora "github.com/tavora-ai/tavora-sdk-go"
)

// `tavora agents versions` and `tavora agents deployments` — the
// CI/CD seam for scripted rollouts. Versions are the immutable
// snapshots of an AgentConfig; deployments pin a version at a target.
// agent-configs CRUD itself stays SDK-only for now — discover the
// agent UUID via the /platform UI or the SDK.

var (
	agentVersionsAgentID string
	agentVersionsLimit   int
)

var agentVersionsCmd = &cobra.Command{
	Use:   "versions",
	Short: "Manage agent versions (immutable snapshots of an agent config)",
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

var (
	versionCreateFrom             string
	versionCreateSemver           string
	versionCreatePersona          string
	versionCreateModel            string
	versionCreateProvider         string
	versionCreateEvalSuiteID      string
	versionCreateEvalSuiteVersion string
	versionCreateSkillsJSON       string
	versionCreateStoresCSV        string
)

var agentVersionsCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new agent version (copy-on-write from another version, or stand-alone)",
	Long: `Creates an immutable agent version. When --from is set, the server copies
that version's fields and overrides with any flags you pass — the common
"bump the prompt, inherit everything else" path. Without --from, a
stand-alone version is created and --model is required.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		input := tavora.CreateAgentVersionInput{
			FromVersionID:    versionCreateFrom,
			Semver:           versionCreateSemver,
			PersonaMD:        versionCreatePersona,
			Model:            versionCreateModel,
			Provider:         versionCreateProvider,
			EvalSuiteID:      versionCreateEvalSuiteID,
			EvalSuiteVersion: versionCreateEvalSuiteVersion,
		}
		if versionCreateSkillsJSON != "" {
			var bindings []tavora.SkillBinding
			if err := json.Unmarshal([]byte(versionCreateSkillsJSON), &bindings); err != nil {
				return fmt.Errorf("--skills must be JSON like [{\"skill_id\":\"...\",\"version\":\"1.0.0\"}]: %w", err)
			}
			input.Skills = bindings
		}
		if versionCreateStoresCSV != "" {
			input.Stores = splitCSV(versionCreateStoresCSV)
		}

		v, err := client.CreateAgentVersion(cmd.Context(), agentVersionsAgentID, input)
		if err != nil {
			return err
		}
		if isJSON() {
			return printJSON(v)
		}
		fmt.Printf("Created version %s (%s)\n", v.Semver, v.ID)
		return nil
	},
}

var agentVersionsSetActiveCmd = &cobra.Command{
	Use:   "set-active [version-id]",
	Short: "Flip the active version of an agent config",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ac, err := client.SetActiveAgentVersion(cmd.Context(), agentVersionsAgentID, args[0])
		if err != nil {
			return err
		}
		if isJSON() {
			return printJSON(ac)
		}
		active := ""
		if ac.ActiveVersionID != nil {
			active = *ac.ActiveVersionID
		}
		fmt.Printf("Active version for %q is now %s\n", ac.Name, active)
		return nil
	},
}

// --- deployments ---

var agentDeploymentsCmd = &cobra.Command{
	Use:   "deployments",
	Short: "Manage agent deployments (version → target bindings)",
}

var agentDeploymentsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List deployments for an agent config",
	RunE: func(cmd *cobra.Command, args []string) error {
		deps, err := client.ListAgentDeployments(cmd.Context(), agentVersionsAgentID)
		if err != nil {
			return err
		}
		if isJSON() {
			return printJSON(deps)
		}
		if len(deps) == 0 {
			fmt.Println("No deployments.")
			return nil
		}
		t := newTable("ID", "VERSION", "TARGET", "STATUS", "DEPLOYED_AT")
		for _, d := range deps {
			target := d.TargetType
			if d.TargetRef != "" {
				target = d.TargetType + ":" + d.TargetRef
			}
			t.row(d.ID, d.VersionID, target, d.Status, d.DeployedAt.Format("2006-01-02 15:04"))
		}
		return t.flush()
	},
}

var (
	deployVersionID  string
	deployTargetType string
	deployTargetRef  string
)

var agentDeploymentsUpsertCmd = &cobra.Command{
	Use:   "upsert",
	Short: "Pin a version at a target (idempotent)",
	RunE: func(cmd *cobra.Command, args []string) error {
		dep, err := client.UpsertAgentDeployment(cmd.Context(), agentVersionsAgentID, tavora.UpsertDeploymentInput{
			VersionID:  deployVersionID,
			TargetType: deployTargetType,
			TargetRef:  deployTargetRef,
		})
		if err != nil {
			return err
		}
		if isJSON() {
			return printJSON(dep)
		}
		target := dep.TargetType
		if dep.TargetRef != "" {
			target = dep.TargetType + ":" + dep.TargetRef
		}
		fmt.Printf("Deployed version %s at %s (status: %s)\n", dep.VersionID, target, dep.Status)
		return nil
	},
}

func splitCSV(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func init() {
	// Shared agent-config id flag on all four commands.
	for _, c := range []*cobra.Command{agentVersionsCmd, agentDeploymentsCmd} {
		c.PersistentFlags().StringVar(&agentVersionsAgentID, "agent", "", "Agent config UUID (required)")
		_ = c.MarkPersistentFlagRequired("agent")
	}

	agentVersionsListCmd.Flags().IntVar(&agentVersionsLimit, "limit", 50, "Max versions to return")

	agentVersionsCreateCmd.Flags().StringVar(&versionCreateFrom, "from", "", "Source version to copy-on-write from")
	agentVersionsCreateCmd.Flags().StringVar(&versionCreateSemver, "semver", "", "Semver (auto-bump if empty)")
	agentVersionsCreateCmd.Flags().StringVar(&versionCreatePersona, "persona", "", "Persona markdown (system prompt)")
	agentVersionsCreateCmd.Flags().StringVar(&versionCreateModel, "model", "", "Model name (required without --from)")
	agentVersionsCreateCmd.Flags().StringVar(&versionCreateProvider, "provider", "", "Provider (gemini default)")
	agentVersionsCreateCmd.Flags().StringVar(&versionCreateEvalSuiteID, "eval-suite", "", "Eval suite UUID to gate this version")
	agentVersionsCreateCmd.Flags().StringVar(&versionCreateEvalSuiteVersion, "eval-suite-version", "", "Eval suite version ID to pin to")
	agentVersionsCreateCmd.Flags().StringVar(&versionCreateSkillsJSON, "skills", "", `JSON array of {skill_id, version} bindings`)
	agentVersionsCreateCmd.Flags().StringVar(&versionCreateStoresCSV, "stores", "", "Comma-separated knowledge store UUIDs")

	agentDeploymentsUpsertCmd.Flags().StringVar(&deployVersionID, "version-id", "", "Version UUID to pin (required)")
	agentDeploymentsUpsertCmd.Flags().StringVar(&deployTargetType, "target-type", "api", "Target kind (api, channel_binding, …)")
	agentDeploymentsUpsertCmd.Flags().StringVar(&deployTargetRef, "target-ref", "", "Target-specific identifier")
	agentDeploymentsUpsertCmd.MarkFlagRequired("version-id")

	agentVersionsCmd.AddCommand(agentVersionsListCmd)
	agentVersionsCmd.AddCommand(agentVersionsGetCmd)
	agentVersionsCmd.AddCommand(agentVersionsCreateCmd)
	agentVersionsCmd.AddCommand(agentVersionsSetActiveCmd)

	agentDeploymentsCmd.AddCommand(agentDeploymentsListCmd)
	agentDeploymentsCmd.AddCommand(agentDeploymentsUpsertCmd)

	agentsCmd.AddCommand(agentVersionsCmd)
	agentsCmd.AddCommand(agentDeploymentsCmd)
}
