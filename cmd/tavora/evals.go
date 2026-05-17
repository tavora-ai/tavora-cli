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

// tavora evals — cases and suites are authored in
// tavora/agents/<id>/evals/*.json and roll in via `tavora dev`.
// CRUD subcommands were retired 2026-05-17 to leave source-sync as
// the single writer for those tables.
//
// The verbs that remain are operational, not authoring:
//
//   tavora evals list                       — show cases (read)
//   tavora evals run <agent>                — trigger an eval run
//                                             against the agent's
//                                             pinned suite (the
//                                             deployed persona)
//   tavora evals run <agent> --wait         — block until done
//   tavora evals run <agent> --gate         — exit non-zero on
//                                             any failed case (CI)
//   tavora evals latest <agent>             — show the most recent
//                                             run + per-case results
//   tavora evals runs list                  — recent runs (all suites)
//   tavora evals runs get <id>              — single run + results
//
// `<agent>` is the local id from agent.jsonc (e.g. "support") OR
// the server UUID. The resolver looks at code_first_local_id first
// so the CLI plays nicely with the folder shape.
var evalsCmd = &cobra.Command{
	Use:   "evals",
	Short: "Trigger eval runs and inspect cases / run history",
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
			fmt.Println("No eval cases found. Add tavora/agents/<id>/evals/<name>.json then run `tavora dev`.")
			return nil
		}

		t := newTable("ID", "NAME", "SET", "THRESHOLD", "CREATED")
		for _, c := range cases {
			t.row(c.ID, c.Name, c.SetName, fmt.Sprintf("%d", c.PassThreshold), c.CreatedAt.Format("2006-01-02"))
		}
		return t.flush()
	},
}

var evalRunsCmd = &cobra.Command{
	Use:   "runs",
	Short: "Inspect eval runs",
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

// --- tavora evals run <agent> ---

var (
	evalRunWait    bool
	evalRunGate    bool
	evalRunTimeout time.Duration
)

var evalsRunCmd = &cobra.Command{
	Use:   "run <agent>",
	Short: "Trigger an eval run against the agent's pinned suite (live persona)",
	Long: `Trigger an eval run against the agent's pinned suite, using the
deployed (live) persona. By default fires the run and exits; use
--wait to block until completion and --gate to also exit non-zero
when any case fails (suitable as the last step of a CI eval job).

The agent argument accepts either the code-first local id (e.g.
"support" from tavora/agents/support/agent.jsonc) or the server UUID.

Examples:
  tavora evals run support
  tavora evals run support --wait
  tavora evals run support --gate --timeout 10m`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		agentID, err := resolveAgentID(ctx, args[0])
		if err != nil {
			return err
		}

		res, err := client.RunAgentEval(ctx, agentID)
		if err != nil {
			return err
		}
		wait := evalRunWait || evalRunGate

		if !wait {
			if isJSON() {
				return printJSON(res)
			}
			fmt.Printf("Eval run started: %s\n", res.Run.ID)
			fmt.Printf("  Status: %s\n", res.Run.Status)
			fmt.Printf("  Cases:  %d\n", res.Run.TotalCases)
			fmt.Println()
			fmt.Println("Re-run with --wait or --gate to block until completion.")
			return nil
		}

		fmt.Fprintf(os.Stderr, "Eval run %s started, waiting for completion", res.Run.ID)
		detail, err := pollEvalRun(ctx, res.Run.ID, evalRunTimeout)
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

		if evalRunGate && detail.Run.Failed > 0 {
			return fmt.Errorf("eval gate failed: %d/%d cases did not pass", detail.Run.Failed, detail.Run.TotalCases)
		}
		return nil
	},
}

// --- tavora evals latest <agent> ---

var evalsLatestCmd = &cobra.Command{
	Use:   "latest <agent>",
	Short: "Show the most recent eval run for an agent + per-case results",
	Long: `Pulls the most recent eval run for the agent's pinned suite and
prints the same per-case table that 'tavora evals runs get' produces.
Convenience for the common 'did the last run pass?' question.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		agentID, err := resolveAgentID(ctx, args[0])
		if err != nil {
			return err
		}

		runs, err := client.ListAgentEvalRuns(ctx, agentID, 1)
		if err != nil {
			return err
		}
		if len(runs) == 0 {
			fmt.Println("No eval runs yet. Trigger one with `tavora evals run <agent>`.")
			return nil
		}

		detail, err := client.GetEvalRun(ctx, runs[0].ID)
		if err != nil {
			return err
		}
		if isJSON() {
			return printJSON(detail)
		}
		printEvalResults(detail)
		return nil
	},
}

// pollEvalRun blocks until the run reaches a terminal state or the
// timeout fires. Prints a dot per poll to stderr so CI logs show
// progress without being noisy.
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
		case "passed", "failed", "completed":
			// Terminal "done" states the runner emits today. Anything
			// else (pending / running) keeps polling.
			return detail, nil
		case "error":
			return detail, fmt.Errorf("eval run errored (server-side failure, distinct from a failed case — inspect the run detail)")
		}
		fmt.Fprint(os.Stderr, ".")
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("timed out after %s waiting for eval run to complete", timeout)
		case <-ticker.C:
		}
	}
}

// printEvalResults renders the per-case table + summary. Matches the
// shape the retired examples/eval-ci binary printed so CI users
// porting from it get a familiar log format.
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

// resolveAgentID maps a CLI argument (local id like "support" OR a
// server UUID OR an agent display name) to the server-side agent
// UUID that `RunAgentEval` expects. Calls ListAgentConfigs once and
// then defers to pickAgent for the in-memory match.
func resolveAgentID(ctx context.Context, arg string) (string, error) {
	configs, err := client.ListAgentConfigs(ctx)
	if err != nil {
		return "", fmt.Errorf("list agents: %w", err)
	}
	return pickAgent(arg, configs)
}

// pickAgent is the pure resolver — separated from resolveAgentID so
// the matching policy can be unit-tested without a fake transport.
// Resolution order: agent.id (exact UUID match) → code_first_local_id
// (the agent.jsonc `id`) → display name (case-sensitive). The order
// matters: a literal UUID always wins so a user who pasted an id
// never ambiguously matches a same-named local agent.
func pickAgent(arg string, configs []tavora.AgentConfig) (string, error) {
	for _, c := range configs {
		if c.ID == arg {
			return c.ID, nil
		}
	}
	for _, c := range configs {
		if c.CodeFirstLocalID != nil && *c.CodeFirstLocalID == arg {
			return c.ID, nil
		}
	}
	for _, c := range configs {
		if c.Name == arg {
			return c.ID, nil
		}
	}
	return "", fmt.Errorf("no agent matches %q (tried agent.id, code_first_local_id, name)", arg)
}

func init() {
	evalsRunCmd.Flags().BoolVar(&evalRunWait, "wait", false, "Poll until the run completes; print a results table")
	evalsRunCmd.Flags().BoolVar(&evalRunGate, "gate", false, "Implies --wait; exit non-zero if any case fails (for CI)")
	evalsRunCmd.Flags().DurationVar(&evalRunTimeout, "timeout", 5*time.Minute, "Max wait for completion (only with --wait or --gate)")

	evalRunsCmd.AddCommand(evalRunsListCmd)
	evalRunsCmd.AddCommand(evalRunsGetCmd)

	evalsCmd.AddCommand(evalsListCmd)
	evalsCmd.AddCommand(evalsRunCmd)
	evalsCmd.AddCommand(evalsLatestCmd)
	evalsCmd.AddCommand(evalRunsCmd)
}
