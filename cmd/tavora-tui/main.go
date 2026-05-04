// Agent TUI — an interactive terminal UI for talking to a Tavora agent.
//
// First run captures TAVORA_URL and TAVORA_API_KEY through a setup
// screen, validates them by calling GetWorkspace, and persists the
// result under the user's config dir. Subsequent runs go straight to
// the chat surface: scrolling output on top, prompt input on the
// bottom, live SSE streaming of tool calls / JS execution / responses.
//
// This example is intentionally end-to-end: it shows config bootstrap,
// API-key scoping, agent session creation, multi-turn dialogue with a
// shared session, and Bubble Tea integration with the SDK's callback-
// based RunAgent stream — useful as a reference when building a Go
// product on top of the Tavora SDK.
//
// Usage:
//
//	go run .                              # interactive setup; pick an agent
//	TAVORA_URL=... TAVORA_API_KEY=... go run .
//	go run . --agent <id-or-name>         # bind to a specific agent
//	go run . --reset-config               # rerun setup
//	go run . --session <id>               # resume a prior server-side session
//
// The TUI is for talking to a configured agent. It does not author
// skills, manage MCP servers, or edit agent personas — admin work
// happens in the `tavora` CLI or the /platform admin UI. The session
// is bound to the agent's active version, so persona and the version's
// skills_json filtering apply automatically.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	tavora "github.com/tavora-ai/tavora-sdk-go"
)

func main() {
	logPath, closeLog, err := SetupLogger()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: log setup failed: %v\n", err)
	} else {
		defer closeLog()
	}

	if err := run(logPath); err != nil {
		slog.Error("fatal", "err", err)
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		if logPath != "" {
			fmt.Fprintf(os.Stderr, "log:   %s\n", logPath)
		}
		os.Exit(1)
	}
}

func run(logPath string) error {
	var (
		resetConfig = flag.Bool("reset-config", false, "ignore stored config and rerun setup")
		sessionID   = flag.String("session", "", "resume an existing agent session by ID instead of creating a new one")
		agentFlag   = flag.String("agent", "", "agent ID or name to bind to (auto-picks if exactly one agent in workspace; interactive picker otherwise)")
	)
	flag.Parse()

	cfg, ws, err := acquireConfig(*resetConfig)
	if err != nil {
		return err
	}

	client := tavora.NewClient(cfg.URL, cfg.APIKey)

	var resumeSession *tavora.AgentSession
	var agent *tavora.AgentConfig
	if id := strings.TrimSpace(*sessionID); id != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		detail, err := client.GetAgentSession(ctx, id)
		cancel()
		if err != nil {
			return fmt.Errorf("resuming session %s: %w", id, err)
		}
		s := detail.Session
		resumeSession = &s
		slog.Info("resuming session", "session", id)
		// Resumed sessions already carry their own agent_version_id on
		// the server, so no agent picker is needed in this branch.
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		agent, err = resolveAgent(ctx, client, strings.TrimSpace(*agentFlag))
		cancel()
		if err != nil {
			return err
		}
		slog.Info("bound to agent", "agent", agent.Name, "agent_id", agent.ID, "active_version", *agent.ActiveVersionID)
	}

	prog := tea.NewProgram(newMainModel(client, ws, logPath, resumeSession, agent), tea.WithAltScreen())
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

// printResumeHint dumps the active session ID + the exact flag the user
// can pass next time, so they can pick up the conversation. Goes to
// stderr after Bubble Tea has restored the terminal, so the message
// remains visible after exit.
func printResumeHint(session *tavora.AgentSession) {
	if session == nil {
		return
	}
	fmt.Fprintf(os.Stderr, "session: %s\n", session.ID)
	fmt.Fprintf(os.Stderr, "resume:  agent-tui --session %s\n", session.ID)
}

// acquireConfig resolves credentials in this order:
//   - --reset-config flag → always run setup
//   - TAVORA_URL + TAVORA_API_KEY env vars → validate, never persist
//   - stored config file → validate, fall through to setup on failure
//   - setup TUI → captures URL + key, validates, persists
func acquireConfig(forceSetup bool) (*Config, *tavora.Workspace, error) {
	if !forceSetup {
		if env := EnvConfig(); env != nil {
			if ws, err := validate(env); err == nil {
				slog.Info("connected via env credentials", "workspace", ws.Name)
				return env, ws, nil
			} else {
				slog.Warn("env credentials rejected; falling back to setup", "err", err)
			}
		}
		if cfg, err := LoadConfig(); err == nil {
			if ws, err := validate(cfg); err == nil {
				slog.Info("connected via stored credentials", "workspace", ws.Name)
				return cfg, ws, nil
			} else {
				slog.Warn("stored credentials rejected; rerunning setup", "err", err)
			}
		}
	}

	seed, _ := LoadConfig()
	prog := tea.NewProgram(newSetupModel(seed), tea.WithAltScreen())
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

func validate(cfg *Config) (*tavora.Workspace, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return tavora.NewClient(cfg.URL, cfg.APIKey).GetWorkspace(ctx)
}

