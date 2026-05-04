package main

import (
	"fmt"

	"github.com/spf13/cobra"
	tavora "github.com/tavora-ai/tavora-sdk-go"
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Manage MCP server integrations",
}

var mcpListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all MCP servers",
	RunE: func(cmd *cobra.Command, args []string) error {
		servers, err := client.ListMCPServers(cmd.Context())
		if err != nil {
			return err
		}

		if isJSON() {
			return printJSON(servers)
		}

		if len(servers) == 0 {
			fmt.Println("No MCP servers found.")
			return nil
		}

		t := newTable("ID", "NAME", "URL", "TRANSPORT", "ENABLED", "CREATED")
		for _, s := range servers {
			t.row(s.ID, s.Name, s.URL, s.Transport, fmt.Sprintf("%v", s.Enabled), s.CreatedAt.Format("2006-01-02"))
		}
		return t.flush()
	},
}

var (
	mcpCreateName      string
	mcpCreateURL       string
	mcpCreateTransport string
)

var mcpCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create an MCP server",
	RunE: func(cmd *cobra.Command, args []string) error {
		server, err := client.CreateMCPServer(cmd.Context(), tavora.CreateMCPServerInput{
			Name:      mcpCreateName,
			URL:       mcpCreateURL,
			Transport: mcpCreateTransport,
		})
		if err != nil {
			return err
		}

		if isJSON() {
			return printJSON(server)
		}

		fmt.Printf("Created MCP server: %s (%s)\n", server.Name, server.ID)
		return nil
	},
}

var mcpGetCmd = &cobra.Command{
	Use:   "get [id]",
	Short: "Get an MCP server by ID",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		server, err := client.GetMCPServer(cmd.Context(), args[0])
		if err != nil {
			return err
		}

		if isJSON() {
			return printJSON(server)
		}

		detail(fmt.Sprintf("MCP Server: %s", server.Name),
			field("ID", server.ID),
			field("URL", server.URL),
			field("Transport", server.Transport),
			field("Enabled", fmt.Sprintf("%v", server.Enabled)),
			field("Created", server.CreatedAt.Format("2006-01-02 15:04:05")),
		)
		return nil
	},
}

var (
	mcpUpdateName      string
	mcpUpdateURL       string
	mcpUpdateTransport string
)

var mcpUpdateCmd = &cobra.Command{
	Use:   "update [id]",
	Short: "Update an MCP server",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		server, err := client.UpdateMCPServer(cmd.Context(), args[0], tavora.UpdateMCPServerInput{
			Name:      mcpUpdateName,
			URL:       mcpUpdateURL,
			Transport: mcpUpdateTransport,
		})
		if err != nil {
			return err
		}

		if isJSON() {
			return printJSON(server)
		}

		fmt.Printf("Updated MCP server: %s (%s)\n", server.Name, server.ID)
		return nil
	},
}

var mcpDeleteCmd = &cobra.Command{
	Use:   "delete [id]",
	Short: "Delete an MCP server by ID",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := client.DeleteMCPServer(cmd.Context(), args[0]); err != nil {
			return err
		}

		if isJSON() {
			return printJSON(map[string]string{"status": "deleted"})
		}

		fmt.Println("MCP server deleted.")
		return nil
	},
}

var mcpTestCmd = &cobra.Command{
	Use:   "test [id]",
	Short: "Dial the MCP server, list its tools, and materialize the skill row",
	Long: `Test dials the registered MCP server, calls tools/list, and upserts a
type='mcp' skill row capturing the schemas. Use after ` + "`mcp create`" + ` so agent
runs can bind to the captured tools instead of re-handshaking every run. Re-Test
returns a drift diff against the prior snapshot — useful in CI to detect when an
upstream server added or changed tools.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := client.TestMCPServer(cmd.Context(), args[0])
		if err != nil {
			return err
		}

		if isJSON() {
			return printJSON(result)
		}

		fmt.Printf("Test OK: captured %d tool(s)\n", len(result.Tools))
		if result.IsFirstTest {
			fmt.Println("(first test — tool set materialized)")
		}
		if len(result.Tools) > 0 {
			t := newTable("TOOL", "DESCRIPTION")
			for _, tool := range result.Tools {
				desc := tool.Description
				if len(desc) > 60 {
					desc = desc[:57] + "..."
				}
				t.row(tool.Name, desc)
			}
			if err := t.flush(); err != nil {
				return err
			}
		}

		if !result.IsFirstTest {
			hasDrift := len(result.Drift.Added) > 0 ||
				len(result.Drift.Removed) > 0 ||
				len(result.Drift.Changed) > 0
			if hasDrift {
				fmt.Println("\nDrift since last Test:")
				if len(result.Drift.Added) > 0 {
					fmt.Printf("  added:   %v\n", result.Drift.Added)
				}
				if len(result.Drift.Removed) > 0 {
					fmt.Printf("  removed: %v\n", result.Drift.Removed)
				}
				if len(result.Drift.Changed) > 0 {
					for _, ch := range result.Drift.Changed {
						fmt.Printf("  changed: %s (%s)\n", ch.Name, ch.What)
					}
				}
			} else {
				fmt.Println("\nNo drift.")
			}
		}
		return nil
	},
}

func init() {
	mcpCreateCmd.Flags().StringVar(&mcpCreateName, "name", "", "Server name (required)")
	mcpCreateCmd.Flags().StringVar(&mcpCreateURL, "url", "", "Server URL (required)")
	mcpCreateCmd.Flags().StringVar(&mcpCreateTransport, "transport", "sse", "Transport type (sse, stdio)")
	mcpCreateCmd.MarkFlagRequired("name")
	mcpCreateCmd.MarkFlagRequired("url")

	mcpUpdateCmd.Flags().StringVar(&mcpUpdateName, "name", "", "Server name")
	mcpUpdateCmd.Flags().StringVar(&mcpUpdateURL, "url", "", "Server URL")
	mcpUpdateCmd.Flags().StringVar(&mcpUpdateTransport, "transport", "", "Transport type")

	mcpCmd.AddCommand(mcpListCmd)
	mcpCmd.AddCommand(mcpCreateCmd)
	mcpCmd.AddCommand(mcpGetCmd)
	mcpCmd.AddCommand(mcpUpdateCmd)
	mcpCmd.AddCommand(mcpDeleteCmd)
	mcpCmd.AddCommand(mcpTestCmd)
}
