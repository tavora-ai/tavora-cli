package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	tavora "github.com/tavora-ai/tavora-sdk-go"
)

var skillsCmd = &cobra.Command{
	Use:   "skills",
	Short: "Manage custom skills (tools)",
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
			fmt.Println("No skills found.")
			return nil
		}

		t := newTable("ID", "NAME", "TYPE", "ENABLED", "CREATED")
		for _, s := range skills {
			t.row(s.ID, s.Name, s.Type, fmt.Sprintf("%v", s.Enabled), s.CreatedAt.Format("2006-01-02"))
		}
		return t.flush()
	},
}

var (
	skillCreateName     string
	skillCreateDesc     string
	skillCreateType     string
	skillCreateProm     string
	skillCreateFromFile string
)

var skillsCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a skill (use --from-file <path.js> to upload a module skill)",
	RunE: func(cmd *cobra.Command, args []string) error {
		name := skillCreateName
		prompt := skillCreateProm
		typ := skillCreateType

		if skillCreateFromFile != "" {
			body, err := os.ReadFile(skillCreateFromFile)
			if err != nil {
				return fmt.Errorf("read --from-file %q: %w", skillCreateFromFile, err)
			}
			prompt = string(body)
			// --from-file defaults to a module skill (the JS-source-becomes-
			// require()-able-function shape). Caller can override with --type.
			if !cmd.Flags().Changed("type") {
				typ = "module"
			}
			if name == "" {
				base := filepath.Base(skillCreateFromFile)
				base = strings.TrimSuffix(base, filepath.Ext(base))
				name = base
			}
		}

		if name == "" {
			return fmt.Errorf("--name is required (or supply --from-file to derive it)")
		}

		skill, err := client.CreateSkill(cmd.Context(), tavora.CreateSkillInput{
			Name:        name,
			Description: skillCreateDesc,
			Type:        typ,
			Prompt:      prompt,
		})
		if err != nil {
			return err
		}

		if isJSON() {
			return printJSON(skill)
		}

		fmt.Printf("Created skill: %s (%s)\n", skill.Name, skill.ID)
		return nil
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

var skillsDeleteCmd = &cobra.Command{
	Use:   "delete [id]",
	Short: "Delete a skill by ID",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := client.DeleteSkill(cmd.Context(), args[0]); err != nil {
			return err
		}

		if isJSON() {
			return printJSON(map[string]string{"status": "deleted"})
		}

		fmt.Println("Skill deleted.")
		return nil
	},
}

func init() {
	skillsCreateCmd.Flags().StringVar(&skillCreateName, "name", "", "Skill name (defaults to filename when --from-file is set)")
	skillsCreateCmd.Flags().StringVar(&skillCreateDesc, "description", "", "Skill description")
	skillsCreateCmd.Flags().StringVar(&skillCreateType, "type", "prompt", "Skill type (prompt, webhook, module)")
	skillsCreateCmd.Flags().StringVar(&skillCreateProm, "prompt", "", "Skill prompt template (or JS source for module skills)")
	skillsCreateCmd.Flags().StringVar(&skillCreateFromFile, "from-file", "", "Upload a JS module skill from a file path. Defaults --type to 'module' and --name to the basename.")

	skillsAuthoringGuideCmd.Flags().StringVarP(&skillAuthoringGuideOut, "output", "o", "", "Write the guide to this file instead of stdout")

	skillsCmd.AddCommand(skillsListCmd)
	skillsCmd.AddCommand(skillsCreateCmd)
	skillsCmd.AddCommand(skillsGetCmd)
	skillsCmd.AddCommand(skillsDeleteCmd)
	skillsCmd.AddCommand(skillsAuthoringGuideCmd)
}
