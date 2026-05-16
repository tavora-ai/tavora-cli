// Package runs serializes a streamed agent run into a self-contained
// markdown file under `tavora/.runs/`.
//
// The shape matches docs/code-first-agents-concept.md §"Session logs
// to disk" — one file per invocation, ISO-timestamp-prefixed name,
// frontmatter + trace + errors. AI coding tools read these with
// their native file tools to verify the last edit's behavior; the
// runtime keeps the authoritative copy in Studio.
package runs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tavora "github.com/tavora-ai/tavora-sdk-go"
)

// Recorder captures a streamed AgentEvent sequence into a buffer
// that Write later serializes. A Recorder is single-use: one Recorder
// per RunAgent call.
type Recorder struct {
	StartedAt time.Time
	Events    []tavora.AgentEvent
	// FinalText carries the final response text (one of the
	// EventTypeResponse events). The last one wins so multi-turn
	// runs keep the most recent reply.
	FinalText string
	// ErrorText is the most recent error event content. Empty on
	// success.
	ErrorText string
}

// New returns a Recorder with StartedAt set to now.
func New() *Recorder {
	return &Recorder{StartedAt: time.Now().UTC()}
}

// Handle is the callback to pass into client.RunAgent. It records
// the event verbatim and pulls out the final response text + last
// error text for the markdown header.
func (r *Recorder) Handle(evt tavora.AgentEvent) {
	r.Events = append(r.Events, evt)
	switch evt.Type {
	case tavora.EventTypeResponse:
		if evt.Content != "" {
			r.FinalText = evt.Content
		}
	case tavora.EventTypeError:
		r.ErrorText = evt.Content
	}
}

// Meta is everything the markdown header needs that the event stream
// doesn't carry. The caller fills it in once.
type Meta struct {
	AgentLocalID string
	AgentID      string
	SessionID    string
	DraftHash    string // optional — empty for live runs
	Target       string // "draft" or "live"
	Input        string
}

// Write serializes the recorded run as markdown and writes it under
// dir (typically `<root>/.runs`). Returns the absolute file path.
//
// File name is `<ISO-timestamp>-<agent>-<short-sid>.md`. Dashes in
// the timestamp swap colons for filesystem-portability.
func (r *Recorder) Write(dir string, meta Meta) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	body := r.Render(meta)
	name := fileName(r.StartedAt, meta.AgentLocalID, meta.SessionID)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// Render produces the markdown body without writing.
func (r *Recorder) Render(meta Meta) string {
	var b strings.Builder
	duration := time.Since(r.StartedAt)

	fmt.Fprintf(&b, "# Session %s\n\n", r.StartedAt.Format(time.RFC3339))

	target := meta.Target
	if target == "" {
		target = "draft"
	}
	versionTag := target
	if meta.DraftHash != "" {
		versionTag = fmt.Sprintf("%s@%s", target, shortHash(meta.DraftHash))
	}
	fmt.Fprintf(&b, "**Agent:** %s (%s)\n", agentLabel(meta), versionTag)
	if meta.SessionID != "" {
		fmt.Fprintf(&b, "**Session:** %s\n", meta.SessionID)
	}
	fmt.Fprintf(&b, "**Input:** %s\n", oneLine(meta.Input))
	if r.FinalText != "" {
		fmt.Fprintf(&b, "**Output:** %s\n", oneLine(r.FinalText))
	}

	pTok, cTok, steps := aggregates(r.Events)
	fmt.Fprintf(&b, "**Duration:** %s | **Tokens:** prompt=%d completion=%d | **Steps:** %d\n",
		formatDuration(duration), pTok, cTok, steps)
	b.WriteString("\n## Trace\n\n")

	step := 0
	for _, evt := range r.Events {
		switch evt.Type {
		case tavora.EventTypeExecuteJS:
			step++
			fmt.Fprintf(&b, "### Step %d — execute_js\n\n```js\n%s\n```\n\n", step, strings.TrimSpace(evt.Content))
		case tavora.EventTypeExecuteJSResult:
			content := strings.TrimSpace(evt.Content)
			if content == "" {
				content = formatResult(evt.Result)
			}
			fmt.Fprintf(&b, "→ %s\n\n", oneLine(content))
		case tavora.EventTypeSandboxEvent:
			if strings.TrimSpace(evt.Content) == "" {
				continue
			}
			fmt.Fprintf(&b, "_sandbox:_ %s\n\n", oneLine(evt.Content))
		case tavora.EventTypeDataUpdate:
			args, _ := json.Marshal(evt.Args)
			fmt.Fprintf(&b, "_data_update:_ `%s`\n\n", string(args))
		case tavora.EventTypeInputRequest:
			fmt.Fprintf(&b, "_input_request:_ %s\n\n", oneLine(evt.Content))
		case tavora.EventTypeResponse:
			step++
			fmt.Fprintf(&b, "### Step %d — response\n\n%s\n\n", step, strings.TrimSpace(evt.Content))
		case tavora.EventTypeError, tavora.EventTypeDone:
			// Handled in their own sections.
		}
	}

	b.WriteString("## Errors\n\n")
	if r.ErrorText != "" {
		fmt.Fprintf(&b, "```\n%s\n```\n", r.ErrorText)
	} else {
		b.WriteString("(none)\n")
	}

	return b.String()
}

// PruneOld keeps at most `keep` files in dir, ordered by mtime
// (newest wins). Returns the list of paths removed.
func PruneOld(dir string, keep int) ([]string, error) {
	if keep <= 0 {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	type stamped struct {
		path string
		mod  time.Time
	}
	var files []stamped
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, stamped{
			path: filepath.Join(dir, e.Name()),
			mod:  info.ModTime(),
		})
	}
	if len(files) <= keep {
		return nil, nil
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	var removed []string
	for _, f := range files[keep:] {
		if err := os.Remove(f.path); err == nil {
			removed = append(removed, f.path)
		}
	}
	return removed, nil
}

// ResolveSession finds a session file under dir given a partial id
// or a filename. Returns the absolute path of the unique match.
// Errors when no match or multiple matches found.
func ResolveSession(dir, query string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var matches []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		if name == query || strings.Contains(name, query) {
			matches = append(matches, filepath.Join(dir, name))
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no session matches %q in %s", query, dir)
	case 1:
		return matches[0], nil
	default:
		sort.Strings(matches)
		return "", fmt.Errorf("ambiguous: %d files match %q in %s\n  %s",
			len(matches), query, dir, strings.Join(matches, "\n  "))
	}
}

// LatestSession returns the most recent .md file in dir, or an
// error if the dir is empty / missing.
func LatestSession(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	type stamped struct {
		path string
		mod  time.Time
	}
	var files []stamped
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, stamped{
			path: filepath.Join(dir, e.Name()),
			mod:  info.ModTime(),
		})
	}
	if len(files) == 0 {
		return "", fmt.Errorf("no sessions in %s", dir)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	return files[0].path, nil
}

// --- helpers ---

func fileName(t time.Time, agentID, sessionID string) string {
	// `2006-01-02T15-04-05Z` — RFC3339 with colons swapped for dashes
	// so the name is safe across all major filesystems.
	stamp := t.UTC().Format("2006-01-02T15-04-05Z")
	short := sessionID
	if len(short) > 6 {
		short = short[:6]
	}
	if short == "" {
		short = "nosid"
	}
	agentSlug := slug(agentID)
	if agentSlug == "" {
		agentSlug = "agent"
	}
	return fmt.Sprintf("%s-%s-%s.md", stamp, agentSlug, short)
}

func slug(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			out = append(out, c)
		case c >= 'A' && c <= 'Z':
			out = append(out, c+('a'-'A'))
		case c == '-' || c == '_':
			out = append(out, '-')
		case c == ' ':
			out = append(out, '-')
		}
	}
	return string(out)
}

func oneLine(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	const max = 240
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

func shortHash(h string) string {
	// Hash is "sha256:<hex>"; surface just the first 12 hex chars.
	if i := strings.Index(h, ":"); i >= 0 && len(h) > i+13 {
		return h[i+1 : i+13]
	}
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

func aggregates(events []tavora.AgentEvent) (prompt, completion int32, steps int) {
	for _, e := range events {
		if e.Tokens != nil {
			prompt += e.Tokens.Prompt
			completion += e.Tokens.Completion
		}
		switch e.Type {
		case tavora.EventTypeExecuteJS, tavora.EventTypeResponse:
			steps++
		}
		if e.Summary != nil {
			// The terminal `done` event carries authoritative totals.
			// Prefer them over the running counters above.
			if e.Summary.Tokens.Prompt > 0 || e.Summary.Tokens.Completion > 0 {
				prompt = e.Summary.Tokens.Prompt
				completion = e.Summary.Tokens.Completion
			}
			if e.Summary.Steps > 0 {
				steps = e.Summary.Steps
			}
		}
	}
	return
}

func formatResult(v any) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

func agentLabel(m Meta) string {
	if m.AgentLocalID == "" {
		return m.AgentID
	}
	if m.AgentID == "" {
		return m.AgentLocalID
	}
	return fmt.Sprintf("%s (%s)", m.AgentLocalID, m.AgentID)
}
