package main

import (
	"fmt"
	"os"

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
	Long: `Tavora CLI lets you interact with your Tavora app from the terminal.

Authenticate with an API key (flag, env var, or config file) to manage
stores, upload documents, search, chat, and run agents.

Configuration precedence (highest to lowest):
  1. Command-line flags (--api-key, --url)
  2. Environment variables (TAVORA_API_KEY, TAVORA_URL)
  3. Config file (~/.tavora.yaml or TAVORA_CONFIG)

Run 'tavora init' to create a config file interactively.`,
	SilenceUsage:  true,
	SilenceErrors: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// Skip client init for commands that don't need it
		if cmd.Name() == "help" || cmd.Name() == "completion" || cmd.Name() == "init" || cmd.Name() == "version" {
			return nil
		}

		// Load config file as baseline
		cfg := loadConfigFile()

		// Resolve URL: flag > env > config > default
		url := flagURL
		if url == "" {
			url = os.Getenv("TAVORA_URL")
		}
		if url == "" && cfg != nil {
			url = cfg.URL
		}
		if url == "" {
			url = "http://localhost:8080"
		}

		// Resolve API key: flag > env > config
		key := flagAPIKey
		if key == "" {
			key = os.Getenv("TAVORA_API_KEY")
		}
		if key == "" && cfg != nil {
			key = cfg.APIKey
		}
		if key == "" {
			return fmt.Errorf("no API key configured — set TAVORA_API_KEY, use --api-key, or run 'tavora init'")
		}

		client = tavora.NewClient(url, key)
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
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(appCmd)
	rootCmd.AddCommand(storesCmd)
	rootCmd.AddCommand(documentsCmd)
	rootCmd.AddCommand(searchCmd)
	rootCmd.AddCommand(chatCmd)
	rootCmd.AddCommand(agentsCmd)
	rootCmd.AddCommand(skillsCmd)
	rootCmd.AddCommand(templatesCmd)
	rootCmd.AddCommand(mcpCmd)
	rootCmd.AddCommand(promotionsCmd)
	rootCmd.AddCommand(schedulesCmd)
	rootCmd.AddCommand(evalsCmd)
	rootCmd.AddCommand(ragEvalCmd)
	rootCmd.AddCommand(metricsCmd)
	rootCmd.AddCommand(studioCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", wrapError(err))
		os.Exit(1)
	}
}
