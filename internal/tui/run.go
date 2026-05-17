// Package tui is the interactive Tavora terminal UI behind the
// `tavora tui` subcommand. Run is the single entry point — the
// cobra command in cmd/tavora/tui.go calls it with the flag values
// packed into Options, the rest of the package is package-internal.
//
// What the TUI does:
//
//   - Chat-with-an-agent surface (full-screen bubbletea, SSE-streamed
//     trace, multi-turn session, slash-commands for /upload etc.).
//   - When launched inside a tavora/ folder it auto-detects the
//     project, syncs once on startup, scopes the agent picker to the
//     folder's local agents, defaults the session target to the dev
//     draft, and downloads emitted assets into <project>/.assets/.
//   - When launched outside a tavora/ folder it falls back to the
//     legacy behavior: env / stored creds, cross-project picker,
//     target=live.
//
// The TUI never authors agents — admin work goes through the
// `tavora` CLI or the /platform web UI.
package tui

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	tavora "github.com/tavora-ai/tavora-sdk-go"
)

// Options controls how Run boots. All fields optional; the zero
// value reproduces the historical `tavora-tui` defaults plus
// folder-aware mode when a tavora/ folder is present at cwd.
type Options struct {
	// AgentFlag — `--agent`. Empty triggers auto-pick (single agent
	// in project / folder) or the interactive picker.
	AgentFlag string
	// SessionFlag — `--session`. When set, resume that session and
	// skip the agent picker. The session's pinned version drives
	// persona / skills.
	SessionFlag string
	// ResetConfig — `--reset-config`. Forces the setup TUI to run
	// even when stored credentials are valid.
	ResetConfig bool
	// ProjectDir — when set, treat this directory as the tavora/
	// folder root (skip the cwd walk-up). Empty = auto-detect.
	ProjectDir string
	// NoFolder — `--no-folder`. Disable folder-aware mode; behave
	// exactly like the pre-folder TUI. Useful for cross-project
	// browsing from inside a project directory.
	NoFolder bool
}

// Run starts the TUI. Returns once the bubbletea program exits or
// fails. On success the caller can pull the resume hint off stderr
// from printResumeHint. Designed to be called from either a flag-
// parsed main() (tavora-tui) or a cobra RunE (tavora tui).
func Run(opts Options) error {
	logPath, closeLog, err := SetupLogger()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: log setup failed: %v\n", err)
	} else {
		defer closeLog()
	}

	// Folder detection — sets folder=nil when --no-folder, when
	// there's no tavora.jsonc up the tree, or when source.Load
	// errors. We don't fail here because the legacy TUI surface
	// (cross-project chat) is still useful outside a folder.
	var folder *folderContext
	if !opts.NoFolder {
		folder = detectFolder(opts.ProjectDir)
		if folder != nil {
			slog.Info("folder mode", "root", folder.Root, "project", folder.Manifest.Project)
		}
	}

	cfg, ws, err := acquireConfigForFolder(opts.ResetConfig, folder)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		if logPath != "" {
			fmt.Fprintf(os.Stderr, "log:   %s\n", logPath)
		}
		return err
	}

	client := tavora.NewClient(cfg.URL, cfg.APIKey)

	// Folder pre-sync. Best-effort: a sync failure surfaces as a
	// warning but doesn't block chat — the agent picker will still
	// list whatever's on the server, and a stale draft is still a
	// valid target.
	if folder != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := folder.PreSync(ctx, client); err != nil {
			slog.Warn("pre-sync failed", "err", err)
			fmt.Fprintf(os.Stderr, "warning: pre-sync failed (%v); continuing with last-known server state\n", err)
		}
		cancel()
	}

	var resumeSession *tavora.AgentSession
	var agent *tavora.AgentConfig
	if id := strings.TrimSpace(opts.SessionFlag); id != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		detail, err := client.GetAgentSession(ctx, id)
		cancel()
		if err != nil {
			return fmt.Errorf("resuming session %s: %w", id, err)
		}
		s := detail.Session
		resumeSession = &s
		slog.Info("resuming session", "session", id)
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		agent, err = resolveAgentForFolder(ctx, client, strings.TrimSpace(opts.AgentFlag), folder)
		cancel()
		if err != nil {
			return err
		}
		slog.Info("bound to agent",
			"agent", agent.Name, "agent_id", agent.ID,
			"folder_mode", folder != nil,
		)
	}

	prog := tea.NewProgram(newMainModel(client, ws, logPath, resumeSession, agent, folder))
	finalModel, err := prog.Run()
	if err != nil {
		return fmt.Errorf("running TUI: %w", err)
	}
	mm, _ := finalModel.(mainModel)
	printResumeHint(mm.session)
	if mm.err != nil {
		return mm.err
	}
	return nil
}

// printResumeHint dumps the active session ID + the exact flag the
// user can pass next time, so they can pick up the conversation.
// Goes to stderr after Bubble Tea has restored the terminal so the
// message remains visible after exit.
func printResumeHint(session *tavora.AgentSession) {
	if session == nil {
		return
	}
	fmt.Fprintf(os.Stderr, "session: %s\n", session.ID)
	fmt.Fprintf(os.Stderr, "resume:  tavora tui --session %s\n", session.ID)
}

// acquireConfigForFolder resolves credentials. Order (same as the
// `tavora` CLI's flag > env > ~/.tavora.yaml > default chain, minus
// the flag layer which the TUI doesn't take):
//
//   - --reset-config              → setup TUI
//   - TAVORA_URL + TAVORA_API_KEY  → env wins outright when both set
//   - either env var + a file      → env overrides per-field, file fills the rest
//   - ~/.tavora.yaml (folder mode) → CLI's config (written by tavora login)
//   - TUI's own config (legacy)
//   - setup TUI
//
// Env vars are checked first so a one-off `TAVORA_API_KEY=… tavora tui`
// invocation overrides the stored file, matching the CLI's behavior and
// the universal "env beats config" convention. Pre-folder behavior is
// preserved: outside folder mode, ~/.tavora.yaml is skipped entirely.
func acquireConfigForFolder(forceSetup bool, folder *folderContext) (*Config, *tavora.Project, error) {
	if !forceSetup {
		// Env first. If both vars are set we go straight to validate;
		// if only one is, we'll merge it onto whichever file-based
		// config we find below.
		envURL := strings.TrimSpace(os.Getenv("TAVORA_URL"))
		envKey := strings.TrimSpace(os.Getenv("TAVORA_API_KEY"))
		if envURL != "" && envKey != "" {
			cfg := &Config{URL: envURL, APIKey: envKey}
			if ws, err := validate(cfg); err == nil {
				slog.Info("connected via env credentials", "project", ws.Name)
				return cfg, ws, nil
			} else {
				slog.Warn("env credentials rejected; falling back", "err", err)
			}
		}

		// Per-field merge helper: take the file's value and override
		// with env when set. So `TAVORA_API_KEY=... tavora tui` with
		// URL in ~/.tavora.yaml still picks the env key, and
		// vice-versa.
		merge := func(file *Config) *Config {
			out := *file
			if envURL != "" {
				out.URL = envURL
			}
			if envKey != "" {
				out.APIKey = envKey
			}
			return &out
		}

		// ~/.tavora.yaml — only consulted in folder mode (the user
		// has run `tavora login` for the CLI; reuse the same key).
		if folder != nil {
			if cli := loadCLIConfig(); cli != nil {
				cfg := merge(cli)
				if ws, err := validate(cfg); err == nil {
					if envURL != "" || envKey != "" {
						slog.Info("connected via ~/.tavora.yaml + env override", "project", ws.Name)
					} else {
						slog.Info("connected via ~/.tavora.yaml", "project", ws.Name)
					}
					return cfg, ws, nil
				} else {
					slog.Warn("~/.tavora.yaml credentials rejected; falling back", "err", err)
				}
			}
		}

		// TUI's own stored config (the file written by the setup TUI
		// on first run; legacy path).
		if stored, err := LoadConfig(); err == nil {
			cfg := merge(stored)
			if ws, err := validate(cfg); err == nil {
				if envURL != "" || envKey != "" {
					slog.Info("connected via stored config + env override", "project", ws.Name)
				} else {
					slog.Info("connected via stored credentials", "project", ws.Name)
				}
				return cfg, ws, nil
			} else {
				slog.Warn("stored credentials rejected; rerunning setup", "err", err)
			}
		}
	}

	seed, _ := LoadConfig()
	prog := tea.NewProgram(newSetupModel(seed))
	finalModel, err := prog.Run()
	if err != nil {
		return nil, nil, fmt.Errorf("running setup: %w", err)
	}
	sm, ok := finalModel.(setupModel)
	if !ok || sm.cfg == nil || sm.ws == nil {
		return nil, nil, fmt.Errorf("setup canceled")
	}
	return sm.cfg, sm.ws, nil
}

func validate(cfg *Config) (*tavora.Project, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return tavora.NewClient(cfg.URL, cfg.APIKey).GetProject(ctx)
}
