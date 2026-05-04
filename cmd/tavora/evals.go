package main

import (
	"fmt"

	"github.com/spf13/cobra"
	tavora "github.com/tavora-ai/tavora-sdk-go"
)

var evalsCmd = &cobra.Command{
	Use:   "evals",
	Short: "Manage eval test cases and runs",
}

var evalsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List eval test cases",
	RunE: func(cmd *cobra.Command, args []string) error {
		cases, err := client.ListEvalCases(cmd.Context())
		if err != nil {
			return err
		}

		if isJSON() {
			return printJSON(cases)
		}

		if len(cases) == 0 {
			fmt.Println("No eval cases found.")
			return nil
		}

		t := newTable("ID", "NAME", "SET", "THRESHOLD", "CREATED")
		for _, c := range cases {
			t.row(c.ID, c.Name, c.SetName, fmt.Sprintf("%d", c.PassThreshold), c.CreatedAt.Format("2006-01-02"))
		}
		return t.flush()
	},
}

var (
	evalCreateName      string
	evalCreateDesc      string
	evalCreateSet       string
	evalCreatePrompt    string
	evalCreateCriteria  string
	evalCreateSystem    string
	evalCreateThreshold int32
)

var evalsCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create an eval test case",
	RunE: func(cmd *cobra.Command, args []string) error {
		input := tavora.CreateEvalCaseInput{
			Name:        evalCreateName,
			Description: evalCreateDesc,
			SetName:     evalCreateSet,
			Prompt:      evalCreatePrompt,
			Criteria:    evalCreateCriteria,
			SystemPrompt: evalCreateSystem,
		}
		if evalCreateThreshold > 0 {
			input.PassThreshold = &evalCreateThreshold
		}

		ec, err := client.CreateEvalCase(cmd.Context(), input)
		if err != nil {
			return err
		}

		if isJSON() {
			return printJSON(ec)
		}

		fmt.Printf("Created eval case: %s (%s)\n", ec.Name, ec.ID)
		return nil
	},
}

var evalsDeleteCmd = &cobra.Command{
	Use:   "delete [id]",
	Short: "Delete an eval test case",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := client.DeleteEvalCase(cmd.Context(), args[0]); err != nil {
			return err
		}

		if isJSON() {
			return printJSON(map[string]string{"status": "deleted"})
		}

		fmt.Println("Eval case deleted.")
		return nil
	},
}

// --- Eval Runs ---

var (
	evalRunSet   string
	evalRunJudge string
)

var evalsRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Run the eval suite",
	RunE: func(cmd *cobra.Command, args []string) error {
		run, err := client.RunEval(cmd.Context(), tavora.RunEvalInput{
			SetFilter:  evalRunSet,
			JudgeModel: evalRunJudge,
		})
		if err != nil {
			return err
		}

		if isJSON() {
			return printJSON(run)
		}

		fmt.Printf("Eval run started: %s\n", run.ID)
		fmt.Printf("  Status: %s\n", run.Status)
		fmt.Printf("  Cases:  %d\n", run.TotalCases)
		return nil
	},
}

var evalRunsCmd = &cobra.Command{
	Use:   "runs",
	Short: "Manage eval runs",
}

var evalRunsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List eval runs",
	RunE: func(cmd *cobra.Command, args []string) error {
		runs, err := client.ListEvalRuns(cmd.Context())
		if err != nil {
			return err
		}

		if isJSON() {
			return printJSON(runs)
		}

		if len(runs) == 0 {
			fmt.Println("No eval runs found.")
			return nil
		}

		t := newTable("ID", "STATUS", "CASES", "PASSED", "FAILED", "AVG SCORE", "CREATED")
		for _, r := range runs {
			t.row(r.ID, r.Status,
				fmt.Sprintf("%d", r.TotalCases),
				fmt.Sprintf("%d", r.Passed),
				fmt.Sprintf("%d", r.Failed),
				fmt.Sprintf("%.1f", r.AverageScore),
				r.CreatedAt.Format("2006-01-02 15:04"))
		}
		return t.flush()
	},
}

var evalRunsGetCmd = &cobra.Command{
	Use:   "get [id]",
	Short: "Get an eval run with results",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		detail, err := client.GetEvalRun(cmd.Context(), args[0])
		if err != nil {
			return err
		}

		if isJSON() {
			return printJSON(detail)
		}

		fmt.Printf("Eval Run: %s\n", detail.Run.ID)
		fmt.Printf("  Status:    %s\n", detail.Run.Status)
		fmt.Printf("  Cases:     %d (%d passed, %d failed)\n",
			detail.Run.TotalCases, detail.Run.Passed, detail.Run.Failed)
		fmt.Printf("  Avg Score: %.1f\n", detail.Run.AverageScore)
		fmt.Printf("  Judge:     %s\n", detail.Run.JudgeModel)

		if len(detail.Results) > 0 {
			fmt.Printf("\n--- Results ---\n\n")
			for _, r := range detail.Results {
				status := "PASS"
				if !r.Pass {
					status = "FAIL"
				}
				fmt.Printf("[%s] %s (score: %d, %dms)\n", status, r.CaseName, r.Score, r.DurationMs)
				if r.Reasoning != "" {
					fmt.Printf("  %s\n", r.Reasoning)
				}
				if r.Error != "" {
					fmt.Printf("  Error: %s\n", r.Error)
				}
				fmt.Println()
			}
		}
		return nil
	},
}

func init() {
	evalsCreateCmd.Flags().StringVar(&evalCreateName, "name", "", "Case name (required)")
	evalsCreateCmd.Flags().StringVar(&evalCreateDesc, "description", "", "Case description")
	evalsCreateCmd.Flags().StringVar(&evalCreateSet, "set", "", "Eval set name")
	evalsCreateCmd.Flags().StringVar(&evalCreatePrompt, "prompt", "", "Test prompt (required)")
	evalsCreateCmd.Flags().StringVar(&evalCreateCriteria, "criteria", "", "Eval criteria (required)")
	evalsCreateCmd.Flags().StringVar(&evalCreateSystem, "system", "", "System prompt")
	evalsCreateCmd.Flags().Int32Var(&evalCreateThreshold, "threshold", 0, "Pass threshold (1-10)")
	evalsCreateCmd.MarkFlagRequired("name")
	evalsCreateCmd.MarkFlagRequired("prompt")
	evalsCreateCmd.MarkFlagRequired("criteria")

	evalsRunCmd.Flags().StringVar(&evalRunSet, "set", "", "Filter by eval set name")
	evalsRunCmd.Flags().StringVar(&evalRunJudge, "judge", "", "Judge model override")

	evalRunsCmd.AddCommand(evalRunsListCmd)
	evalRunsCmd.AddCommand(evalRunsGetCmd)

	evalsCmd.AddCommand(evalsListCmd)
	evalsCmd.AddCommand(evalsCreateCmd)
	evalsCmd.AddCommand(evalsDeleteCmd)
	evalsCmd.AddCommand(evalsRunCmd)
	evalsCmd.AddCommand(evalRunsCmd)
}
