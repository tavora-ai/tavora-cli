package tui

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	tavora "github.com/tavora-ai/tavora-sdk-go"
)

// setupModel runs a small two-field form (URL, API key) and validates
// the credentials by calling GetProject before persisting them. It
// reports the resulting Config + Project back to the caller via
// program-level messages so main.go can hand off into mainModel.

type setupStep int

const (
	stepURL setupStep = iota
	stepKey
	stepValidating
	stepDone
)

type setupModel struct {
	step    setupStep
	url     textinput.Model
	apiKey  textinput.Model
	spin    spinner.Model
	err     error
	cfg     *Config
	ws      *tavora.Project
	width   int
	height  int
	exiting bool
}

type setupValidatedMsg struct {
	cfg *Config
	ws  *tavora.Project
}

type setupErrorMsg struct{ err error }

// SetupResult is what main.go receives once the setup TUI quits.
type SetupResult struct {
	Cfg *Config
	WS  *tavora.Project
}

func newSetupModel(seed *Config) setupModel {
	url := textinput.New()
	url.Placeholder = "https://api.tavora.ai"
	url.Prompt = "  URL  › "
	url.CharLimit = 256
	url.SetWidth(60)
	url.Focus()
	if seed != nil && seed.URL != "" {
		url.SetValue(seed.URL)
	}

	key := textinput.New()
	key.Placeholder = "tvr_..."
	key.Prompt = "  Key  › "
	key.CharLimit = 256
	key.SetWidth(60)
	key.EchoMode = textinput.EchoPassword
	key.EchoCharacter = '•'
	if seed != nil && seed.APIKey != "" {
		key.SetValue(seed.APIKey)
	}

	sp := spinner.New()
	sp.Spinner = spinner.Dot

	return setupModel{
		step:   stepURL,
		url:    url,
		apiKey: key,
		spin:   sp,
	}
}

func (m setupModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m setupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.exiting = true
			return m, tea.Quit
		case "enter":
			return m.advance()
		case "tab", "shift+tab":
			return m.toggleFocus(), nil
		}

	case setupValidatedMsg:
		m.cfg, m.ws = msg.cfg, msg.ws
		m.step = stepDone
		_ = SaveConfig(msg.cfg)
		return m, tea.Quit

	case setupErrorMsg:
		m.err = msg.err
		m.step = stepKey
		m.apiKey.Focus()
		return m, nil

	case spinner.TickMsg:
		if m.step == stepValidating {
			var cmd tea.Cmd
			m.spin, cmd = m.spin.Update(msg)
			return m, cmd
		}
	}

	var cmd tea.Cmd
	switch m.step {
	case stepURL:
		m.url, cmd = m.url.Update(msg)
	case stepKey:
		m.apiKey, cmd = m.apiKey.Update(msg)
	}
	return m, cmd
}

func (m setupModel) toggleFocus() setupModel {
	if m.step == stepURL {
		m.step = stepKey
		m.url.Blur()
		m.apiKey.Focus()
	} else if m.step == stepKey {
		m.step = stepURL
		m.apiKey.Blur()
		m.url.Focus()
	}
	return m
}

func (m setupModel) advance() (tea.Model, tea.Cmd) {
	switch m.step {
	case stepURL:
		if strings.TrimSpace(m.url.Value()) == "" {
			m.err = fmt.Errorf("URL is required")
			return m, nil
		}
		m.err = nil
		m.step = stepKey
		m.url.Blur()
		m.apiKey.Focus()
		return m, nil

	case stepKey:
		if strings.TrimSpace(m.apiKey.Value()) == "" {
			m.err = fmt.Errorf("API key is required")
			return m, nil
		}
		m.err = nil
		m.step = stepValidating
		cfg := &Config{
			URL:    strings.TrimRight(strings.TrimSpace(m.url.Value()), "/"),
			APIKey: strings.TrimSpace(m.apiKey.Value()),
		}
		return m, tea.Batch(m.spin.Tick, validateCmd(cfg))
	}
	return m, nil
}

func validateCmd(cfg *Config) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		client := tavora.NewClient(cfg.URL, cfg.APIKey)
		ws, err := client.GetProject(ctx)
		if err != nil {
			slog.Warn("setup validation failed", "url", cfg.URL, "err", err)
			return setupErrorMsg{err: err}
		}
		slog.Info("setup validated", "url", cfg.URL, "project", ws.Name)
		return setupValidatedMsg{cfg: cfg, ws: ws}
	}
}

var (
	titleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("212")).
			Bold(true)
	subtleStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	boxStyle    = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("63")).
			Padding(1, 2)
)

func (m setupModel) View() tea.View {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Tavora — Agent TUI"))
	b.WriteString("\n")
	b.WriteString(subtleStyle.Render("Connect to a project. The API key scopes everything you do."))
	b.WriteString("\n\n")

	switch m.step {
	case stepURL, stepKey:
		b.WriteString(m.url.View())
		b.WriteString("\n")
		b.WriteString(m.apiKey.View())
	case stepValidating:
		b.WriteString(m.url.View())
		b.WriteString("\n")
		b.WriteString(m.apiKey.View())
		b.WriteString("\n\n")
		b.WriteString(m.spin.View())
		b.WriteString(" validating credentials...")
	case stepDone:
		b.WriteString(fmt.Sprintf("Connected to project: %s", m.ws.Name))
	}

	if m.err != nil {
		b.WriteString("\n\n")
		b.WriteString(errStyle.Render("error: " + m.err.Error()))
	}

	b.WriteString("\n\n")
	b.WriteString(subtleStyle.Render("enter to continue · tab to switch field · esc/ctrl+c to quit"))
	v := tea.NewView(boxStyle.Render(b.String()))
	v.AltScreen = true
	return v
}
