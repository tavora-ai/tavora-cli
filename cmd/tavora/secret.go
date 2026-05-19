package main

import (
	"errors"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// secretCmd ("tavora secret …") is the sibling of envCmd for the
// secret-class entries on the same deployment KV store. Same
// backing table; the is_secret flag toggles UI redaction and
// trace-redactor inclusion at runtime.
//
// Why two commands over one store. The split is intent — a
// developer typing `tavora secret put STRIPE_KEY …` is signaling
// "this is sensitive" in a way `tavora env put … --secret` doesn't
// communicate at read time. Plus `tavora secret list` filters to
// just secrets so the operator's "what secrets do I have on this
// deployment" question has a clean answer.
//
// Resolution rules (--deployment flag → TAVORA_DEPLOYMENT → .env.local)
// are inherited from envCmd's persistent flag.
var secretCmd = &cobra.Command{
	Use:   "secret",
	Short: "Manage per-deployment secrets (sensitive entries)",
	Long: `Set, list, get, and delete secret-class entries on the deployment.

Source-sync substitutes ${secret.NAME} in agent.jsonc against
entries here. Same KV store as ` + "`tavora env`" + `; the difference is
that secret-class entries are hidden in the UI and the trace
redactor strips their resolved values from event payloads at
runtime.`,
}

var secretPutCmd = &cobra.Command{
	Use:   "put <key> <value>",
	Short: "Set a secret-class entry. Flagged for UI redaction + trace stripping.",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		return putDeploymentEnv(cmd, args, true)
	},
}

var secretGetCmd = &cobra.Command{
	Use:   "get <key>",
	Short: "Print one secret's plaintext value to stdout (no newline)",
	Long: `Print one entry's plaintext value to stdout, no trailing newline.

Identical to ` + "`tavora env get`" + ` — the kind controls runtime
display + redaction, not whether the value is retrievable. Provided
as a sibling command so secret-handling scripts read naturally:

  export STRIPE_KEY=$(tavora secret get STRIPE_KEY)`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return getDeploymentEnv(cmd, args)
	},
}

var secretListCmd = &cobra.Command{
	Use:   "list",
	Short: "List only the secret-class entries on the current deployment",
	RunE: func(cmd *cobra.Command, args []string) error {
		if client == nil {
			return errors.New("no API client configured — run `tavora login`")
		}
		slug, err := resolveEnvDeploymentSlug()
		if err != nil {
			return err
		}
		entries, err := client.ListDeploymentEnv(cmd.Context(), slug)
		if err != nil {
			return err
		}
		// Filter to secrets only — the env list shows everything;
		// `secret list` answers the narrower question.
		filtered := entries[:0:0]
		for _, e := range entries {
			if e.IsSecret {
				filtered = append(filtered, e)
			}
		}
		if isJSON() {
			return printJSON(map[string]any{
				"deployment": slug,
				"entries":    filtered,
			})
		}
		if len(filtered) == 0 {
			fmt.Fprintf(os.Stderr, "No secrets on deployment %q yet.\n\n", slug)
			fmt.Fprintln(os.Stderr, "Add one with:")
			fmt.Fprintln(os.Stderr, "  tavora secret put <KEY> <value>")
			return nil
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "KEY\tUPDATED")
		for _, e := range filtered {
			fmt.Fprintf(w, "%s\t%s\n", e.Key, e.UpdatedAt)
		}
		return w.Flush()
	},
}

var secretDeleteCmd = &cobra.Command{
	Use:   "delete <key>",
	Short: "Remove one secret. Idempotent.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return deleteDeploymentEnv(cmd, args)
	},
}

func init() {
	// Reuse env's --deployment flag rather than declaring our own —
	// the resolution rules MUST stay in lockstep across the two
	// command trees, and two persistent flags over the same struct
	// var would just be confusing.
	secretCmd.PersistentFlags().AddFlag(envCmd.PersistentFlags().Lookup("deployment"))

	secretCmd.AddCommand(secretPutCmd)
	secretCmd.AddCommand(secretGetCmd)
	secretCmd.AddCommand(secretListCmd)
	secretCmd.AddCommand(secretDeleteCmd)
}
