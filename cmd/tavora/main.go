package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	tavora "github.com/tavora-ai/tavora-sdk-go"
)

var client *tavora.Client

var (
	flagAPIKey string
	flagURL    string
)

var rootCmd = &cobra.Command{
	Use:   "tavora",
	Short: "Tavora CLI — developer client for the Tavora API",
	Long: `Tavora CLI lets you interact with your Tavora project from the terminal.

Two workflows live here:

  Code-first authoring — author agents as files in a tavora/ folder
    tavora init      scaffold tavora/ with a starter agent
    tavora dev       watch + validate + sync a dev draft
    tavora deploy    promote the draft to a published version
    tavora config show <agent>   print resolved config

  API client — manage stores, documents, sessions, evals against
  your hosted Tavora project via X-API-Key auth.

Configuration precedence (highest to lowest):
  1. Command-line flags (--api-key, --url)
  2. Environment variables (TAVORA_API_KEY, TAVORA_URL)
  3. Config file (~/.tavora.yaml or TAVORA_CONFIG)

Run 'tavora login' to write credentials to the config file.`,
	SilenceUsage:  true,
	SilenceErrors: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// Skip client init for commands that can't talk to the server
		// at all (no API in scope, or local-only verbs).
		switch cmd.Name() {
		case "help", "completion", "version":
			return nil
		case "init", "login", "tui":
			// "tui" manages its own credentials (via internal/tui's
			// folder→~/.tavora.yaml→env→setup chain), so it
			// shouldn't fail here just because no API key was on the
			// flags. Same reasoning as "login".
			return nil
		case "session", "latest":
			// `tavora session …` reads from local disk only — no
			// API key required. `latest` is the subcommand name.
			return nil
		}
		// Subcommands with offline-only local-disk paths, scoped by
		// parent to avoid colliding with same-named commands that DO
		// need an API client. Two cases today:
		//   `tavora session get <id>` / `… list`
		//   `tavora config show <agent>` (reads tavora/ off disk)
		// Past bug: a bare `case "show"` in the switch above also
		// matched `tavora project show`, which DOES need the client
		// and was nil-deref'ing post-PreRun. The parent-keyed form
		// keeps the skip narrow to where it's actually safe.
		if cmd.Parent() != nil {
			switch cmd.Parent().Name() {
			case "session", "config":
				return nil
			}
		}

		url, key := resolveAPIConfig()
		if key == "" {
			// The code-first verbs `dev` and `deploy` can still do
			// useful local work (validate, --no-sync, --dry-run) when
			// no API key is configured. Leave client = nil and let
			// the verb decide whether that's fatal — `deploy` errors
			// out, `dev` prints a "sync skipped" status and keeps
			// watching files.
			if cmd.Name() == "dev" || cmd.Name() == "deploy" {
				return nil
			}
			return fmt.Errorf("no API key configured — set TAVORA_API_KEY, use --api-key, or run 'tavora login'")
		}

		// httpClientForDeployment wires the X-Tavora-Deployment header
		// through resty via SDK's WithHTTPClient option. The header is
		// the Convex-shape binding from tavora/.env.local — empty if
		// the user hasn't run `tavora init` yet, in which case the
		// server's deployment-resolver middleware falls back to the
		// project's prod deployment.
		client = tavora.NewClient(url, key, tavora.WithHTTPClient(httpClientForDeployment()))
		return nil
	},
}

func init() {
	rootCmd.PersistentFlags().StringVar(&flagAPIKey, "api-key", "", "API key (or TAVORA_API_KEY env var)")
	rootCmd.PersistentFlags().StringVar(&flagURL, "url", "", "Server URL (or TAVORA_URL env var, default: http://localhost:8080)")
	rootCmd.PersistentFlags().StringVar(&outputFormat, "output", "text", "Output format: text, json")
	rootCmd.PersistentFlags().BoolVarP(&quiet, "quiet", "q", false, "Suppress status messages, only output data")
	rootCmd.PersistentFlags().StringVar(&configPath, "config", "", "Config file path (default: ~/.tavora.yaml)")

	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(loginCmd)
	rootCmd.AddCommand(envCmd)
	rootCmd.AddCommand(appCmd)
	rootCmd.AddCommand(storesCmd)
	rootCmd.AddCommand(documentsCmd)
	rootCmd.AddCommand(searchCmd)
	rootCmd.AddCommand(chatCmd)
	rootCmd.AddCommand(agentsCmd)
	rootCmd.AddCommand(skillsCmd)
	rootCmd.AddCommand(templatesCmd)
	rootCmd.AddCommand(scheduleCmd)
	rootCmd.AddCommand(evalsCmd)
	rootCmd.AddCommand(ragEvalCmd)
	rootCmd.AddCommand(metricsCmd)
	rootCmd.AddCommand(studioCmd)
}

func main() {
	os.Exit(run())
}

// run is the int-returning entry point. main() wraps it so the
// process exits cleanly; cli_test.go's testscript runner wraps it
// so each scripted `tavora ...` invocation runs the binary with
// fresh global state (cobra's package-level flag vars survive
// across rootCmd.Execute calls, so reusing the binary in-process
// would leak --output / --api-key etc. between scripts).
func run() int {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", wrapError(err))
		return 1
	}
	return 0
}

// tryGetProjectSlug calls GET /api/sdk/project to look up the slug of the
// project the configured API key is bound to. Used by `tavora init` to
// fill the project name in tavora.jsonc when the user didn't pass
// --project explicitly, instead of using a brittle directory-name
// fallback. Returns "" on any failure (no api key, server
// unreachable, request errored) — the caller falls back to the
// directory-name path.
func tryGetProjectSlug() string {
	url, key := resolveAPIConfig()
	if key == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c := tavora.NewClient(url, key)
	project, err := c.GetProject(ctx)
	if err != nil {
		return ""
	}
	return project.Slug
}

// resolveAPIConfig returns (url, key) using the same precedence
// PersistentPreRunE applies: flag > env var > ~/.tavora.yaml > default.
// Empty key means "no credentials configured" — callers decide
// whether that's fatal (most CLI commands) or a soft skip
// (`tavora init` proceeds offline and just skips the cloud bind).
func resolveAPIConfig() (url, key string) {
	cfg := loadConfigFile()
	url = flagURL
	if url == "" {
		url = os.Getenv("TAVORA_URL")
	}
	if url == "" && cfg != nil {
		url = cfg.URL
	}
	if url == "" {
		url = "http://localhost:8080"
	}
	key = flagAPIKey
	if key == "" {
		key = os.Getenv("TAVORA_API_KEY")
	}
	if key == "" && cfg != nil {
		key = cfg.APIKey
	}
	return url, key
}
