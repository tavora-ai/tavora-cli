package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	tavora "github.com/tavora-ai/tavora-sdk-go"
)

// resolveAgent picks the AgentConfig the TUI should bind to.
//
// Resolution order:
//   1. If flag matches an agent's ID or Name (case-sensitive on ID,
//      case-insensitive on Name), use that.
//   2. If flag is empty and there is exactly one agent, auto-select.
//   3. If flag is empty and there are 2+ agents, run the interactive
//      picker.
//   4. If there are zero agents, return a helpful error pointing the
//      user at the CLI / admin UI rather than offering inline create
//      (configured-agent-only is the TUI's contract).
//
// On success the returned config has its ActiveVersionID populated, or
// the function errors — chatting against an agent without a version
// leaves the runtime nothing to resolve persona / skills_json from.
func resolveAgent(ctx context.Context, client *tavora.Client, flag string) (*tavora.AgentConfig, error) {
	listCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	agents, err := client.ListAgentConfigs(listCtx)
	cancel()
	if err != nil {
		return nil, fmt.Errorf("listing agents: %w", err)
	}
	if len(agents) == 0 {
		// "Workspace always has a default agent" is a platform invariant
		// the backend owns (auto-provisioning on workspace create). If
		// you hit this error, the workspace was created via a path that
		// bypasses signup's SeedStarter — fix in CLI:
		//   tavora workspace seed
		// rather than bootstrapping inline from the TUI.
		return nil, fmt.Errorf("no agents in this workspace — run `tavora workspace seed` to provision the default agent, or use the admin UI")
	}

	if flag != "" {
		match := findAgent(agents, flag)
		if match == nil {
			return nil, fmt.Errorf("no agent matched %q in this workspace (try `tavora agents list`)", flag)
		}
		return ensureActiveVersion(match)
	}

	if len(agents) == 1 {
		return ensureActiveVersion(&agents[0])
	}

	picked, err := runAgentPicker(agents)
	if err != nil {
		return nil, err
	}
	return ensureActiveVersion(picked)
}

func ensureActiveVersion(a *tavora.AgentConfig) (*tavora.AgentConfig, error) {
	if a.ActiveVersionID == nil || *a.ActiveVersionID == "" {
		return nil, fmt.Errorf("agent %q (%s) has no active version — create one with `tavora agent-versions create %s` or via the admin UI", a.Name, a.ID, a.ID)
	}
	return a, nil
}

func findAgent(agents []tavora.AgentConfig, flag string) *tavora.AgentConfig {
	flagLower := strings.ToLower(flag)
	for i := range agents {
		if agents[i].ID == flag {
			return &agents[i]
		}
	}
	for i := range agents {
		if strings.EqualFold(agents[i].Name, flag) {
			return &agents[i]
		}
	}
	// Looser fallback: prefix on lowercased name. Lets `--agent rev`
	// match "Revenue Bot" when the user can't be bothered to spell it.
	for i := range agents {
		if strings.HasPrefix(strings.ToLower(agents[i].Name), flagLower) {
			return &agents[i]
		}
	}
	return nil
}

// --- interactive picker ---

type agentItem struct{ a tavora.AgentConfig }

func (i agentItem) Title() string { return i.a.Name }
func (i agentItem) Description() string {
	if i.a.Description != "" {
		return i.a.Description
	}
	return i.a.ID
}
func (i agentItem) FilterValue() string { return i.a.Name }

type pickerModel struct {
	list     list.Model
	chosen   *tavora.AgentConfig
	canceled bool
}

func newPickerModel(agents []tavora.AgentConfig) pickerModel {
	items := make([]list.Item, 0, len(agents))
	for _, a := range agents {
		items = append(items, agentItem{a: a})
	}
	d := list.NewDefaultDelegate()
	l := list.New(items, d, 0, 0)
	l.Title = "Pick an agent"
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(true)
	l.Styles.Title = lipgloss.NewStyle().
		Foreground(lipgloss.Color("230")).
		Background(lipgloss.Color("63")).
		Padding(0, 1).
		Bold(true)
	return pickerModel{list: l}
}

func (m pickerModel) Init() tea.Cmd { return nil }

func (m pickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.list.SetSize(msg.Width, msg.Height)
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.canceled = true
			return m, tea.Quit
		case "enter":
			if it, ok := m.list.SelectedItem().(agentItem); ok {
				a := it.a
				m.chosen = &a
				return m, tea.Quit
			}
		}
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m pickerModel) View() string { return m.list.View() }

func runAgentPicker(agents []tavora.AgentConfig) (*tavora.AgentConfig, error) {
	prog := tea.NewProgram(newPickerModel(agents), tea.WithAltScreen())
	final, err := prog.Run()
	if err != nil {
		return nil, fmt.Errorf("running agent picker: %w", err)
	}
	pm, ok := final.(pickerModel)
	if !ok || pm.canceled || pm.chosen == nil {
		return nil, fmt.Errorf("agent selection canceled")
	}
	return pm.chosen, nil
}
