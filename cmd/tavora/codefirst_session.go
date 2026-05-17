package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/tavora-ai/tavora-cli/internal/codefirst/runs"
)

// `tavora session` reads session logs out of `tavora/.runs/`. The
// AI verification loop expects this to be a no-network operation —
// the run already happened, the markdown is on disk, just print it.

var sessionDir string

var codefirstSessionCmd = &cobra.Command{
	Use:   "session",
	Short: "Inspect session logs written under tavora/.runs/",
}

var codefirstSessionLatestCmd = &cobra.Command{
	Use:   "latest",
	Short: "Print the most-recent session log to stdout",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := resolveRunsDir(sessionDir)
		if err != nil {
			return err
		}
		path, err := runs.LatestSession(dir)
		if err != nil {
			return err
		}
		return printFile(path)
	},
}

var codefirstSessionGetCmd = &cobra.Command{
	Use:   "get [id-or-filename]",
	Short: "Print a session log by id, partial id, or filename",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := resolveRunsDir(sessionDir)
		if err != nil {
			return err
		}
		path, err := runs.ResolveSession(dir, args[0])
		if err != nil {
			return err
		}
		return printFile(path)
	},
}

var codefirstSessionListCmd = &cobra.Command{
	Use:   "list",
	Short: "List session log filenames under .runs/ (newest last)",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := resolveRunsDir(sessionDir)
		if err != nil {
			return err
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("no session logs yet — run `tavora run <agent> \"<input>\"` first")
			}
			return err
		}
		// ReadDir order is filesystem-dependent; rely on the
		// timestamp-prefixed filenames to give a stable sort.
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) != ".md" {
				continue
			}
			fmt.Println(e.Name())
		}
		return nil
	},
}

func resolveRunsDir(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	p, err := loadProjectOrFail("")
	if err != nil {
		return "", err
	}
	return filepath.Join(p.Root, ".runs"), nil
}

func printFile(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	os.Stdout.Write(b) //nolint:errcheck
	if len(b) > 0 && b[len(b)-1] != '\n' {
		fmt.Println()
	}
	return nil
}

func init() {
	codefirstSessionCmd.PersistentFlags().StringVar(&sessionDir, "dir", "", "Override the runs/ directory (default: <project>/.runs)")
	codefirstSessionCmd.AddCommand(codefirstSessionLatestCmd)
	codefirstSessionCmd.AddCommand(codefirstSessionGetCmd)
	codefirstSessionCmd.AddCommand(codefirstSessionListCmd)
	rootCmd.AddCommand(codefirstSessionCmd)
}
