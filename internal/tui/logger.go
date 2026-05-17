package tui

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// maxLogFiles caps how many per-session log files are retained. Each
// run writes to a fresh file named agent-tui-YYYYMMDD-HHMMSS.log; on
// startup we prune the oldest until at most maxLogFiles - 1 remain
// (the new file makes maxLogFiles total).
const maxLogFiles = 10

// logsDir resolves the directory holding per-session log files. The
// path mirrors the config dir convention used by config.go so the user
// only has one place to look.
func logsDir() (string, error) {
	if v := os.Getenv("TAVORA_TUI_LOG_DIR"); v != "" {
		return v, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "tavora", "logs"), nil
}

// SetupLogger opens a per-session log file, installs it as the default
// slog handler, prunes older sessions, and returns the file path plus a
// close function the caller defers.
//
// Per-session (rather than rotating) was chosen because the alt-screen
// TUI swallows stderr noise — having a separate, append-only artifact
// per run is friendlier than parsing a multi-run log when debugging
// "what happened during my last attempt".
func SetupLogger() (string, func() error, error) {
	dir, err := logsDir()
	if err != nil {
		return "", nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", nil, fmt.Errorf("create log dir: %w", err)
	}
	pruneLogs(dir, maxLogFiles-1)

	name := fmt.Sprintf("agent-tui-%s.log", time.Now().Format("20060102-150405"))
	path := filepath.Join(dir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return "", nil, fmt.Errorf("open log: %w", err)
	}

	h := slog.NewTextHandler(f, &slog.HandlerOptions{Level: slog.LevelDebug})
	slog.SetDefault(slog.New(h))
	slog.Info("agent-tui started", "pid", os.Getpid(), "log", path)

	return path, f.Close, nil
}

func pruneLogs(dir string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type item struct {
		path string
		mod  time.Time
	}
	var files []item
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".log" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, item{filepath.Join(dir, e.Name()), info.ModTime()})
	}
	if len(files) <= keep {
		return
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	for _, f := range files[keep:] {
		_ = os.Remove(f.path)
	}
}
