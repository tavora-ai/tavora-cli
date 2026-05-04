package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Set via ldflags at build time:
//
//	go build -ldflags "-X main.version=v0.1.0 -X main.commit=abc123 -X main.buildDate=2024-01-01"
var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show CLI version information",
	Run: func(cmd *cobra.Command, args []string) {
		if isJSON() {
			printJSON(map[string]string{ //nolint:errcheck
				"version":    version,
				"commit":     commit,
				"build_date": buildDate,
			})
			return
		}
		fmt.Printf("tavora %s (commit: %s, built: %s)\n", version, commit, buildDate)
	},
}
