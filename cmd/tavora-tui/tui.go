package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/reflow/wordwrap"
	tavora "github.com/tavora-ai/tavora-sdk-go"
)

// mainModel is the chat-style TUI: scrolling output viewport on top,
// prompt input on the bottom, status bar on top, hint line below input.
//
// The session is bound to an agent's active version (or, on resume, to
// whatever version the prior session carried). Persona, model, and the
// version's skills_json filtering are all driven server-side from the
// version — the TUI does not pass inline persona / tools.
//
// SSE events from the SDK arrive on a channel populated by runAgent;
// the model pulls from that channel via a recursive tea.Cmd so the UI
// stays responsive while the agent is reasoning.
type mainModel struct {
	client  *tavora.Client
	ws      *tavora.App
	agent   *tavora.AgentConfig // nil when resuming a session by ID
	session *tavora.AgentSession
	logPath string

	viewport viewport.Model
	input    textinput.Model
	spin     spinner.Model

	output  string
	width   int
	height  int
	ready   bool
	running bool

	stream <-chan streamEvent
	cancel context.CancelFunc

	// Lazy cache of the agent active version's stores_json so /upload
	// without an explicit store doesn't re-fetch the version each time.
	agentIndexIDsCache []string
	agentStoresCached  bool

	// Shell-style prompt history. history holds previously-sent prompts
	// (oldest first). historyIdx is -1 when the user is editing a fresh
	// prompt; values 0..len-1 mean the input is showing history[idx].
	// draft preserves whatever the user had typed when they first
	// pressed up, so down past the newest entry restores it.
	history    []string
	historyIdx int
	draft      string

	err error
}

type streamMsg streamEvent
type sessionStartedMsg struct {
	session *tavora.AgentSession
	err     error
}

func newMainModel(client *tavora.Client, ws *tavora.App, logPath string, resume *tavora.AgentSession, agent *tavora.AgentConfig) mainModel {
	in := textinput.New()
	in.Placeholder = "Ask anything. /help for commands."
	in.Prompt = "› "
	in.CharLimit = 4096
	in.Focus()

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("212"))

	return mainModel{
		client:     client,
		ws:         ws,
		agent:      agent,
		logPath:    logPath,
		session:    resume,
		input:      in,
		spin:       sp,
		historyIdx: -1,
	}
}

func (m mainModel) Init() tea.Cmd {
	if m.session != nil {
		// Resuming a server-side session — skip CreateAgentSession and
		// drive the same sessionStartedMsg path so the rest of the
		// model lights up identically.
		s := m.session
		return tea.Batch(textinput.Blink, func() tea.Msg {
			return sessionStartedMsg{session: s, err: nil}
		})
	}
	return tea.Batch(textinput.Blink, createSessionCmd(m.client, m.agent))
}

// createSessionCmd creates a session pinned to the agent's active
// version. The server fills persona + model from the version, and the
// runtime applies the version's skills_json filtering to all skill
// types. No inline persona / tools are passed.
func createSessionCmd(client *tavora.Client, agent *tavora.AgentConfig) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		input := tavora.CreateAgentSessionInput{
			Title: "agent-tui · " + agent.Name,
		}
		if agent.ActiveVersionID != nil {
			input.AgentVersionID = *agent.ActiveVersionID
		}
		s, err := client.CreateAgentSession(ctx, input)
		return sessionStartedMsg{session: s, err: err}
	}
}

// waitForStream returns a Cmd that blocks on one event from the stream
// channel, wraps it as streamMsg, and returns. The Update handler
// re-issues this Cmd until it sees a Result event, at which point it
// stops re-issuing and clears the stream pointer.
func waitForStream(ch <-chan streamEvent) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return streamMsg{Result: &runResult{}}
		}
		return streamMsg(ev)
	}
}

func (m mainModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m.handleResize(msg), nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			if m.cancel != nil {
				m.cancel()
			}
			return m, tea.Quit
		case "enter":
			if m.running || !m.ready || strings.TrimSpace(m.input.Value()) == "" {
				return m, nil
			}
			return m.submit()
		case "pgup":
			m.viewport.HalfViewUp()
			return m, nil
		case "pgdown":
			m.viewport.HalfViewDown()
			return m, nil
		case "up":
			if m.ready && !m.running {
				m.historyPrev()
				return m, nil
			}
		case "down":
			if m.ready && !m.running {
				m.historyNext()
				return m, nil
			}
		}

	case sessionStartedMsg:
		if msg.err != nil {
			slog.Error("creating agent session", "err", msg.err)
			m.err = fmt.Errorf("creating agent session: %w", msg.err)
			return m, tea.Quit
		}
		slog.Info("agent session ready", "session", msg.session.ID)
		m.session = msg.session
		m.ready = true
		if m.agent != nil {
			m.appendSystem(fmt.Sprintf("Bound to agent %q · session %s", m.agent.Name, msg.session.ID))
		} else {
			m.appendSystem(fmt.Sprintf("Resumed session %s", msg.session.ID))
		}
		if m.logPath != "" {
			m.appendSystem("log: " + m.logPath)
		}
		return m, nil

	case streamMsg:
		return m.handleStream(msg)

	case uploadResultMsg:
		if msg.err != nil {
			slog.Error("document upload failed", "err", msg.err)
			m.appendError(msg.err.Error())
		} else {
			slog.Info("document uploaded", "doc_id", msg.doc.ID, "filename", msg.doc.Filename, "status", msg.doc.Status)
			m.appendSystem(fmt.Sprintf("Uploaded %s (id %s, status %s) — agent's search() will see it once processing completes",
				msg.doc.Filename, msg.doc.ID, msg.doc.Status))
		}
		return m, nil

	case spinner.TickMsg:
		if m.running {
			var cmd tea.Cmd
			m.spin, cmd = m.spin.Update(msg)
			return m, cmd
		}
	}

	var cmds []tea.Cmd
	var c tea.Cmd
	m.input, c = m.input.Update(msg)
	cmds = append(cmds, c)
	m.viewport, c = m.viewport.Update(msg)
	cmds = append(cmds, c)
	return m, tea.Batch(cmds...)
}

func (m mainModel) handleResize(msg tea.WindowSizeMsg) mainModel {
	m.width, m.height = msg.Width, msg.Height
	headerH := lipgloss.Height(m.headerView())
	footerH := lipgloss.Height(m.footerView())
	vpH := msg.Height - headerH - footerH
	if vpH < 3 {
		vpH = 3
	}
	if !m.ready || m.viewport.Width == 0 {
		m.viewport = viewport.New(msg.Width, vpH)
		m.viewport.SetContent(m.output)
	} else {
		m.viewport.Width = msg.Width
		m.viewport.Height = vpH
	}
	m.input.Width = msg.Width - lipgloss.Width(m.input.Prompt) - 2
	return m
}

func (m mainModel) submit() (tea.Model, tea.Cmd) {
	prompt := strings.TrimSpace(m.input.Value())
	m.input.SetValue("")

	if cmd, handled := m.handleSlash(prompt); handled {
		return m, cmd
	}

	slog.Info("user prompt", "session", m.session.ID, "len", len(prompt))
	m.pushHistory(prompt)
	m.appendUser(prompt)
	m.running = true

	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.stream = runAgent(ctx, m.client, m.session.ID, prompt)

	return m, tea.Batch(m.spin.Tick, waitForStream(m.stream))
}

// handleSlash uses a pointer receiver because slash commands mutate the
// model (clearing output, appending system messages, dropping ready
// while a fresh session is created). With a value receiver these
// mutations were silently discarded — that's why /help previously
// looked unimplemented.
func (m *mainModel) handleSlash(prompt string) (tea.Cmd, bool) {
	// Slash commands with arguments — checked before the simple-switch.
	if strings.HasPrefix(prompt, "/upload") {
		return m.handleUpload(strings.TrimPrefix(prompt, "/upload")), true
	}

	switch prompt {
	case "/exit", "/quit":
		return tea.Quit, true
	case "/clear":
		m.output = ""
		m.viewport.SetContent("")
		return nil, true
	case "/help":
		m.appendSystem("Commands: /reset (new session) · /clear (clear screen) · /upload <path> [store] · /log (show log path) · /quit · pgup/pgdown to scroll")
		return nil, true
	case "/log":
		if m.logPath == "" {
			m.appendSystem("logging is not active for this session")
		} else {
			m.appendSystem("log: " + m.logPath)
		}
		return nil, true
	case "/reset":
		if m.agent == nil {
			m.appendSystem("/reset is unavailable on resumed sessions — exit and relaunch with --agent")
			return nil, true
		}
		m.appendSystem("Creating fresh session...")
		m.ready = false
		return createSessionCmd(m.client, m.agent), true
	}
	return nil, false
}

func (m mainModel) handleStream(msg streamMsg) (tea.Model, tea.Cmd) {
	if msg.Result != nil {
		if msg.Result.Err != nil {
			slog.Error("agent run failed", "session", m.session.ID, "err", msg.Result.Err)
			m.appendError(msg.Result.Err.Error())
		} else {
			slog.Info("agent run finished", "session", m.session.ID)
		}
		m.running = false
		m.stream = nil
		if m.cancel != nil {
			m.cancel()
			m.cancel = nil
		}
		return m, nil
	}

	if msg.Event != nil {
		slog.Debug("agent event", "type", msg.Event.Type, "tool", msg.Event.Tool)
		m.renderEvent(*msg.Event)
	}
	return m, waitForStream(m.stream)
}

func (m *mainModel) renderEvent(e tavora.AgentEvent) {
	switch e.Type {
	case "tool_call":
		args, _ := json.Marshal(e.Args)
		m.appendDim(fmt.Sprintf("  ⏵ %s %s", e.Tool, truncate(string(args), 100)))
	case "tool_result":
		m.appendDim(fmt.Sprintf("  ✓ %s", e.Tool))
	case "execute_js":
		m.appendDim("  ⏵ execute_js")
		m.appendDim("    " + truncate(strings.ReplaceAll(e.Content, "\n", " ⏎ "), 120))
	case "execute_js_result":
		m.appendDim("  ✓ execute_js → " + truncate(strings.ReplaceAll(e.Content, "\n", " ⏎ "), 120))
	case "sandbox_event":
		kind, _ := e.Args["kind"].(string)
		summary, _ := e.Args["summary"].(string)
		if kind == "" {
			kind = "sandbox"
		}
		m.appendDim(fmt.Sprintf("  • %s: %s", kind, truncate(summary, 100)))
	case "response":
		m.appendAgent(e.Content)
	case "error":
		m.appendError(e.Content)
	case "done":
		if e.Summary != nil {
			m.appendDim(fmt.Sprintf("  [%d steps · %d prompt + %d completion tokens]",
				e.Summary.Steps, e.Summary.Tokens.Prompt, e.Summary.Tokens.Completion))
		}
	}
}

func (m *mainModel) appendUser(text string) {
	m.output += m.wrap(userStyle.Render("You")+"  "+text) + "\n\n"
	m.scrollToBottom()
}

func (m *mainModel) appendAgent(text string) {
	m.output += m.wrap(agentStyle.Render("Agent")+"  "+text) + "\n\n"
	m.scrollToBottom()
}

func (m *mainModel) appendSystem(text string) {
	m.output += m.wrap(systemStyle.Render(text)) + "\n"
	m.scrollToBottom()
}

func (m *mainModel) appendDim(text string) {
	m.output += m.wrap(subtleStyle.Render(text)) + "\n"
	m.scrollToBottom()
}

func (m *mainModel) appendError(text string) {
	m.output += m.wrap(errStyle.Render("error: "+text)) + "\n"
	m.scrollToBottom()
}

// handleUpload parses `/upload <path> [store]` and kicks off an
// asynchronous UploadDocument call. Errors during arg parsing or store
// resolution surface synchronously as system messages; the upload
// itself returns via uploadResultMsg.
func (m *mainModel) handleUpload(rest string) tea.Cmd {
	path, storeArg, err := parseUploadArgs(rest)
	if err != nil {
		m.appendError(err.Error())
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	indexID, err := m.resolveStoreForUpload(ctx, storeArg)
	if err != nil {
		m.appendError(err.Error())
		return nil
	}

	m.appendSystem(fmt.Sprintf("Uploading %s → store %s ...", path, indexID))
	return uploadCmd(m.client, indexID, path)
}

// pushHistory records a sent prompt and resets the browse cursor so
// the next up-arrow lands on this entry. Suppresses adjacent duplicates
// to keep the history list tidy.
func (m *mainModel) pushHistory(prompt string) {
	if n := len(m.history); n == 0 || m.history[n-1] != prompt {
		m.history = append(m.history, prompt)
	}
	m.historyIdx = -1
	m.draft = ""
}

func (m *mainModel) historyPrev() {
	if len(m.history) == 0 {
		return
	}
	if m.historyIdx == -1 {
		m.draft = m.input.Value()
		m.historyIdx = len(m.history) - 1
	} else if m.historyIdx > 0 {
		m.historyIdx--
	} else {
		return
	}
	m.input.SetValue(m.history[m.historyIdx])
	m.input.CursorEnd()
}

func (m *mainModel) historyNext() {
	if m.historyIdx == -1 {
		return
	}
	if m.historyIdx >= len(m.history)-1 {
		m.historyIdx = -1
		m.input.SetValue(m.draft)
		m.draft = ""
	} else {
		m.historyIdx++
		m.input.SetValue(m.history[m.historyIdx])
	}
	m.input.CursorEnd()
}

// wrap word-wraps to the current terminal width using the ANSI-aware
// reflow helper, so styled prefixes and embedded color codes survive
// intact. Falls back to no wrapping if width hasn't been set yet (the
// first WindowSizeMsg arrives before any append call in practice).
func (m *mainModel) wrap(s string) string {
	if m.width <= 0 {
		return s
	}
	return wordwrap.String(s, m.width)
}

func (m *mainModel) scrollToBottom() {
	if m.viewport.Height == 0 {
		return
	}
	m.viewport.SetContent(m.output)
	m.viewport.GotoBottom()
}

func (m mainModel) View() string {
	if m.width == 0 {
		return "loading..."
	}
	return strings.Join([]string{
		m.headerView(),
		m.viewport.View(),
		m.footerView(),
	}, "\n")
}

func (m mainModel) headerView() string {
	left := fmt.Sprintf("app: %s", m.ws.Name)
	right := "agent-tui"
	if m.session != nil {
		right = "session: " + m.session.ID[:min(8, len(m.session.ID))]
	}
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return headerStyle.Width(m.width).Render(left + strings.Repeat(" ", gap) + right)
}

func (m mainModel) footerView() string {
	var status string
	switch {
	case !m.ready:
		status = m.spin.View() + " preparing session..."
	case m.running:
		status = m.spin.View() + " thinking..."
	default:
		status = m.input.View()
	}
	hint := subtleStyle.Render("enter to send · /help · ctrl+c to quit")
	return strings.Join([]string{
		inputBoxStyle.Width(m.width).Render(status),
		hint,
	}, "\n")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

var (
	headerStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("63")).
			Foreground(lipgloss.Color("230")).
			Padding(0, 1).
			Bold(true)
	inputBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("63")).
			Padding(0, 1)
	userStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("212")).
			Bold(true)
	agentStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("46")).
			Bold(true)
	systemStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			Italic(true)
)
