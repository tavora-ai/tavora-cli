package main

import (
	"fmt"

	"github.com/spf13/cobra"
	tavora "github.com/tavora-ai/tavora-sdk-go"
)

// The promotions commands are the CI/CD seam for eval-gated rollouts:
// propose a version, watch the eval run to completion, then approve or
// reject. Mirrors sdk.Client.{ProposePromotion, ApprovePromotion,
// RejectPromotion, GetPromotion, ListPendingPromotions}.

var promotionsCmd = &cobra.Command{
	Use:   "promotions",
	Short: "Manage eval-gated agent version promotions",
	Long: `Promotions gate an agent version behind its attached eval suite before it
gets pinned to a target (API, channel, etc.). Typical CI flow:

  tavora promotions propose --version-id VERSION_ID
  tavora promotions get PROMOTION_ID       # watch status until pending_approval
  tavora promotions approve PROMOTION_ID   # or reject --reason "…"`,
}

var promotionsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List pending promotions",
	RunE: func(cmd *cobra.Command, args []string) error {
		proms, err := client.ListPendingPromotions(cmd.Context())
		if err != nil {
			return err
		}
		if isJSON() {
			return printJSON(proms)
		}
		if len(proms) == 0 {
			fmt.Println("No pending promotions.")
			return nil
		}
		t := newTable("ID", "VERSION", "TARGET", "STATUS", "PROPOSED_AT")
		for _, p := range proms {
			target := p.TargetType
			if p.TargetRef != "" {
				target = p.TargetType + ":" + p.TargetRef
			}
			t.row(p.ID, p.VersionID, target, p.Status, p.ProposedAt.Format("2006-01-02 15:04"))
		}
		return t.flush()
	},
}

var (
	promoteVersionID  string
	promoteTargetType string
	promoteTargetRef  string
)

var promotionsProposeCmd = &cobra.Command{
	Use:   "propose",
	Short: "Propose promoting an agent version",
	Long: `Proposes pinning an agent version at a target. The promotion starts in
pending_eval; the server-side eval runner advances it to pending_approval
(or failed_eval) once the attached suite finishes.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		input := tavora.ProposePromotionInput{
			VersionID:  promoteVersionID,
			TargetType: promoteTargetType,
			TargetRef:  promoteTargetRef,
		}
		prom, err := client.ProposePromotion(cmd.Context(), input)
		if err != nil {
			return err
		}
		if isJSON() {
			return printJSON(prom)
		}
		fmt.Printf("Proposed promotion %s — status: %s\n", prom.ID, prom.Status)
		if prom.Status == "pending_eval" {
			fmt.Println("(watch with `tavora promotions get " + prom.ID + "` until status flips)")
		}
		return nil
	},
}

var promotionsGetCmd = &cobra.Command{
	Use:   "get [id]",
	Short: "Show a promotion's full state",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		prom, err := client.GetPromotion(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		if isJSON() {
			return printJSON(prom)
		}
		fields := []kv{
			field("ID", prom.ID),
			field("Version", prom.VersionID),
			field("Target", fmt.Sprintf("%s:%s", prom.TargetType, prom.TargetRef)),
			field("Status", prom.Status),
			field("Proposed by", prom.ProposedBy),
			field("Proposed at", prom.ProposedAt.Format("2006-01-02 15:04:05")),
		}
		if prom.EvalRunID != nil {
			fields = append(fields, field("Eval run", *prom.EvalRunID))
		}
		if prom.DecidedAt != nil {
			fields = append(fields, field("Decided at", prom.DecidedAt.Format("2006-01-02 15:04:05")))
		}
		if prom.ApproverUserID != nil {
			fields = append(fields, field("Approver", *prom.ApproverUserID))
		}
		if prom.Reason != "" {
			fields = append(fields, field("Reason", prom.Reason))
		}
		detail("Promotion", fields...)
		return nil
	},
}

var promotionsApproveCmd = &cobra.Command{
	Use:   "approve [id]",
	Short: "Approve a pending_approval promotion",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		prom, err := client.ApprovePromotion(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		if isJSON() {
			return printJSON(prom)
		}
		fmt.Printf("Approved %s — status: %s\n", prom.ID, prom.Status)
		return nil
	},
}

var promotionsRejectReason string

var promotionsRejectCmd = &cobra.Command{
	Use:   "reject [id]",
	Short: "Reject a pending_approval promotion",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if promotionsRejectReason == "" {
			return fmt.Errorf("--reason is required")
		}
		prom, err := client.RejectPromotion(cmd.Context(), args[0], promotionsRejectReason)
		if err != nil {
			return err
		}
		if isJSON() {
			return printJSON(prom)
		}
		fmt.Printf("Rejected %s — status: %s\n", prom.ID, prom.Status)
		return nil
	},
}

func init() {
	promotionsProposeCmd.Flags().StringVar(&promoteVersionID, "version-id", "", "Agent version UUID to promote (required)")
	promotionsProposeCmd.Flags().StringVar(&promoteTargetType, "target-type", "api", "Target kind (api, channel, …)")
	promotionsProposeCmd.Flags().StringVar(&promoteTargetRef, "target-ref", "", "Target-specific identifier")
	promotionsProposeCmd.MarkFlagRequired("version-id")

	promotionsRejectCmd.Flags().StringVar(&promotionsRejectReason, "reason", "", "Non-empty rejection reason (required)")
	promotionsRejectCmd.MarkFlagRequired("reason")

	promotionsCmd.AddCommand(promotionsListCmd)
	promotionsCmd.AddCommand(promotionsProposeCmd)
	promotionsCmd.AddCommand(promotionsGetCmd)
	promotionsCmd.AddCommand(promotionsApproveCmd)
	promotionsCmd.AddCommand(promotionsRejectCmd)
}
