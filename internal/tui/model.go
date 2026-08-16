package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/globulario/sensei-code/internal/authority"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/mcpconfig"
	"github.com/globulario/sensei-code/internal/provider"
	"github.com/globulario/sensei-code/internal/workflow"
)

type eventMsg event.Event
type closedMsg struct{}
type tickMsg time.Time
type providerLoginFinishedMsg struct {
	id  provider.ID
	err error
}

// mode is the interaction posture shown in the bar under the composer. Both
// modes are display/verbosity postures owned by the TUI. Neither can widen what
// the workflow is allowed to do, and neither can skip a human-authority
// rendezvous: those remain governed by the workflow and Sensei.
type mode int

const (
	modeAuto mode = iota
	modeAutoStreaming
)

func (m mode) label() string {
	switch m {
	case modeAutoStreaming:
		return "auto mode on · streaming agent activity"
	default:
		return "auto mode on"
	}
}

// tickInterval drives the working indicator's animation.
const tickInterval = 200 * time.Millisecond

// workingWords are the whimsical present participles shown while the architect
// and its workers run, so a long autonomous stretch still looks alive.
var workingWords = []string{
	"Newspapering", "Percolating", "Pondering", "Marinating", "Deliberating",
	"Whittling", "Convening", "Sharpening", "Ruminating", "Untangling",
	"Noodling", "Cogitating", "Distilling", "Harmonizing", "Puzzling",
	"Consulting the graph", "Weighing invariants", "Sensei-ing",
}

var spinnerFrames = []string{"✻", "✼", "✽", "✼"}

type Model struct {
	ctx           context.Context
	engine        *workflow.Engine
	events        <-chan event.Event
	input         textarea.Model
	initCmd       tea.Cmd
	lines         []string
	width, height int
	busy          bool
	verbose       bool
	loginMenu     bool
	mcpMenu       bool
	pendingTask   string
	pending       *authority.Decision
	mode          mode
	frame         int
	startedAt     time.Time
	wordIdx       int
	activity      string
}

func New(ctx context.Context, engine *workflow.Engine, events <-chan event.Event, history []event.Event) Model {
	ta := textarea.New()
	ta.Placeholder = "Describe a task for Sensei Code..."
	ta.Prompt = "› "
	ta.SetHeight(1)
	ta.SetWidth(80)
	ta.ShowLineNumbers = false
	focusCmd := ta.Focus()
	return Model{
		ctx:     ctx,
		engine:  engine,
		events:  events,
		input:   ta,
		initCmd: focusCmd,
		lines:   append(banner(len(history) > 0), replayConversation(history)...),
	}
}

func banner(resumed bool) []string {
	lines := []string{senseiStyle.Render("◆ SENSEI CODE"), dimStyle.Render("  autonomous, governed development"), ""}
	if resumed {
		lines = append(lines, dimStyle.Render("  resumed earlier conversation · /clear to start fresh"), "")
	}
	return lines
}

// replayConversation rebuilds the dialogue from a recorded session so a
// relaunch continues where the last one stopped. Only conversation is replayed;
// recorded activity described work that is already finished.
func replayConversation(history []event.Event) []string {
	var lines []string
	for _, e := range history {
		if e.Source == event.SourceSystem && e.Kind == event.TaskCreated {
			lines = append(lines, userStyle.Render("You"), promptGlyphStyle.Render("› ")+strings.TrimSpace(e.Summary), "")
			continue
		}
		if !isConversation(e) {
			continue
		}
		if line := renderEvent(e); line != "" {
			lines = append(lines, line, "")
		}
	}
	return lines
}

func (m Model) Init() tea.Cmd { return tea.Batch(m.initCmd, waitEvent(m.events), tick()) }

func tick() tea.Cmd {
	return tea.Tick(tickInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func waitEvent(ch <-chan event.Event) tea.Cmd {
	return func() tea.Msg {
		e, ok := <-ch
		if !ok {
			return closedMsg{}
		}
		return eventMsg(e)
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.input.SetWidth(max(20, msg.Width-6))
	case tickMsg:
		if m.busy {
			m.frame++
			// Change the word every ~5s so it reads as progress, not noise.
			if m.frame%(int(5*time.Second/tickInterval)) == 0 {
				m.wordIdx++
			}
		}
		return m, tick()
	case providerLoginFinishedMsg:
		if msg.err != nil {
			m.lines = append(m.lines, errorStyle.Render("✗ PROVIDER"), "  "+provider.Label(msg.id)+": "+msg.err.Error())
		} else {
			m.lines = append(m.lines, workerStyle.Render("✓ PROVIDER"), "  "+provider.Label(msg.id)+" login flow completed")
		}
		m.loginMenu = false
	case eventMsg:
		e := event.Event(msg)
		if e.Kind == event.AuthorityRequired {
			var d authority.Decision
			if json.Unmarshal(e.Payload, &d) == nil {
				m.pending = &d
				m.pendingTask = e.TaskID
				m.busy = false
			}
		}
		if e.Kind == event.AuthorityResolved {
			m.pending = nil
			m.pendingTask = ""
			m.busy = true
		}
		// The transcript is the conversation with the architect. Sensei
		// receipts, worker output, git and retries are activity: they feed the
		// status bar, and reach the transcript only in streaming mode.
		if isConversation(e) {
			if line := renderEvent(e); line != "" {
				m.lines = append(m.lines, line, "")
				// Scrolling rewrites every row with different text. The diff
				// renderer does not clear to end of line when a row shrinks, so
				// a shorter row keeps the tail of the row it replaced.
				cmds = append(cmds, tea.ClearScreen)
			}
		} else {
			if phase := currentPhase(e); phase != "" {
				m.activity = phase
			}
			if m.verbose && e.Kind != event.TaskCreated {
				if line := renderEvent(e); line != "" {
					m.lines = append(m.lines, line)
					cmds = append(cmds, tea.ClearScreen)
				}
			}
		}
		if e.Kind == event.WorkflowCompleted || e.Kind == event.WorkflowFailed {
			m.busy = false
			m.pending = nil
			m.pendingTask = ""
		}
		cmds = append(cmds, waitEvent(m.events))
	case closedMsg:
		return m, tea.Quit
	case tea.KeyPressMsg:
		if m.mcpMenu {
			key := msg.String()
			if key == "ctrl+c" {
				return m, tea.Quit
			}
			if key == "esc" {
				m.mcpMenu = false
				return m, tea.Batch(cmds...)
			}
			agent, ok := mcpAgentFor(key)
			if !ok {
				return m, tea.Batch(cmds...)
			}
			m.mcpMenu = false
			status, err := mcpconfig.Configure(m.engine.Repo.Root, agent, m.engine.Config.Sensei.Command, m.engine.Config.Sensei.Args)
			if err != nil {
				m.lines = append(m.lines, errorStyle.Render("✗ MCP"), "  "+mcpconfig.Label(agent)+": "+err.Error(), "")
			} else {
				m.lines = append(m.lines, senseiStyle.Render("◆ MCP"), "  "+mcpconfig.Label(agent)+": "+string(status.State)+" · "+status.Detail, "")
			}
			cmds = append(cmds, tea.ClearScreen)
			return m, tea.Batch(cmds...)
		}
		if m.loginMenu {
			key := msg.String()
			if key == "ctrl+c" {
				return m, tea.Quit
			}
			if key == "esc" {
				m.loginMenu = false
				return m, tea.Batch(cmds...)
			}
			id, err := provider.Parse(key)
			if err != nil {
				return m, tea.Batch(cmds...)
			}
			exe, err := os.Executable()
			if err != nil {
				m.lines = append(m.lines, errorStyle.Render("✗ PROVIDER"), "  resolve Sensei Code executable: "+err.Error())
				m.loginMenu = false
				return m, tea.Batch(cmds...)
			}
			m.loginMenu = false
			m.lines = append(m.lines, dimStyle.Render("provider login · "+provider.Label(id)))
			cmd := exec.Command(exe, "login", string(id))
			return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
				return providerLoginFinishedMsg{id: id, err: err}
			})
		}
		if m.pending != nil {
			key := msg.String()
			if key == "ctrl+c" {
				return m, tea.Quit
			}
			for _, option := range m.pending.Options {
				if key == option.ID {
					if m.engine.ResolveHuman(m.pendingTask, option.ID) {
						// The engine records the resolved choice and it renders
						// from that event; echoing it here printed it twice.
						m.pending = nil
						m.pendingTask = ""
						m.busy = true
					}
					return m, tea.Batch(cmds...)
				}
			}
			return m, tea.Batch(cmds...)
		}
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "shift+tab", "ctrl+o":
			m.mode = (m.mode + 1) % 2
			m.verbose = m.mode == modeAutoStreaming
			return m, tea.Batch(cmds...)
		case "enter":
			text := strings.TrimSpace(m.input.Value())
			if text == "/login" && !m.busy {
				m.input.Reset()
				m.loginMenu = true
				return m, tea.Batch(cmds...)
			}
			if text == "/mcp" && !m.busy {
				m.input.Reset()
				m.mcpMenu = true
				return m, tea.Batch(cmds...)
			}
			if text == "/clear" && !m.busy {
				m.input.Reset()
				m.lines = banner(false)
				m.activity = ""
				if err := m.engine.RotateSession(); err != nil {
					m.lines = append(m.lines, errorStyle.Render("✗ SESSION"), "  "+err.Error(), "")
				}
				return m, tea.Batch(cmds...)
			}
			if text != "" && !m.busy {
				m.busy = true
				m.startedAt = time.Now()
				m.frame = 0
				m.wordIdx++
				m.lines = append(m.lines, "", userStyle.Render("You"), promptGlyphStyle.Render("› ")+text, "")
				m.input.Reset()
				m.engine.Submit(m.ctx, text)
				cmds = append(cmds, tea.ClearScreen)
			}
			return m, tea.Batch(cmds...)
		}
	}
	updated, cmd := m.input.Update(msg)
	m.input = updated
	cmds = append(cmds, cmd)
	return m, tea.Batch(cmds...)
}

var (
	senseiStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#E95454"))
	architectStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#7AA2F7"))
	workerStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#9ECE6A"))
	userStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#E0AF68"))
	dimStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("#737AA2"))
	errorStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#F7768E"))
	authorityStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FF9E64"))

	// The composer sits between two rules with no side walls, so the prompt
	// reads as part of the page rather than a box dropped onto it.
	composerBorder = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), true, false).
			BorderForeground(lipgloss.Color("#FFFFFF")).
			Padding(0, 1)
	promptGlyphStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#E0AF68"))
	workingStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#7AA2F7"))
	modeStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF9E64"))
	hintStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("#565F89"))
)

func renderEvent(e event.Event) string {
	prefix := dimStyle.Render("•")
	// You converse with the architect. Workers are not conversation partners:
	// their output is indented under the architect as reported activity, so the
	// transcript reads as one dialogue rather than several.
	indent := "  "
	switch e.Source {
	case event.SourceSensei:
		prefix = senseiStyle.Render("◆ SENSEI")
	case event.SourceArchitect:
		prefix = architectStyle.Render("◈ ARCHITECT")
	case event.SourceReviewer:
		prefix = architectStyle.Render("◈ REVIEWER")
	case event.SourceClaude:
		prefix = "    " + workerStyle.Render("└ worker · Claude")
		indent = "      "
	case event.SourceCodex:
		prefix = "    " + workerStyle.Render("└ worker · Codex")
		indent = "      "
	case event.SourceGit:
		prefix = "    " + dimStyle.Render("└ git")
		indent = "      "
	case event.SourceTests:
		prefix = "    " + dimStyle.Render("└ tests")
		indent = "      "
	case event.SourceUser:
		prefix = userStyle.Render("⚑ YOU")
	}
	if e.Kind == event.DecisionRecorded {
		prefix = senseiStyle.Render("◆ DECISION")
		indent = "  "
	}
	if e.Kind == event.ChangeReported {
		prefix = senseiStyle.Render("◆ CHANGE REPORT")
		indent = "  "
	}
	if e.Kind == event.PlanProposed {
		prefix = architectStyle.Render("◈ ARCHITECT · PLAN")
		indent = "  "
	}
	if e.Kind == event.AuthorityRequired {
		prefix = authorityStyle.Render("⚑ HUMAN AUTHORITY")
		indent = "  "
	}
	if e.Kind == event.WorkflowFailed {
		prefix = errorStyle.Render("✗ FAILED")
		indent = "  "
	}
	if e.Kind == event.WorkflowCompleted {
		prefix = senseiStyle.Render("✓ READY")
		indent = "  "
	}
	if strings.TrimSpace(e.Summary) == "" {
		return prefix
	}
	return prefix + "\n" + indent + strings.ReplaceAll(strings.TrimSpace(e.Summary), "\n", "\n"+indent)
}

// statusLine is the thin bar directly above the composer. It reports what the
// system is doing right now, not what it has done.
func (m Model) statusLine() string {
	switch {
	case m.pending != nil:
		return authorityStyle.Render("⚑ human authority required — choose an option above")
	case m.mcpMenu:
		return workingStyle.Render("● Sensei MCP access — choose an agent to configure, Esc to cancel")
	case m.loginMenu:
		return workingStyle.Render("● provider login — choose 1-4, Esc to cancel")
	case m.busy:
		word := workingWords[m.wordIdx%len(workingWords)]
		spin := spinnerFrames[m.frame%len(spinnerFrames)]
		elapsed := time.Since(m.startedAt).Round(time.Second)
		line := workingStyle.Render(fmt.Sprintf("%s %s…", spin, word)) +
			hintStyle.Render(fmt.Sprintf("  (%s · ctrl+c to quit)", elapsed))
		if m.activity != "" {
			line += hintStyle.Render("  " + m.activity)
		}
		return line
	default:
		return hintStyle.Render("● ready — describe a task · /login · /mcp · /clear")
	}
}

// modeLine is the bar under the composer.
func (m Model) modeLine() string {
	return modeStyle.Render("»  "+m.mode.label()) +
		hintStyle.Render(" (shift+tab to cycle)")
}

func (m Model) View() tea.View {
	width := max(40, m.width)
	inner := max(20, width)

	var composer string
	switch {
	case m.pending != nil:
		composer = renderAuthority(*m.pending, inner)
	case m.mcpMenu:
		composer = renderMCP(m.engine.Repo.Root, inner)
	case m.loginMenu:
		composer = renderProviderLogin(inner)
	default:
		composer = composerBorder.Width(inner).Render(m.input.View())
	}

	bottom := m.statusLine() + "\n" + composer + "\n" + m.modeLine()

	// The composer owns the bottom of the screen; the transcript gets the rest
	// and is anchored to its foot, so the newest line always sits directly above
	// the prompt and older lines scroll off the top.
	available := max(3, m.height-lipgloss.Height(bottom))
	rows := wrapRows(m.lines, width)
	if len(rows) > available {
		rows = rows[len(rows)-available:]
	}
	body := strings.Join(rows, "\n")
	if pad := available - len(rows); pad > 0 {
		blank := strings.Repeat(" ", width)
		head := make([]string, pad)
		for i := range head {
			head[i] = blank
		}
		body = strings.Join(head, "\n") + "\n" + body
	}

	v := tea.NewView(body + "\n" + bottom)
	v.AltScreen = true
	v.WindowTitle = "Sensei Code"
	return v
}

func renderProviderLogin(width int) string {
	var b strings.Builder
	b.WriteString(architectStyle.Render("Provider login"))
	b.WriteString("\n\nCredentials stay with each native provider client. Sensei Code stores no OAuth tokens.\n")
	for i, id := range provider.Ordered {
		b.WriteString(fmt.Sprintf("\n  %d. %s", i+1, provider.Label(id)))
	}
	b.WriteString("\n\nEsc. Cancel")
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(1, 2).Width(max(30, width)).Render(b.String())
}

func renderAuthority(d authority.Decision, width int) string {
	var b strings.Builder
	b.WriteString(authorityStyle.Render("⚑ HUMAN AUTHORITY REQUIRED"))
	b.WriteString("\n\n")
	b.WriteString(d.Subject)
	if d.Reason != "" {
		b.WriteString("\n\n")
		b.WriteString(dimStyle.Render(d.Reason))
	}
	if d.Recommendation != "" {
		b.WriteString("\n\nArchitect recommendation: ")
		b.WriteString(d.Recommendation)
	}
	b.WriteString("\n")
	for _, option := range d.Options {
		b.WriteString("\n  ")
		b.WriteString(option.ID)
		b.WriteString(". ")
		b.WriteString(option.Label)
		if option.Description != "" {
			b.WriteString("\n     ")
			b.WriteString(dimStyle.Render(option.Description))
		}
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(1, 2).Width(max(30, width)).Render(b.String())
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// isConversation reports whether an event is part of the dialogue with the
// architect. Sensei receipts, worker output, git and retry notices are activity
// about the work, not turns in the conversation, and would otherwise bury the
// few lines the human actually needs to read.
func isConversation(e event.Event) bool {
	switch e.Kind {
	case event.ArchitectSpoke, event.PlanProposed, event.ChangeReported, event.AuthorityRequired, event.AuthorityResolved, event.WorkflowFailed:
		return true
	case event.DecisionRecorded:
		// Whether the reason for this work reached Sensei is the architect's
		// business. Filed as activity it was never shown, so a decision that
		// went unrecorded looked exactly like one that was captured.
		return true
	case event.TaskCreated, event.Output, event.SenseiResult:
		return false
	case event.WorkflowCompleted:
		// A bare completion after a conversational reply has nothing to add.
		return strings.TrimSpace(e.Summary) != ""
	}
	// Agent lifecycle ("codex started") is activity, not speech.
	if e.Kind == event.AgentStarted || e.Kind == event.AgentFinished {
		return false
	}
	// The architect announcing its bounded decision is the architect speaking,
	// but only when it actually said something.
	return e.Source == event.SourceArchitect && e.Kind == event.Status &&
		strings.TrimSpace(e.Summary) != ""
}

// currentPhase names what the system is doing, for the bar above the prompt.
// It deliberately ignores raw agent output: streaming every stdout line through
// the status bar replaced it several times a second, which is motion rather
// than information. An architect wants to know which stage is running, not to
// watch a worker's console scroll past unreadably.
func currentPhase(e event.Event) string {
	if e.Kind == event.Output {
		return ""
	}
	switch e.Kind {
	case event.SenseiResult:
		return "consulting Sensei"
	case event.CandidateAudited:
		return "Sensei auditing the candidate diff"
	case event.CandidateChanged:
		return "candidate diff ready"
	}
	switch e.Source {
	case event.SourceArchitect:
		if e.Kind == event.AgentStarted {
			return "architect thinking"
		}
		return ""
	case event.SourceReviewer:
		if e.Kind == event.AgentStarted {
			return "reviewer reading the candidate"
		}
		return ""
	case event.SourceClaude, event.SourceCodex:
		if e.Kind == event.AgentStarted {
			return "worker " + workerLabel(e.Source) + " implementing"
		}
		return ""
	case event.SourceGit:
		return "preparing the candidate worktree"
	case event.SourceTests:
		return "running tests"
	case event.SourceSystem:
		// Retries and fallbacks are infrequent and worth reading.
		return firstLine(e.Summary)
	}
	return ""
}

func workerLabel(source event.Source) string {
	if source == event.SourceClaude {
		return "Claude"
	}
	return "Codex"
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 60 {
		s = s[:57] + "…"
	}
	return s
}

// wrapRows expands logical transcript lines into the screen rows they actually
// occupy, so the bottom-anchored view counts real rows rather than entries.
func wrapRows(lines []string, width int) []string {
	blank := strings.Repeat(" ", width)
	out := make([]string, 0, len(lines))
	// A transcript entry may itself be several lines; each is wrapped on its own
	// so indents are computed per line rather than from the entry's first line.
	var flat []string
	for _, line := range lines {
		flat = append(flat, strings.Split(line, "\n")...)
	}
	for _, line := range flat {
		// Every row is padded to the full width. An unpadded short row leaves
		// the previous frame's characters behind on that line.
		if strings.TrimSpace(line) == "" {
			out = append(out, blank)
			continue
		}
		// Wrapped text keeps the logical line's indent, so a continuation stays
		// visually inside the section it belongs to instead of resetting to the
		// left margin and reading like a new speaker.
		indent := line[:len(line)-len(strings.TrimLeft(line, " "))]
		body := strings.TrimLeft(line, " ")
		wrap := lipgloss.NewStyle().Width(max(8, width-len(indent)))
		for _, row := range strings.Split(wrap.Render(body), "\n") {
			out = append(out, padRow(indent+row, width))
		}
	}
	return out
}

// padRow extends a row to the full terminal width. Rows are measured by visible
// cells, not bytes, so styled text pads correctly.
func padRow(row string, width int) string {
	if gap := width - lipgloss.Width(row); gap > 0 {
		return row + strings.Repeat(" ", gap)
	}
	return row
}

func mcpAgentFor(key string) (mcpconfig.Agent, bool) {
	for i, agent := range mcpconfig.Ordered {
		if key == fmt.Sprintf("%d", i+1) {
			return agent, true
		}
	}
	return "", false
}

// renderMCP shows where each agent gets Sensei from. An agent whose
// configuration Sensei Code cannot read is shown as unknown rather than as
// working, because an unverified route to Sensei is not a route to Sensei.
func renderMCP(repoRoot string, width int) string {
	var b strings.Builder
	b.WriteString(architectStyle.Render("Sensei MCP access"))
	b.WriteString("\n\nEach agent reaches Sensei through its own MCP configuration, so what it\nsees is what Sensei said. Sensei Code never answers for Sensei.\n")
	for i, status := range mcpconfig.Describe(repoRoot) {
		mark := dimStyle.Render("○")
		switch status.State {
		case mcpconfig.Configured:
			mark = workerStyle.Render("●")
		case mcpconfig.Missing:
			mark = authorityStyle.Render("○")
		}
		b.WriteString(fmt.Sprintf("\n  %d. %s %-20s %s", i+1, mark, mcpconfig.Label(status.Agent), status.State))
		if status.Detail != "" {
			b.WriteString("\n       " + dimStyle.Render(status.Detail))
		}
	}
	b.WriteString("\n\nChoose a number to configure it. Esc. Cancel")
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(1, 2).Width(max(30, width)).Render(b.String())
}
