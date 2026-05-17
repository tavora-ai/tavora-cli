package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tavora-ai/tavora-tools/internal/codefirst/source"
)

// tavora open <ref> — resolves a server-side resource name to a
// local file path and opens it in $EDITOR. Pairs with the
// <SourcePathChip> component on the web UI: a Convex-style "Defined
// in <file>" pointer that the user can both copy (chip) and open
// (CLI). See docs/ui-rethink-plan.md §6.
//
// Supported reference shapes:
//
//   tavora open <agent-id>                 → agents/<id>/agent.jsonc
//   tavora open <agent-id>/persona         → agents/<id>/persona.md
//   tavora open <agent-id>/skills          → agents/<id>/skills/
//   tavora open <agent-id>/skills/<file>   → agents/<id>/skills/<file>
//   tavora open <agent-id>/evals           → agents/<id>/evals/
//   tavora open <agent-id>/evals/<name>    → agents/<id>/evals/<name>.json
//   tavora open <agent-id>/agent.jsonc     → agents/<id>/agent.jsonc  (alias)
//
// The verb takes a local tavora/ folder context (defaults to walking
// up from cwd, like dev/deploy). It does not contact the server —
// the chip already knows the path; this is a pure filesystem +
// $EDITOR exec. When the resolved path doesn't exist locally, the
// verb prints the suggested path and exits 1 so the user knows
// either to run `tavora dev --once` (sync from server) or that the
// chip references a future file.

var openDir string

var codefirstOpenCmd = &cobra.Command{
	Use:   "open <ref>",
	Short: "Open the source file behind a SourcePathChip in $EDITOR",
	Long: `tavora open resolves a server-side resource name into a local
file path and opens it in your editor.

The web UI's "Defined in" chips point at files inside the local
tavora/ folder. Copying the path works in the browser; this verb
takes the same reference and opens it — handy when iterating on
agent.jsonc from a Cmd+K spotlight match or an audit-log link.

Examples:
  tavora open support                 # agents/support/agent.jsonc
  tavora open support/persona         # agents/support/persona.md
  tavora open support/skills          # agents/support/skills/
  tavora open support/evals/greeting  # agents/support/evals/greeting.json

The verb reads $EDITOR. Fall back is "vi". Use --print to just
echo the resolved path without launching anything.`,
	Args: cobra.ExactArgs(1),
	RunE: runOpen,
}

var openPrintOnly bool

func runOpen(cmd *cobra.Command, args []string) error {
	root, err := resolveProjectRoot(openDir)
	if err != nil {
		return err
	}

	target, err := resolveReference(root, args[0])
	if err != nil {
		return err
	}

	if openPrintOnly {
		fmt.Println(target)
		return nil
	}

	if _, err := os.Stat(target); err != nil {
		// Existence check is informational — print the expected path
		// so the user can copy it into their editor manually, then
		// return an error so scripts can detect the miss.
		fmt.Fprintf(os.Stderr, "tavora open: %s does not exist locally\n", target)
		fmt.Fprintf(os.Stderr, "  - run `tavora dev --once` to sync the latest source from the server, or\n")
		fmt.Fprintf(os.Stderr, "  - the chip points at a future file (create it and re-run tavora dev)\n")
		return fmt.Errorf("file not found: %s", target)
	}

	editor := pickEditor()
	c := exec.Command(editor, target)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		return fmt.Errorf("running %s %s: %w", editor, target, err)
	}
	return nil
}

// resolveProjectRoot walks up from cwd (or the --dir flag) to the
// nearest tavora.jsonc and returns the directory containing it.
// Uses source.Load (the same entrypoint dev/deploy take) which
// performs the upward walk for free. Falls back to <cwd>/tavora
// when no manifest is found so the verb can still print or fail
// loudly with a useful path.
func resolveProjectRoot(dir string) (string, error) {
	if dir != "" {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return "", err
		}
		return abs, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	p, err := source.Load(cwd)
	if err != nil {
		// No manifest yet — fall back to ./tavora so users iterating
		// on agent files in a freshly-scaffolded folder can still
		// use the verb without --dir.
		return filepath.Join(cwd, "tavora"), nil
	}
	return p.Root, nil
}

// resolveReference maps the chip-style reference to a repo-relative
// path under the project root. Returns the absolute file path.
func resolveReference(root, ref string) (string, error) {
	if ref == "" {
		return "", fmt.Errorf("ref is required (e.g. <agent-id>, <agent-id>/persona)")
	}

	parts := strings.Split(strings.Trim(ref, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		return "", fmt.Errorf("invalid ref: %q", ref)
	}

	agent := parts[0]
	agentDir := filepath.Join(root, "agents", agent)
	rest := parts[1:]

	// Bare agent id → agent.jsonc.
	if len(rest) == 0 {
		return filepath.Join(agentDir, "agent.jsonc"), nil
	}

	switch rest[0] {
	case "agent.jsonc", "config":
		return filepath.Join(agentDir, "agent.jsonc"), nil
	case "persona", "persona.md":
		return filepath.Join(agentDir, "persona.md"), nil
	case "skills":
		if len(rest) == 1 {
			return filepath.Join(agentDir, "skills"), nil
		}
		// Allow trailing .js / .md / .json; default to the literal
		// joined path so power users can name a specific file.
		return filepath.Join(agentDir, "skills", filepath.Join(rest[1:]...)), nil
	case "evals":
		if len(rest) == 1 {
			return filepath.Join(agentDir, "evals"), nil
		}
		// If the user didn't give an extension, assume .json since
		// that's the only authored extension in evals/.
		name := filepath.Join(rest[1:]...)
		if filepath.Ext(name) == "" {
			name += ".json"
		}
		return filepath.Join(agentDir, "evals", name), nil
	}

	// Unknown subpath — pass through so e.g. tavora open
	// support/notes.md works for non-canonical files the user added.
	return filepath.Join(agentDir, filepath.Join(rest...)), nil
}

func pickEditor() string {
	if e := os.Getenv("VISUAL"); e != "" {
		return e
	}
	if e := os.Getenv("EDITOR"); e != "" {
		return e
	}
	return "vi"
}

func init() {
	codefirstOpenCmd.Flags().StringVar(&openDir, "dir", "", "Project directory containing tavora.jsonc (default: search up from cwd)")
	codefirstOpenCmd.Flags().BoolVar(&openPrintOnly, "print", false, "Print the resolved path without launching an editor")
	rootCmd.AddCommand(codefirstOpenCmd)
}
