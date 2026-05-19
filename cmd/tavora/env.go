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

// envCmd ("tavora env ...") manages non-secret entries on the
// deployment's KV store — the values agent.jsonc references as
// ${env.NAME}. For sensitive values use `tavora secret …` instead;
// it's the same backing store with the is_secret flag flipped, but
// the command split keeps the mental model clean (env = config,
// secret = credential).
var envCmd = &cobra.Command{
	Use:   "env",
	Short: "Manage per-deployment env vars (non-secret config)",
	Long: `Set, list, get, and delete env-class entries on the deployment.

Source-sync substitutes ${env.NAME} in agent.jsonc against entries
here. Use ` + "`tavora secret`" + ` for the same operations on secret-
class values — same store, different display + redaction policy.

Deployment selection follows the same precedence as the rest of the
CLI:

  --deployment <slug> flag
  TAVORA_DEPLOYMENT env var
  tavora/.env.local or .env.local in the cwd
  (no binding → server falls back to the project's prod deployment)`,
}

var envSlugFlag string

// resolveEnvDeploymentSlug picks the slug the env/secret subcommands
// should operate against. Shared by both command trees so the
// resolution rules stay in one place.
func resolveEnvDeploymentSlug() (string, error) {
	slug := strings.TrimSpace(envSlugFlag)
	if slug == "" {
		slug = loadDeploymentSlug()
	}
	if slug == "" {
		return "", errors.New("no deployment binding — pass --deployment <slug>, set TAVORA_DEPLOYMENT, or run `tavora init` to write tavora/.env.local")
	}
	if i := strings.Index(slug, ":"); i >= 0 {
		slug = slug[i+1:]
	}
	return slug, nil
}

// kindLabel renders the is_secret bool as a short label for table
// output. Used by env list (shows both kinds) and secret list
// (where the column is always "secret" but we keep the shape
// uniform for scripting).
func kindLabel(isSecret bool) string {
	if isSecret {
		return "secret"
	}
	return "env"
}

var envListCmd = &cobra.Command{
	Use:   "list",
	Short: "List entries on the current deployment (keys + kind; values NOT shown)",
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
		if isJSON() {
			// JSON output always includes the (possibly empty) array
			// so scripts can iterate without special-casing "no
			// entries." Same shape regardless of length.
			return printJSON(map[string]any{
				"deployment": slug,
				"entries":    entries,
			})
		}
		if len(entries) == 0 {
			fmt.Fprintf(os.Stderr, "No entries on deployment %q yet.\n\n", slug)
			fmt.Fprintln(os.Stderr, "Add one with:")
			fmt.Fprintln(os.Stderr, "  tavora env put <KEY> <value>")
			fmt.Fprintln(os.Stderr, "  tavora secret put <KEY> <value>   # encrypted-display, trace-redacted")
			return nil
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "KEY\tKIND\tUPDATED")
		for _, e := range entries {
			fmt.Fprintf(w, "%s\t%s\t%s\n", e.Key, kindLabel(e.IsSecret), e.UpdatedAt)
		}
		return w.Flush()
	},
}

var envPutCmd = &cobra.Command{
	Use:   "put <key> <value>",
	Short: "Set an env-class entry (non-secret). For sensitive values use `tavora secret put`.",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		return putDeploymentEnv(cmd, args, false)
	},
}

// envGetCmd writes the plaintext value to stdout with no trailing
// newline (operator-controlled — caller does `tavora env get FOO`
// vs `tavora env get FOO | head -c 200`). Stderr carries any
// informational text so shell substitution captures only the value.
var envGetCmd = &cobra.Command{
	Use:   "get <key>",
	Short: "Print one entry's plaintext value to stdout (works for both env and secret kinds)",
	Long: `Print one entry's plaintext value to stdout, no trailing newline.

Intended for shell substitution:

  export STRIPE_KEY=$(tavora secret get STRIPE_KEY)

Works against both env and secret kinds — the kind controls
UI/trace redaction, not whether the value is retrievable. The
API-key auth that gates this is the same gate that put the value
there.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return getDeploymentEnv(cmd, args)
	},
}

var envDeleteCmd = &cobra.Command{
	Use:   "delete <key>",
	Short: "Remove one entry from the current deployment. Idempotent.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return deleteDeploymentEnv(cmd, args)
	},
}

// putDeploymentEnv is the shared write path for both `env put` and
// `secret put`; the caller fixes is_secret. Extracted so the two
// command trees stay thin wrappers — the intent flag is the only
// thing they decide.
//
// JSON output prints the redacted entry returned by the server
// (key + is_secret + timestamps; never the value). Text output is
// a one-line confirmation on stderr so $(tavora env put …) capture
// is empty.
func putDeploymentEnv(cmd *cobra.Command, args []string, isSecret bool) error {
	if client == nil {
		return errors.New("no API client configured — run `tavora login`")
	}
	slug, err := resolveEnvDeploymentSlug()
	if err != nil {
		return err
	}
	key, value := args[0], args[1]
	red, err := client.PutDeploymentEnv(cmd.Context(), slug, key, tavora.SetDeploymentEnvInput{
		Value:    value,
		IsSecret: isSecret,
	})
	if err != nil {
		return err
	}
	if isJSON() {
		return printJSON(red)
	}
	fmt.Fprintf(os.Stderr, "set %s.%s on deployment %s\n", kindLabel(isSecret), key, slug)
	return nil
}

// getDeploymentEnv prints one entry's value.
//
// Text output: just the plaintext, no newline — matches `git config
// --get` so shell substitution captures clean. The whole point of
// `tavora env get` is `$(tavora env get FOO)`.
//
// JSON output: the full DeploymentEnvValue (key + value +
// is_secret) so scripted callers can branch on the kind. The value
// is plaintext in both modes — the only redaction-aware mode here
// is the text mode's "no newline" convention.
func getDeploymentEnv(cmd *cobra.Command, args []string) error {
	if client == nil {
		return errors.New("no API client configured — run `tavora login`")
	}
	slug, err := resolveEnvDeploymentSlug()
	if err != nil {
		return err
	}
	res, err := client.GetDeploymentEnv(cmd.Context(), slug, args[0])
	if err != nil {
		return err
	}
	if isJSON() {
		return printJSON(res)
	}
	_, _ = os.Stdout.WriteString(res.Value)
	return nil
}

// deleteDeploymentEnv removes one entry.
//
// JSON output: a tiny ack object so scripts can check status
// without grepping stderr. Text output: the same one-line stderr
// confirmation as put.
func deleteDeploymentEnv(cmd *cobra.Command, args []string) error {
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
	if isJSON() {
		return printJSON(map[string]any{
			"deployment": slug,
			"key":        key,
			"deleted":    true,
		})
	}
	fmt.Fprintf(os.Stderr, "removed %s from deployment %s\n", key, slug)
	return nil
}

func init() {
	envCmd.PersistentFlags().StringVar(&envSlugFlag, "deployment", "",
		"deployment slug (overrides TAVORA_DEPLOYMENT and tavora/.env.local)")

	envCmd.AddCommand(envListCmd)
	envCmd.AddCommand(envPutCmd)
	envCmd.AddCommand(envGetCmd)
	envCmd.AddCommand(envDeleteCmd)
}
