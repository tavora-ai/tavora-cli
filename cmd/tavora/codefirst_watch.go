package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/tavora-ai/tavora-tools/internal/codefirst/source"
	"github.com/tavora-ai/tavora-tools/internal/codefirst/validate"
)

// watchAndSync is the long-running loop behind `tavora dev`. It
// watches the project root recursively (one fsnotify.Watcher with
// per-directory Adds), debounces bursts of file events at ~250ms,
// reloads the project, validates, and (when not in --no-sync)
// syncs. Sigterm/Sigint break the loop.
func watchAndSync(initial *source.Project, noSync bool, verbose bool) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("watcher: %w", err)
	}
	defer w.Close()

	if err := addRecursive(w, initial.Root); err != nil {
		return err
	}

	status("watching %s (ctrl-c to stop)", initial.Root)

	debounce := time.NewTimer(0)
	if !debounce.Stop() {
		<-debounce.C
	}

	pending := false
	for {
		select {
		case ev, ok := <-w.Events:
			if !ok {
				return nil
			}
			if shouldIgnore(ev.Name) {
				continue
			}
			// New folders need to be watched too.
			if ev.Op&fsnotify.Create != 0 {
				if info, err := os.Stat(ev.Name); err == nil && info.IsDir() {
					_ = addRecursive(w, ev.Name)
				}
			}
			pending = true
			debounce.Reset(250 * time.Millisecond)
		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			status("watch error: %v", err)
		case <-debounce.C:
			if !pending {
				continue
			}
			pending = false
			fmt.Println("\n--- change detected ---")
			p, loadErr := source.Load(initial.Root)
			if loadErr != nil {
				status("load failed: %v", loadErr)
				continue
			}
			if err := runValidateAndSync(p, noSync, verbose); err != nil {
				status("%v", err)
				continue
			}
		}
	}
}

func addRecursive(w *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // best-effort
		}
		if !d.IsDir() {
			return nil
		}
		base := d.Name()
		if base == ".runs" || base == ".git" || base == "node_modules" {
			return fs.SkipDir
		}
		return w.Add(path)
	})
}

func shouldIgnore(path string) bool {
	base := filepath.Base(path)
	if strings.HasPrefix(base, ".") && !strings.HasSuffix(base, ".jsonc") && !strings.HasSuffix(base, ".json") && !strings.HasSuffix(base, ".md") && !strings.HasSuffix(base, ".js") {
		// editor swap/temp files
		return true
	}
	if strings.Contains(path, "/.runs/") || strings.Contains(path, "/.git/") {
		return true
	}
	ext := strings.ToLower(filepath.Ext(base))
	if ext == ".swp" || ext == ".swx" || ext == ".tmp" || ext == "" {
		// no extension catches editor temp files like 4913, vim ~ files
		// — directories also fall through here but we handled them above
	}
	return false
}

// printIssues writes validation issues to stdout in a stable,
// AI-friendly format. Fatal first, then warnings.
func printIssues(p *source.Project, issues []validate.Issue) {
	if len(issues) == 0 {
		return
	}
	fatal := 0
	warn := 0
	for _, i := range issues {
		if i.Severity == validate.Fatal {
			fatal++
		} else {
			warn++
		}
	}
	for _, i := range issues {
		tag := "fatal"
		if i.Severity == validate.Warn {
			tag = " warn"
		}
		fmt.Fprintf(os.Stderr, "[%s] %s\n", tag, i.Issue.String())
	}
	fmt.Fprintf(os.Stderr, "\n%d issue(s): %d fatal, %d warning\n", len(issues), fatal, warn)
}
