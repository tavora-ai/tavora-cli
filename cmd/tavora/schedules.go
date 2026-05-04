package main

import (
	"fmt"

	"github.com/spf13/cobra"
	tavora "github.com/tavora-ai/tavora-sdk-go"
)

var schedulesCmd = &cobra.Command{
	Use:   "schedules",
	Short: "Manage scheduled agent runs",
}

var schedulesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all scheduled runs",
	RunE: func(cmd *cobra.Command, args []string) error {
		runs, err := client.ListScheduledRuns(cmd.Context())
		if err != nil {
			return err
		}

		if isJSON() {
			return printJSON(runs)
		}

		if len(runs) == 0 {
			fmt.Println("No scheduled runs found.")
			return nil
		}

		t := newTable("ID", "NAME", "CRON", "ENABLED", "RUNS", "NEXT RUN")
		for _, r := range runs {
			nextRun := "-"
			if r.NextRunAt != nil {
				nextRun = r.NextRunAt.Format("2006-01-02 15:04")
			}
			t.row(r.ID, r.Name, r.CronExpression, fmt.Sprintf("%v", r.Enabled),
				fmt.Sprintf("%d", r.RunCount), nextRun)
		}
		return t.flush()
	},
}

var (
	schedCreateAgent   string
	schedCreateName    string
	schedCreateCron    string
	schedCreateMessage string
)

var schedulesCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a scheduled run",
	RunE: func(cmd *cobra.Command, args []string) error {
		run, err := client.CreateScheduledRun(cmd.Context(), tavora.CreateScheduledRunInput{
			AgentSessionID: schedCreateAgent,
			Name:           schedCreateName,
			CronExpression: schedCreateCron,
			Message:        schedCreateMessage,
		})
		if err != nil {
			return err
		}

		if isJSON() {
			return printJSON(run)
		}

		fmt.Printf("Created scheduled run: %s (%s)\n", run.Name, run.ID)
		if run.NextRunAt != nil {
			fmt.Printf("  Next run: %s\n", run.NextRunAt.Format("2006-01-02 15:04:05"))
		}
		return nil
	},
}

var schedulesGetCmd = &cobra.Command{
	Use:   "get [id]",
	Short: "Get a scheduled run by ID",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		run, err := client.GetScheduledRun(cmd.Context(), args[0])
		if err != nil {
			return err
		}

		if isJSON() {
			return printJSON(run)
		}

		nextRun := "-"
		if run.NextRunAt != nil {
			nextRun = run.NextRunAt.Format("2006-01-02 15:04:05")
		}
		lastRun := "-"
		if run.LastRunAt != nil {
			lastRun = run.LastRunAt.Format("2006-01-02 15:04:05")
		}

		fields := []kv{
			field("ID", run.ID),
			field("Agent", run.AgentSessionID),
			field("Cron", run.CronExpression),
			field("Message", run.Message),
			field("Enabled", fmt.Sprintf("%v", run.Enabled)),
			field("Run Count", fmt.Sprintf("%d", run.RunCount)),
			field("Next Run", nextRun),
			field("Last Run", lastRun),
		}
		if run.LastError != "" {
			fields = append(fields, field("Last Error", run.LastError))
		}

		detail(fmt.Sprintf("Schedule: %s", run.Name), fields...)
		return nil
	},
}

var schedulesDeleteCmd = &cobra.Command{
	Use:   "delete [id]",
	Short: "Delete a scheduled run by ID",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := client.DeleteScheduledRun(cmd.Context(), args[0]); err != nil {
			return err
		}

		if isJSON() {
			return printJSON(map[string]string{"status": "deleted"})
		}

		fmt.Println("Scheduled run deleted.")
		return nil
	},
}

func init() {
	schedulesCreateCmd.Flags().StringVar(&schedCreateAgent, "agent", "", "Agent session ID (required)")
	schedulesCreateCmd.Flags().StringVar(&schedCreateName, "name", "", "Schedule name")
	schedulesCreateCmd.Flags().StringVar(&schedCreateCron, "cron", "", "Cron expression (required)")
	schedulesCreateCmd.Flags().StringVar(&schedCreateMessage, "message", "", "Message to send (required)")
	schedulesCreateCmd.MarkFlagRequired("agent")
	schedulesCreateCmd.MarkFlagRequired("cron")
	schedulesCreateCmd.MarkFlagRequired("message")

	schedulesCmd.AddCommand(schedulesListCmd)
	schedulesCmd.AddCommand(schedulesCreateCmd)
	schedulesCmd.AddCommand(schedulesGetCmd)
	schedulesCmd.AddCommand(schedulesDeleteCmd)
}
