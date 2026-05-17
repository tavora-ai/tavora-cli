package main

import (
	"fmt"

	"github.com/spf13/cobra"
	tavora "github.com/tavora-ai/tavora-sdk-go"
)

// `tavora schedule …` manages cron-driven agent invocations.
//
// Schedules are deliberately *not* part of agent.jsonc — they're a
// runtime resource (cron expression + prompt + agent pointer) that
// belongs in the imperative API. Authoring them in code would turn
// every `tavora dev` cycle into a "should I re-create the cron job?"
// reconciliation problem. Instead: scaffold the agent in code, then
// imperatively bind schedules to it from the CLI / SDK / UI.

var scheduleCmd = &cobra.Command{
	Use:     "schedule",
	Aliases: []string{"schedules"}, // legacy plural form
	Short:   "Manage cron-driven agent invocations",
	Long: `Schedules pair an agent with a cron expression and a prompt.
The server creates a session and replays the prompt on the cadence.

Schedules are NOT in agent.jsonc — they're imperative resources.
Create them with ` + "`tavora schedule add`" + ` or via the SDK; list
and clear them from this CLI or the /schedules page in the UI.`,
}

var scheduleListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all schedules in the project",
	RunE: func(cmd *cobra.Command, args []string) error {
		runs, err := client.ListScheduledRuns(cmd.Context())
		if err != nil {
			return err
		}
		if isJSON() {
			return printJSON(runs)
		}
		if len(runs) == 0 {
			fmt.Println("No schedules.")
			return nil
		}
		t := newTable("ID", "NAME", "CRON", "ENABLED", "RUNS", "NEXT FIRE")
		for _, r := range runs {
			next := "-"
			if r.NextRunAt != nil {
				next = r.NextRunAt.Format("2006-01-02 15:04")
			}
			t.row(r.ID, r.Name, r.CronExpression, fmt.Sprintf("%v", r.Enabled),
				fmt.Sprintf("%d", r.RunCount), next)
		}
		return t.flush()
	},
}

var (
	scheduleAddAgent   string
	scheduleAddTarget  string
	scheduleAddName    string
	scheduleAddCron    string
	scheduleAddMessage string
)

var scheduleAddCmd = &cobra.Command{
	Use:     "add",
	Aliases: []string{"create"},
	Short:   "Add a schedule: cron + prompt against an agent",
	Long: `Add a schedule that fires <prompt> against <agent> on the
<cron> cadence. Pass --target=draft to bind the schedule to the
tavora-dev draft (useful during authoring); --target=live (default)
pins it to the deployed version.

The CLI creates a dedicated agent session for the schedule under the
hood so each schedule has its own conversation history. Use the UI's
/sessions page (or ` + "`tavora session`" + `) to inspect runs.`,
	Example: `  tavora schedule add --agent 7d0f… --cron "*/15 * * * *" --message "Summarise new tickets"
  tavora schedule add --agent 7d0f… --cron "0 9 * * 1" --message "Weekly brief" --name brief`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if scheduleAddAgent == "" || scheduleAddCron == "" || scheduleAddMessage == "" {
			return fmt.Errorf("--agent, --cron and --message are required")
		}
		target := scheduleAddTarget
		if target == "" {
			target = "live"
		}
		title := scheduleAddName
		if title == "" {
			title = "schedule"
		}

		// Step 1 — mint a session for this schedule. The server
		// resolves persona+model+skills from the agent's live (or
		// draft) version; the cron worker replays --message against
		// it each tick.
		session, err := client.CreateAgentSession(cmd.Context(), tavora.CreateAgentSessionInput{
			AgentID: scheduleAddAgent,
			Target:  target,
			Title:   "schedule: " + title,
		})
		if err != nil {
			return fmt.Errorf("create session for schedule: %w", err)
		}

		// Step 2 — bind the schedule to the freshly-minted session.
		run, err := client.CreateScheduledRun(cmd.Context(), tavora.CreateScheduledRunInput{
			AgentSessionID: session.ID,
			Name:           scheduleAddName,
			CronExpression: scheduleAddCron,
			Message:        scheduleAddMessage,
		})
		if err != nil {
			return fmt.Errorf("create schedule: %w", err)
		}
		if isJSON() {
			return printJSON(run)
		}
		fmt.Printf("Added schedule %s (%s)\n", run.Name, run.ID)
		fmt.Printf("  agent:   %s (target=%s)\n", scheduleAddAgent, target)
		fmt.Printf("  cron:    %s\n", run.CronExpression)
		fmt.Printf("  session: %s\n", session.ID)
		if run.NextRunAt != nil {
			fmt.Printf("  next:    %s\n", run.NextRunAt.Format("2006-01-02 15:04:05"))
		}
		return nil
	},
}

var scheduleGetCmd = &cobra.Command{
	Use:   "get [id]",
	Short: "Show a schedule by ID",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		run, err := client.GetScheduledRun(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		if isJSON() {
			return printJSON(run)
		}
		next, last := "-", "-"
		if run.NextRunAt != nil {
			next = run.NextRunAt.Format("2006-01-02 15:04:05")
		}
		if run.LastRunAt != nil {
			last = run.LastRunAt.Format("2006-01-02 15:04:05")
		}
		fields := []kv{
			field("ID", run.ID),
			field("Session", run.AgentSessionID),
			field("Cron", run.CronExpression),
			field("Message", run.Message),
			field("Enabled", fmt.Sprintf("%v", run.Enabled)),
			field("Run count", fmt.Sprintf("%d", run.RunCount)),
			field("Next fire", next),
			field("Last fire", last),
		}
		if run.LastError != "" {
			fields = append(fields, field("Last error", run.LastError))
		}
		detail(fmt.Sprintf("Schedule: %s", run.Name), fields...)
		return nil
	},
}

var scheduleRmAll bool

var scheduleRmCmd = &cobra.Command{
	Use:     "rm [id]",
	Aliases: []string{"delete"},
	Short:   "Remove a schedule, or all schedules with --all",
	Args:    cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if scheduleRmAll {
			if len(args) > 0 {
				return fmt.Errorf("pass either an id or --all, not both")
			}
			out, err := client.DeleteAllScheduledRuns(cmd.Context())
			if err != nil {
				return err
			}
			if isJSON() {
				return printJSON(out)
			}
			fmt.Printf("Deleted %d schedule(s).\n", out.Deleted)
			return nil
		}
		if len(args) == 0 {
			return fmt.Errorf("pass a schedule id or --all")
		}
		if err := client.DeleteScheduledRun(cmd.Context(), args[0]); err != nil {
			return err
		}
		if isJSON() {
			return printJSON(map[string]string{"status": "deleted"})
		}
		fmt.Println("Schedule deleted.")
		return nil
	},
}

func init() {
	scheduleAddCmd.Flags().StringVar(&scheduleAddAgent, "agent", "", "Server-side agent UUID (visible in the UI URL or in tavora dev's --verbose output) (required)")
	scheduleAddCmd.Flags().StringVar(&scheduleAddTarget, "target", "live", "Agent version to bind: live (default) or draft (the tavora-dev draft)")
	scheduleAddCmd.Flags().StringVar(&scheduleAddName, "name", "", "Display name for the schedule")
	scheduleAddCmd.Flags().StringVar(&scheduleAddCron, "cron", "", "Cron expression — \"min hr dom mon dow\" (required)")
	scheduleAddCmd.Flags().StringVar(&scheduleAddMessage, "message", "", "Prompt to send each fire (required)")
	_ = scheduleAddCmd.MarkFlagRequired("agent")
	_ = scheduleAddCmd.MarkFlagRequired("cron")
	_ = scheduleAddCmd.MarkFlagRequired("message")

	scheduleRmCmd.Flags().BoolVar(&scheduleRmAll, "all", false, "Delete every schedule in the project (idempotent)")

	scheduleCmd.AddCommand(scheduleListCmd)
	scheduleCmd.AddCommand(scheduleAddCmd)
	scheduleCmd.AddCommand(scheduleGetCmd)
	scheduleCmd.AddCommand(scheduleRmCmd)
}
