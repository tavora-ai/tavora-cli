package main

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

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
	evalRunSet     string
	evalRunJudge   string
	evalRunWait    bool
	evalRunGate    bool
	evalRunTimeout time.Duration
)

var evalsRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Run the eval suite (use --wait for completion, --gate for CI exit codes)",
	Long: `Trigger an eval run.

By default, fires the run and exits — useful when you want to inspect the
result later via 'tavora evals runs get <id>'.

For CI pipelines that need to gate on agent quality:

    tavora evals run --gate --timeout 10m

--gate implies --wait; the command polls until completion, prints a results
table, and exits non-zero if any case fails. Suitable for use as the last
step of a 'eval' job in GitHub Actions / GitLab CI / etc.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// --gate is shorthand for --wait + exit-on-failure.
		wait := evalRunWait || evalRunGate

		run, err := client.RunEval(cmd.Context(), tavora.RunEvalInput{
			SetFilter:  evalRunSet,
			JudgeModel: evalRunJudge,
		})
		if err != nil {
			return err
		}

		if !wait {
			if isJSON() {
				return printJSON(run)
			}
			fmt.Printf("Eval run started: %s\n", run.ID)
			fmt.Printf("  Status: %s\n", run.Status)
			fmt.Printf("  Cases:  %d\n", run.TotalCases)
			fmt.Println()
			fmt.Printf("Re-run with --wait or --gate to block until completion.\n")
			return nil
		}

		fmt.Fprintf(os.Stderr, "Eval run %s started, waiting for completion", run.ID)
		detail, err := pollEvalRun(cmd.Context(), run.ID, evalRunTimeout)
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return err
		}

		if isJSON() {
			if err := printJSON(detail); err != nil {
				return err
			}
		} else {
			printEvalResults(detail)
		}

		// Gate behavior: any failed case → non-zero exit. The error
		// message goes to stderr (visible in CI logs); cobra's RunE
		// translates the error to exit code 1 automatically.
		if evalRunGate && detail.Run.Failed > 0 {
			return fmt.Errorf("eval gate failed: %d/%d cases did not pass", detail.Run.Failed, detail.Run.TotalCases)
		}
		return nil
	},
}

// pollEvalRun blocks until the run reaches a terminal state or timeout.
// Prints a dot per poll to stderr so CI logs show progress without being
// noisy. Returns the final EvalRunDetail.
func pollEvalRun(ctx context.Context, runID string, timeout time.Duration) (*tavora.EvalRunDetail, error) {
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		detail, err := client.GetEvalRun(ctx, runID)
		if err != nil {
			return nil, fmt.Errorf("polling eval run: %w", err)
		}

		switch detail.Run.Status {
		case "completed":
			return detail, nil
		case "failed":
			return detail, fmt.Errorf("eval run failed (server-side error, not a failed case — check the run detail)")
		}

		fmt.Fprint(os.Stderr, ".")

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("timed out after %s waiting for eval run to complete", timeout)
		case <-ticker.C:
		}
	}
}

// printEvalResults renders the per-case table + summary the same shape
// the legacy examples/eval-ci binary printed, so CI users porting from
// it get a familiar log format.
func printEvalResults(detail *tavora.EvalRunDetail) {
	fmt.Fprintf(os.Stderr, "\nRun: %s | Status: %s\n\n", detail.Run.ID, detail.Run.Status)

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "CASE\tSCORE\tPASS\tDURATION")
	fmt.Fprintln(w, "----\t-----\t----\t--------")

	for _, r := range detail.Results {
		pass := "FAIL"
		if r.Pass {
			pass = "PASS"
		}
		fmt.Fprintf(w, "%s\t%d/10\t%s\t%dms\n", r.CaseName, r.Score, pass, r.DurationMs)
	}
	_ = w.Flush()

	fmt.Fprintf(os.Stderr, "\nPassed: %d/%d | Average Score: %.1f/10\n",
		detail.Run.Passed, detail.Run.TotalCases, detail.Run.AverageScore)
}

var evalsSeedCmd = &cobra.Command{
	Use:   "seed",
	Short: "Create sample eval cases (no-op if cases already exist)",
	Long: `Create a small set of sample eval cases so you have something real to gate
on. Idempotent: if the app already has any eval cases, the command
prints how many it found and exits without mutating state.

Intended for first-time setup of a CI eval job — run once locally, then
delete or replace with cases that exercise your actual agent.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		existing, err := client.ListEvalCases(ctx)
		if err != nil {
			return err
		}
		if len(existing) > 0 {
			fmt.Printf("Found %d existing eval cases — no changes made.\n", len(existing))
			return nil
		}

		samples := []tavora.CreateEvalCaseInput{
			{
				Name:     "basic-search",
				SetName:  "ci",
				Prompt:   "Search for documents in the knowledge base and summarize what you find.",
				Criteria: "Must use the search tool at least once and provide a coherent summary of results. If no documents exist, should clearly state that.",
				Tools:    []string{"search", "list_stores"},
			},
			{
				Name:     "memory-usage",
				SetName:  "ci",
				Prompt:   "Remember that the project deadline is next Friday, then recall it.",
				Criteria: "Must use the remember tool to store information and the recall tool to retrieve it. The recalled information should match what was stored.",
				Tools:    []string{"remember", "recall", "memories"},
			},
		}

		created := make([]tavora.EvalCase, 0, len(samples))
		for _, s := range samples {
			ec, err := client.CreateEvalCase(ctx, s)
			if err != nil {
				return fmt.Errorf("creating case %q: %w", s.Name, err)
			}
			created = append(created, *ec)
		}

		if isJSON() {
			return printJSON(created)
		}
		for _, c := range created {
			fmt.Printf("Created: %s (%s)\n", c.Name, c.ID)
		}
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
	evalsRunCmd.Flags().BoolVar(&evalRunWait, "wait", false, "Poll until the run completes; print a results table")
	evalsRunCmd.Flags().BoolVar(&evalRunGate, "gate", false, "Implies --wait; exit non-zero if any case fails (for CI)")
	evalsRunCmd.Flags().DurationVar(&evalRunTimeout, "timeout", 5*time.Minute, "Max wait for completion (only with --wait or --gate)")

	evalRunsCmd.AddCommand(evalRunsListCmd)
	evalRunsCmd.AddCommand(evalRunsGetCmd)

	evalsCmd.AddCommand(evalsListCmd)
	evalsCmd.AddCommand(evalsCreateCmd)
	evalsCmd.AddCommand(evalsDeleteCmd)
	evalsCmd.AddCommand(evalsRunCmd)
	evalsCmd.AddCommand(evalsSeedCmd)
	evalsCmd.AddCommand(evalRunsCmd)
}
