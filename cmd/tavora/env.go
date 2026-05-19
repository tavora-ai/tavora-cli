package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	tavora "github.com/tavora-ai/tavora-sdk-go"
)

// envCmd ("tavora env ...") manages the per-deployment KV store
// that backs ${env.X} and ${secret.X} resolution in agent.jsonc at
// source-sync time. Owned by the agent developer — same person who
// runs `tavora dev` and authored the placeholder in the first place.
var envCmd = &cobra.Command{
	Use:   "env",
	Short: "Manage per-deployment env vars and secrets",
	Long: `Set, list, and delete values on a deployment's KV store.

When source-sync sees ${env.NAME} or ${secret.NAME} in agent.jsonc, it
substitutes against entries here. Both are encrypted at rest;
--secret toggles UI display + trace redaction.

Deployment selection follows the same precedence as the rest of the
CLI:

  --deployment <slug> flag
  TAVORA_DEPLOYMENT env var
  tavora/.env.local or .env.local in the cwd
  (no binding → server falls back to the project's prod deployment)`,
}

// envSlugFlag — explicit override for the deployment binding. Falls
// back to loadDeploymentSlug() (env var / .env.local) when empty,
// matching how the rest of the CLI resolves the deployment context.
var envSlugFlag string

// resolveEnvDeploymentSlug picks the slug the env subcommand should
// operate against. Stripping the kind: prefix (dev:foo → foo)
// matches the server's deployment-resolver middleware so the user
// can paste the same value they put in .env.local without
// thinking about the format.
func resolveEnvDeploymentSlug() (string, error) {
	slug := strings.TrimSpace(envSlugFlag)
	if slug == "" {
		slug = loadDeploymentSlug()
	}
	if slug == "" {
		return "", errors.New("no deployment binding — pass --deployment <slug>, set TAVORA_DEPLOYMENT, or run `tavora init` to write tavora/.env.local")
	}
	// Strip the optional kind: prefix the .env.local format allows.
	if i := strings.Index(slug, ":"); i >= 0 {
		slug = slug[i+1:]
	}
	return slug, nil
}

var envListCmd = &cobra.Command{
	Use:   "list",
	Short: "List entries on the current deployment (keys + is_secret flag; values NOT shown)",
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
		if len(entries) == 0 {
			fmt.Fprintf(os.Stderr, "No entries on deployment %q yet.\n\n", slug)
			fmt.Fprintln(os.Stderr, "Add one with:")
			fmt.Fprintln(os.Stderr, "  tavora env put <KEY> <value>          # plaintext env")
			fmt.Fprintln(os.Stderr, "  tavora env put <KEY> <value> --secret # encrypted, redacted")
			return nil
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "KEY\tIS_SECRET\tUPDATED")
		for _, e := range entries {
			tag := "no"
			if e.IsSecret {
				tag = "yes"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\n", e.Key, tag, e.UpdatedAt)
		}
		return w.Flush()
	},
}

var envPutIsSecret bool

var envPutCmd = &cobra.Command{
	Use:   "put <key> <value>",
	Short: "Set (or rotate) one entry on the current deployment. --secret flips the redaction flag.",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if client == nil {
			return errors.New("no API client configured — run `tavora login`")
		}
		slug, err := resolveEnvDeploymentSlug()
		if err != nil {
			return err
		}
		key, value := args[0], args[1]
		_, err = client.PutDeploymentEnv(cmd.Context(), slug, key, tavora.SetDeploymentEnvInput{
			Value:    value,
			IsSecret: envPutIsSecret,
		})
		if err != nil {
			return err
		}
		tag := "env"
		if envPutIsSecret {
			tag = "secret"
		}
		fmt.Fprintf(os.Stderr, "set %s.%s on deployment %s\n", tag, key, slug)
		return nil
	},
}

var envDeleteCmd = &cobra.Command{
	Use:   "delete <key>",
	Short: "Remove one entry from the current deployment. Idempotent.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if client == nil {
			return errors.New("no API client configured — run `tavora login`")
		}
		slug, err := resolveEnvDeploymentSlug()
		if err != nil {
			return err
		}
		key := args[0]
		if err := client.DeleteDeploymentEnv(cmd.Context(), slug, key); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "removed %s from deployment %s\n", key, slug)
		return nil
	},
}

func init() {
	envCmd.PersistentFlags().StringVar(&envSlugFlag, "deployment", "",
		"deployment slug (overrides TAVORA_DEPLOYMENT and tavora/.env.local)")
	envPutCmd.Flags().BoolVar(&envPutIsSecret, "secret", false,
		"mark as sensitive: UI redacts, trace redactor includes the resolved value at runtime")

	envCmd.AddCommand(envListCmd)
	envCmd.AddCommand(envPutCmd)
	envCmd.AddCommand(envDeleteCmd)
}
