package main

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
)

// outputFormat is set by the --output flag on the root command.
var outputFormat string

// quiet suppresses non-essential output (status messages, headers).
var quiet bool

// status prints a message to stderr unless --quiet is set.
// Use for progress/status messages that aren't primary data output.
func status(format string, args ...any) {
	if !quiet {
		fmt.Fprintf(os.Stderr, format+"\n", args...)
	}
}

// printJSON encodes v as indented JSON to stdout.
func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// table writes rows as an aligned text table.
// headers are printed first, then each row.
type table struct {
	w       *tabwriter.Writer
	headers []string
}

func newTable(headers ...string) *table {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	t := &table{w: w, headers: headers}
	if len(headers) > 0 {
		for i, h := range headers {
			if i > 0 {
				fmt.Fprint(w, "\t")
			}
			fmt.Fprint(w, h)
		}
		fmt.Fprintln(w)
	}
	return t
}

func (t *table) row(cols ...string) {
	for i, c := range cols {
		if i > 0 {
			fmt.Fprint(t.w, "\t")
		}
		fmt.Fprint(t.w, c)
	}
	fmt.Fprintln(t.w)
}

func (t *table) flush() error {
	return t.w.Flush()
}

// detail prints key-value pairs in a formatted detail view.
func detail(title string, pairs ...kv) {
	fmt.Println(title)
	for _, p := range pairs {
		fmt.Printf("  %-14s %s\n", p.key+":", p.value)
	}
}

type kv struct {
	key   string
	value string
}

func field(key, value string) kv {
	return kv{key: key, value: value}
}

// formatSize returns a human-readable size string.
func formatSize(bytes int64) string {
	switch {
	case bytes >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(1<<20))
	case bytes >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

// isJSON returns true when the user requested JSON output.
func isJSON() bool {
	return outputFormat == "json"
}
