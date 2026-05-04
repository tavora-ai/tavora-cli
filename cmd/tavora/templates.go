package main

import (
	"fmt"

	"github.com/spf13/cobra"
	tavora "github.com/tavora-ai/tavora-sdk-go"
)

var templatesCmd = &cobra.Command{
	Use:   "templates",
	Short: "Manage prompt templates",
}

var templatesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all prompt templates",
	RunE: func(cmd *cobra.Command, args []string) error {
		templates, err := client.ListPromptTemplates(cmd.Context())
		if err != nil {
			return err
		}

		if isJSON() {
			return printJSON(templates)
		}

		if len(templates) == 0 {
			fmt.Println("No prompt templates found.")
			return nil
		}

		t := newTable("ID", "NAME", "CREATED")
		for _, tmpl := range templates {
			t.row(tmpl.ID, tmpl.Name, tmpl.CreatedAt.Format("2006-01-02"))
		}
		return t.flush()
	},
}

var (
	tmplCreateName    string
	tmplCreateContent string
)

var templatesCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a prompt template",
	RunE: func(cmd *cobra.Command, args []string) error {
		tmpl, err := client.CreatePromptTemplate(cmd.Context(), tavora.CreatePromptTemplateInput{
			Name:    tmplCreateName,
			Content: tmplCreateContent,
		})
		if err != nil {
			return err
		}

		if isJSON() {
			return printJSON(tmpl)
		}

		fmt.Printf("Created template: %s (%s)\n", tmpl.Name, tmpl.ID)
		return nil
	},
}

var templatesGetCmd = &cobra.Command{
	Use:   "get [id]",
	Short: "Get a prompt template by ID",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		tmpl, err := client.GetPromptTemplate(cmd.Context(), args[0])
		if err != nil {
			return err
		}

		if isJSON() {
			return printJSON(tmpl)
		}

		detail(fmt.Sprintf("Template: %s", tmpl.Name),
			field("ID", tmpl.ID),
			field("Created", tmpl.CreatedAt.Format("2006-01-02 15:04:05")),
		)
		fmt.Printf("\n--- Content ---\n%s\n", tmpl.Content)
		return nil
	},
}

var (
	tmplUpdateName    string
	tmplUpdateContent string
)

var templatesUpdateCmd = &cobra.Command{
	Use:   "update [id]",
	Short: "Update a prompt template",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		tmpl, err := client.UpdatePromptTemplate(cmd.Context(), args[0], tavora.UpdatePromptTemplateInput{
			Name:    tmplUpdateName,
			Content: tmplUpdateContent,
		})
		if err != nil {
			return err
		}

		if isJSON() {
			return printJSON(tmpl)
		}

		fmt.Printf("Updated template: %s (%s)\n", tmpl.Name, tmpl.ID)
		return nil
	},
}

var templatesDeleteCmd = &cobra.Command{
	Use:   "delete [id]",
	Short: "Delete a prompt template by ID",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := client.DeletePromptTemplate(cmd.Context(), args[0]); err != nil {
			return err
		}

		if isJSON() {
			return printJSON(map[string]string{"status": "deleted"})
		}

		fmt.Println("Template deleted.")
		return nil
	},
}

func init() {
	templatesCreateCmd.Flags().StringVar(&tmplCreateName, "name", "", "Template name (required)")
	templatesCreateCmd.Flags().StringVar(&tmplCreateContent, "content", "", "Template content (required)")
	templatesCreateCmd.MarkFlagRequired("name")
	templatesCreateCmd.MarkFlagRequired("content")

	templatesUpdateCmd.Flags().StringVar(&tmplUpdateName, "name", "", "Template name")
	templatesUpdateCmd.Flags().StringVar(&tmplUpdateContent, "content", "", "Template content")

	templatesCmd.AddCommand(templatesListCmd)
	templatesCmd.AddCommand(templatesCreateCmd)
	templatesCmd.AddCommand(templatesGetCmd)
	templatesCmd.AddCommand(templatesUpdateCmd)
	templatesCmd.AddCommand(templatesDeleteCmd)
}
