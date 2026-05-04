package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	tavora "github.com/tavora-ai/tavora-sdk-go"
)

var studioCmd = &cobra.Command{
	Use:   "studio",
	Short: "Agent debugging tools (trace, replay, analyze)",
}

var studioTraceCmd = &cobra.Command{
	Use:   "trace [session-id]",
	Short: "View enriched agent trace",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		trace, err := client.GetStudioTrace(cmd.Context(), args[0])
		if err != nil {
			return err
		}

		if isJSON() {
			return printJSON(trace)
		}

		title := trace.Session.Title
		if title == "" {
			title = "(untitled)"
		}
		fmt.Printf("Trace: %s\n", title)
		fmt.Printf("  Session: %s\n", trace.Session.ID)
		fmt.Printf("  Model:   %s\n", trace.Session.Model)
		fmt.Printf("  Status:  %s\n", trace.Session.Status)

		if len(trace.Tools) > 0 {
			fmt.Printf("  Tools:   %s\n", strings.Join(trace.Tools, ", "))
		}

		if trace.SystemPrompt != "" {
			fmt.Printf("\n--- System Prompt ---\n%s\n", trace.SystemPrompt)
		}

		if len(trace.Steps) > 0 {
			fmt.Printf("\n--- Steps (%d) ---\n\n", len(trace.Steps))
			for i, raw := range trace.Steps {
				var step map[string]any
				if err := json.Unmarshal(raw, &step); err != nil {
					fmt.Printf("[step %d] (parse error)\n", i)
					continue
				}
				stepType, _ := step["step_type"].(string)
				content, _ := step["content"].(string)
				toolName, _ := step["tool_name"].(string)

				switch stepType {
				case "user":
					fmt.Printf("[%d] user: %s\n\n", i, content)
				case "tool_call":
					argsJSON, _ := json.Marshal(step["tool_args"])
					fmt.Printf("[%d] tool_call: %s(%s)\n\n", i, toolName, string(argsJSON))
				case "tool_result":
					if len(content) > 200 {
						content = content[:200] + "..."
					}
					fmt.Printf("[%d] tool_result: %s → %s\n\n", i, toolName, content)
				case "response":
					fmt.Printf("[%d] response: %s\n\n", i, content)
				case "error":
					fmt.Printf("[%d] error: %s\n\n", i, content)
				default:
					fmt.Printf("[%d] %s: %s\n\n", i, stepType, content)
				}
			}
		}
		return nil
	},
}

var (
	replayFromStep int32
	replaySystem   string
	replayMessage  string
)

var studioReplayCmd = &cobra.Command{
	Use:   "replay [session-id]",
	Short: "Replay agent session from a specific step",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		config := tavora.StudioReplayConfig{
			FromStep: replayFromStep,
		}
		if replaySystem != "" {
			config.SystemPrompt = &replaySystem
		}
		if replayMessage != "" {
			config.Message = &replayMessage
		}

		if isJSON() {
			return client.ReplayFromStep(cmd.Context(), args[0], config, func(evt tavora.AgentEvent) {
				printJSON(evt) //nolint:errcheck
			})
		}

		fmt.Printf("Replaying from step %d...\n\n", replayFromStep)
		return client.ReplayFromStep(cmd.Context(), args[0], config, printAgentEvent)
	},
}

var (
	analyzeExpected string
	analyzeSteps    string
)

var studioAnalyzeCmd = &cobra.Command{
	Use:   "analyze [session-id]",
	Short: "Get AI fix suggestions for a failed agent run",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var failedSteps []int
		if analyzeSteps != "" {
			for _, s := range strings.Split(analyzeSteps, ",") {
				var n int
				if _, err := fmt.Sscanf(strings.TrimSpace(s), "%d", &n); err == nil {
					failedSteps = append(failedSteps, n)
				}
			}
		}

		suggestion, err := client.AnalyzeFix(cmd.Context(), args[0], tavora.StudioFixRequest{
			FailedSteps:     failedSteps,
			ExpectedOutcome: analyzeExpected,
		})
		if err != nil {
			return err
		}

		if isJSON() {
			return printJSON(suggestion)
		}

		fmt.Println("=== AI Fix Analysis ===")
		fmt.Printf("\n--- Reasoning ---\n%s\n", suggestion.Reasoning)
		fmt.Printf("\n--- Prompt Changes ---\n%s\n", suggestion.PromptChanges)

		if suggestion.EvalCase != nil {
			fmt.Printf("\n--- Suggested Eval Case ---\n")
			fmt.Printf("  Name:     %s\n", suggestion.EvalCase.Name)
			fmt.Printf("  Prompt:   %s\n", suggestion.EvalCase.Prompt)
			fmt.Printf("  Criteria: %s\n", suggestion.EvalCase.Criteria)
		}
		return nil
	},
}

func init() {
	studioReplayCmd.Flags().Int32Var(&replayFromStep, "from-step", 0, "Step index to replay from (required)")
	studioReplayCmd.Flags().StringVar(&replaySystem, "system", "", "Override system prompt")
	studioReplayCmd.Flags().StringVar(&replayMessage, "message", "", "Override user message")
	studioReplayCmd.MarkFlagRequired("from-step")

	studioAnalyzeCmd.Flags().StringVar(&analyzeExpected, "expected", "", "Expected outcome description (required)")
	studioAnalyzeCmd.Flags().StringVar(&analyzeSteps, "steps", "", "Comma-separated failed step indices")
	studioAnalyzeCmd.MarkFlagRequired("expected")

	studioCmd.AddCommand(studioTraceCmd)
	studioCmd.AddCommand(studioReplayCmd)
	studioCmd.AddCommand(studioAnalyzeCmd)
}
