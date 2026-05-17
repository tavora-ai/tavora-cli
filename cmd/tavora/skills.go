package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// tavora skills — inspection only. Authoring lives in
// tavora/agents/<id>/skills/<name>.js (plus the matching .md prompt)
// and rolls in via `tavora dev`. The previous `create` and `delete`
// subcommands were removed 2026-05-17 alongside the SDK skill-write
// surface; the skills table now has one writer (source-sync).
var skillsCmd = &cobra.Command{
	Use:   "skills",
	Short: "Inspect skills deployed to the current app",
}

var skillsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all skills",
	RunE: func(cmd *cobra.Command, args []string) error {
		skills, err := client.ListSkills(cmd.Context())
		if err != nil {
			return err
		}

		if isJSON() {
			return printJSON(skills)
		}

		if len(skills) == 0 {
			fmt.Println("No skills found. Add tavora/agents/<id>/skills/<name>.js then run `tavora dev`.")
			return nil
		}

		t := newTable("ID", "NAME", "TYPE", "ENABLED", "CREATED")
		for _, s := range skills {
			t.row(s.ID, s.Name, s.Type, fmt.Sprintf("%v", s.Enabled), s.CreatedAt.Format("2006-01-02"))
		}
		return t.flush()
	},
}

var skillAuthoringGuideOut string

var skillsAuthoringGuideCmd = &cobra.Command{
	Use:   "authoring-guide",
	Short: "Print the canonical skill-authoring guide (Markdown). Hand to an LLM as context for writing skills.",
	RunE: func(cmd *cobra.Command, args []string) error {
		guide, err := client.GetSkillAuthoringGuide(cmd.Context())
		if err != nil {
			return err
		}
		if skillAuthoringGuideOut == "" {
			fmt.Print(guide)
			return nil
		}
		if err := os.WriteFile(skillAuthoringGuideOut, []byte(guide), 0o644); err != nil {
			return fmt.Errorf("write %q: %w", skillAuthoringGuideOut, err)
		}
		fmt.Fprintf(os.Stderr, "Wrote authoring guide to %s\n", skillAuthoringGuideOut)
		return nil
	},
}

var skillsGetCmd = &cobra.Command{
	Use:   "get [id]",
	Short: "Get a skill by ID",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		skill, err := client.GetSkill(cmd.Context(), args[0])
		if err != nil {
			return err
		}

		if isJSON() {
			return printJSON(skill)
		}

		detail(fmt.Sprintf("Skill: %s", skill.Name),
			field("ID", skill.ID),
			field("Type", skill.Type),
			field("Description", skill.Description),
			field("Enabled", fmt.Sprintf("%v", skill.Enabled)),
			field("Created", skill.CreatedAt.Format("2006-01-02 15:04:05")),
		)
		if skill.Prompt != "" {
			fmt.Printf("\n--- Prompt ---\n%s\n", skill.Prompt)
		}
		return nil
	},
}

func init() {
	skillsAuthoringGuideCmd.Flags().StringVarP(&skillAuthoringGuideOut, "output", "o", "", "Write the guide to this file instead of stdout")

	skillsCmd.AddCommand(skillsListCmd)
	skillsCmd.AddCommand(skillsGetCmd)
	skillsCmd.AddCommand(skillsAuthoringGuideCmd)
}
