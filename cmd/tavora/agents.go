package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	tavora "github.com/tavora-ai/tavora-sdk-go"
)

var agentsCmd = &cobra.Command{
	Use:   "agents",
	Short: "Manage agent sessions",
}

var (
	agentsListLimit int
)

var agentsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List agent sessions",
	RunE: func(cmd *cobra.Command, args []string) error {
		sessions, err := client.ListAgentSessions(cmd.Context(), agentsListLimit, 0)
		if err != nil {
			return err
		}

		if isJSON() {
			return printJSON(sessions)
		}

		if len(sessions) == 0 {
			fmt.Println("No agent sessions found.")
			return nil
		}

		t := newTable("ID", "TITLE", "STATUS", "MODEL", "UPDATED")
		for _, s := range sessions {
			title := s.Title
			if title == "" {
				title = "(untitled)"
			}
			t.row(s.ID, title, s.Status, s.Model, s.UpdatedAt.Format("2006-01-02 15:04"))
		}
		return t.flush()
	},
}

var (
	agentCreateTitle  string
	agentCreateSystem string
	agentCreateModel  string
	agentCreateTools  string
)

var agentsCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create an agent session",
	RunE: func(cmd *cobra.Command, args []string) error {
		var tools []string
		if agentCreateTools != "" {
			tools = strings.Split(agentCreateTools, ",")
		}

		session, err := client.CreateAgentSession(cmd.Context(), tavora.CreateAgentSessionInput{
			Title:        agentCreateTitle,
			SystemPrompt: agentCreateSystem,
			Model:        agentCreateModel,
			Tools:        tools,
		})
		if err != nil {
			return err
		}

		if isJSON() {
			return printJSON(session)
		}

		fmt.Printf("Created agent session: %s\n", session.ID)
		if session.Title != "" {
			fmt.Printf("  Title: %s\n", session.Title)
		}
		fmt.Printf("  Model: %s\n", session.Model)
		fmt.Printf("  Tools: %s\n", string(session.ToolsConfig))
		return nil
	},
}

var agentsGetCmd = &cobra.Command{
	Use:   "get [id]",
	Short: "Get an agent session with steps",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		detail, err := client.GetAgentSession(cmd.Context(), args[0])
		if err != nil {
			return err
		}

		if isJSON() {
			return printJSON(detail)
		}

		title := detail.Session.Title
		if title == "" {
			title = "(untitled)"
		}
		fmt.Printf("Agent Session: %s\n", title)
		fmt.Printf("  ID:     %s\n", detail.Session.ID)
		fmt.Printf("  Status: %s\n", detail.Session.Status)
		fmt.Printf("  Model:  %s\n", detail.Session.Model)

		if len(detail.Steps) > 0 {
			fmt.Printf("\n--- Steps (%d) ---\n\n", len(detail.Steps))
			for _, step := range detail.Steps {
				printAgentStep(step)
			}
		}
		return nil
	},
}

var agentsRunCmd = &cobra.Command{
	Use:   "run [session-id] [message]",
	Short: "Run agent with a message (streams events)",
	Example: `  tavora agents run abc123 "Research recent AI papers"
  tavora agents run abc123 "Find pricing info" --output json`,
	Args: cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		sessionID := args[0]
		message := strings.Join(args[1:], " ")

		if isJSON() {
			return client.RunAgent(cmd.Context(), sessionID, message, func(evt tavora.AgentEvent) {
				printJSON(evt) //nolint:errcheck
			})
		}

		return client.RunAgent(cmd.Context(), sessionID, message, printAgentEvent)
	},
}

var agentsPromptCmd = &cobra.Command{
	Use:   "prompt",
	Short: "Show the full agent system prompt for this product",
	RunE: func(cmd *cobra.Command, args []string) error {
		prompt, err := client.GetAgentSystemPrompt(cmd.Context())
		if err != nil {
			return err
		}

		if isJSON() {
			return printJSON(map[string]string{"prompt": prompt})
		}

		fmt.Println(prompt)
		return nil
	},
}

var agentsDeleteCmd = &cobra.Command{
	Use:   "delete [id]",
	Short: "Delete an agent session",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := client.DeleteAgentSession(cmd.Context(), args[0]); err != nil {
			return err
		}

		if isJSON() {
			return printJSON(map[string]string{"status": "deleted"})
		}

		fmt.Println("Agent session deleted.")
		return nil
	},
}

func printAgentEvent(evt tavora.AgentEvent) {
	switch evt.Type {
	case "tool_call":
		argsJSON, _ := json.Marshal(evt.Args)
		fmt.Printf("[tool_call] %s(%s)\n", evt.Tool, string(argsJSON))
	case "tool_result":
		resultJSON, _ := json.Marshal(evt.Result)
		result := string(resultJSON)
		if len(result) > 200 {
			result = result[:200] + "..."
		}
		fmt.Printf("[tool_result] %s → %s\n\n", evt.Tool, result)
	case "response":
		fmt.Printf("\n%s\n", evt.Content)
	case "error":
		status("[error] %s", evt.Content)
	case "done":
		if evt.Summary != nil {
			status("\n[done] %d steps | %d prompt + %d completion tokens",
				evt.Summary.Steps, evt.Summary.Tokens.Prompt, evt.Summary.Tokens.Completion)
		}
	}
}

func printAgentStep(step tavora.AgentStep) {
	switch step.StepType {
	case "user":
		fmt.Printf("[user] %s\n\n", step.Content)
	case "tool_call":
		argsStr, _ := json.Marshal(step.ToolArgs)
		fmt.Printf("[tool_call] %s(%s)\n\n", step.ToolName, string(argsStr))
	case "tool_result":
		content := step.Content
		if len(content) > 200 {
			content = content[:200] + "..."
		}
		fmt.Printf("[tool_result] %s → %s\n\n", step.ToolName, content)
	case "response":
		fmt.Printf("[response] %s\n\n", step.Content)
	case "error":
		fmt.Printf("[error] %s\n\n", step.Content)
	}
}

var agentsInteractiveCmd = &cobra.Command{
	Use:   "interactive [session-id]",
	Short: "Interactive agent REPL (send messages and see traces)",
	Example: `  tavora agents interactive abc123`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		sessionID := args[0]

		session, err := client.GetAgentSession(cmd.Context(), sessionID)
		if err != nil {
			return err
		}

		title := session.Session.Title
		if title == "" {
			title = "(untitled)"
		}
		fmt.Printf("Agent: %s (model: %s, status: %s)\n", title, session.Session.Model, session.Session.Status)

		return repl("you> ", func(input string) error {
			if isJSON() {
				return client.RunAgent(cmd.Context(), sessionID, input, func(evt tavora.AgentEvent) {
					printJSON(evt) //nolint:errcheck
				})
			}
			return client.RunAgent(cmd.Context(), sessionID, input, printAgentEvent)
		})
	},
}

func init() {
	agentsListCmd.Flags().IntVar(&agentsListLimit, "limit", 20, "Max sessions to return")

	agentsCreateCmd.Flags().StringVar(&agentCreateTitle, "title", "", "Session title")
	agentsCreateCmd.Flags().StringVar(&agentCreateSystem, "system", "", "System prompt")
	agentsCreateCmd.Flags().StringVar(&agentCreateModel, "model", "", "AI model")
	agentsCreateCmd.Flags().StringVar(&agentCreateTools, "tools", "", "Comma-separated tool names (e.g. search,list_stores)")

	agentsCmd.AddCommand(agentsListCmd)
	agentsCmd.AddCommand(agentsCreateCmd)
	agentsCmd.AddCommand(agentsGetCmd)
	agentsCmd.AddCommand(agentsRunCmd)
	agentsCmd.AddCommand(agentsInteractiveCmd)
	agentsCmd.AddCommand(agentsPromptCmd)
	agentsCmd.AddCommand(agentsDeleteCmd)
}
