package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var metricsCmd = &cobra.Command{
	Use:   "metrics",
	Short: "Show product-level metrics dashboard",
	RunE: func(cmd *cobra.Command, args []string) error {
		m, err := client.GetMetrics(cmd.Context())
		if err != nil {
			return err
		}

		if isJSON() {
			return printJSON(m)
		}

		fmt.Println("=== Tokens ===")
		fmt.Printf("  Prompt:     %d\n", m.Tokens.PromptTokens)
		fmt.Printf("  Candidate:  %d\n", m.Tokens.CandidateTokens)
		fmt.Printf("  Total:      %d\n", m.Tokens.TotalTokens)
		fmt.Printf("  Requests:   %d\n", m.Tokens.RequestCount)

		fmt.Println("\n=== Agents ===")
		fmt.Printf("  Total:      %d\n", m.Agents.TotalSessions)
		fmt.Printf("  Active:     %d\n", m.Agents.ActiveSessions)
		fmt.Printf("  Completed:  %d\n", m.Agents.CompletedSessions)
		fmt.Printf("  Errors:     %d\n", m.Agents.ErrorSessions)
		fmt.Printf("  Steps:      %d\n", m.Agents.TotalSteps)

		fmt.Println("\n=== Evals ===")
		fmt.Printf("  Runs:       %d (%d completed)\n", m.Evals.TotalRuns, m.Evals.CompletedRuns)
		fmt.Printf("  Passed:     %d\n", m.Evals.TotalPassed)
		fmt.Printf("  Failed:     %d\n", m.Evals.TotalFailed)
		fmt.Printf("  Avg Score:  %.1f\n", m.Evals.AverageScore)

		return nil
	},
}
