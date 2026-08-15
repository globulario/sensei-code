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
	pendingTask   string
	pending       *authority.Decision
	mode          mode
	frame         int
	startedAt     time.Time
	wordIdx       int
}

func New(ctx context.Context, engine *workflow.Engine, events <-chan event.Event) Model {
	ta := textarea.New()
	ta.Placeholder = "Describe a task for Sensei Code..."
	ta.Prompt = "› "
	ta.SetHeight(1)
	ta.SetWidth(80)
	ta.ShowLineNumbers = false
	focusCmd := ta.Focus()
	return Model{ctx: ctx, engine: engine, events: events, input: ta, initCmd: focusCmd, lines: []string{"◆ SENSEI CODE", "  autonomous, governed development", ""}}
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
		// TaskCreated echoes the task text back, but the composer already
		// wrote it under "You", so rendering it again duplicates the prompt.
		if e.Kind != event.TaskCreated && (e.Kind != event.Output || m.verbose) {
			if line := renderEvent(e); line != "" {
				m.lines = append(m.lines, line)
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
						m.lines = append(m.lines, userStyle.Render("⚑ YOU")+"\n  "+option.ID+". "+option.Label)
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
			if text != "" && !m.busy {
				m.busy = true
				m.startedAt = time.Now()
				m.frame = 0
				m.wordIdx++
				m.lines = append(m.lines, "", userStyle.Render("You"), promptGlyphStyle.Render("› ")+text, "")
				m.input.Reset()
				m.engine.Submit(m.ctx, text)
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

	// The composer sits between two white rules, the way Claude Code frames its
	// prompt, so the input is always findable no matter how long the transcript.
	composerBorder = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
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
		prefix = "    " + workerStyle.Render("└ worker · claude")
		indent = "      "
	case event.SourceCodex:
		prefix = "    " + workerStyle.Render("└ worker · codex")
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
	case m.loginMenu:
		return workingStyle.Render("● provider login — choose 1-4, Esc to cancel")
	case m.busy:
		word := workingWords[m.wordIdx%len(workingWords)]
		spin := spinnerFrames[m.frame%len(spinnerFrames)]
		elapsed := time.Since(m.startedAt).Round(time.Second)
		return workingStyle.Render(fmt.Sprintf("%s %s…", spin, word)) +
			hintStyle.Render(fmt.Sprintf("  (%s · ctrl+c to quit)", elapsed))
	default:
		return hintStyle.Render("● ready — describe a task, or /login to connect a provider")
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
	case m.loginMenu:
		composer = renderProviderLogin(inner)
	default:
		composer = composerBorder.Width(inner).Render(m.input.View())
	}

	bottom := m.statusLine() + "\n" + composer + "\n" + m.modeLine()

	// Give the transcript whatever the composer block does not need, so the
	// prompt stays pinned to the bottom at any terminal height.
	available := max(3, m.height-lipgloss.Height(bottom))
	start := max(0, len(m.lines)-available)
	body := strings.Join(m.lines[start:], "\n")
	if pad := available - lipgloss.Height(body); pad > 0 {
		body += strings.Repeat("\n", pad)
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
