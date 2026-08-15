package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/globulario/sensei-code/internal/authority"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/workflow"
)

type eventMsg event.Event
type closedMsg struct{}

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
	pendingTask   string
	pending       *authority.Decision
}

func New(ctx context.Context, engine *workflow.Engine, events <-chan event.Event) Model {
	ta := textarea.New()
	ta.Placeholder = "Describe a task for Sensei Code..."
	ta.Prompt = "> "
	ta.SetHeight(3)
	ta.SetWidth(80)
	ta.ShowLineNumbers = false
	focusCmd := ta.Focus()
	return Model{ctx: ctx, engine: engine, events: events, input: ta, initCmd: focusCmd, lines: []string{"◆ SENSEI CODE", "  autonomous, governed development", ""}}
}

func (m Model) Init() tea.Cmd { return tea.Batch(m.initCmd, waitEvent(m.events)) }

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
		m.input.SetWidth(max(20, msg.Width-4))
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
		if e.Kind != event.Output || m.verbose {
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
		case "ctrl+o":
			m.verbose = !m.verbose
			state := "collapsed"
			if m.verbose {
				state = "visible"
			}
			m.lines = append(m.lines, dimStyle.Render("agent activity "+state))
			return m, tea.Batch(cmds...)
		case "enter":
			text := strings.TrimSpace(m.input.Value())
			if text != "" && !m.busy {
				m.busy = true
				m.lines = append(m.lines, "", userStyle.Render("You"), "> "+text)
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
)

func renderEvent(e event.Event) string {
	prefix := dimStyle.Render("•")
	switch e.Source {
	case event.SourceSensei:
		prefix = senseiStyle.Render("◆ SENSEI")
	case event.SourceArchitect:
		prefix = architectStyle.Render("◈ ARCHITECT")
	case event.SourceReviewer:
		prefix = architectStyle.Render("◈ REVIEWER")
	case event.SourceClaude:
		prefix = workerStyle.Render("● CLAUDE")
	case event.SourceCodex:
		prefix = workerStyle.Render("● CODEX")
	case event.SourceGit:
		prefix = dimStyle.Render("● GIT")
	case event.SourceTests:
		prefix = dimStyle.Render("✓ TESTS")
	case event.SourceUser:
		prefix = userStyle.Render("⚑ YOU")
	}
	if e.Kind == event.AuthorityRequired {
		prefix = authorityStyle.Render("⚑ HUMAN AUTHORITY")
	}
	if e.Kind == event.WorkflowFailed {
		prefix = errorStyle.Render("✗ FAILED")
	}
	if e.Kind == event.WorkflowCompleted {
		prefix = senseiStyle.Render("✓ READY")
	}
	if strings.TrimSpace(e.Summary) == "" {
		return prefix
	}
	return prefix + "\n  " + strings.ReplaceAll(strings.TrimSpace(e.Summary), "\n", "\n  ")
}

func (m Model) View() tea.View {
	available := max(3, m.height-7)
	start := max(0, len(m.lines)-available)
	body := strings.Join(m.lines[start:], "\n")

	var composer string
	if m.pending != nil {
		composer = renderAuthority(*m.pending, max(40, m.width-4))
	} else {
		composer = m.input.View()
	}

	status := "ready"
	if m.pending != nil {
		status = "human authority required · choose 1/2/3"
	} else if m.busy {
		status = "working autonomously"
	}
	activity := "collapsed"
	if m.verbose {
		activity = "visible"
	}
	footer := dimStyle.Render(fmt.Sprintf("%s · agent activity %s · Ctrl+O toggle · Ctrl+C quit", status, activity))
	content := body + "\n\n" + composer + "\n" + footer
	v := tea.NewView(content)
	v.AltScreen = true
	v.WindowTitle = "Sensei Code"
	return v
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
