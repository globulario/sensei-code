package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/globulario/sensei-code/internal/agent"
	"github.com/globulario/sensei-code/internal/architect"
	"github.com/globulario/sensei-code/internal/authority"
	"github.com/globulario/sensei-code/internal/candidate"
	"github.com/globulario/sensei-code/internal/command"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/mcpconfig"
	"github.com/globulario/sensei-code/internal/provider"
	"github.com/globulario/sensei-code/internal/sensei"
	"github.com/globulario/sensei-code/internal/session"
	"github.com/globulario/sensei-code/internal/setup"
	"github.com/globulario/sensei-code/internal/workflow"
)

type eventMsg event.Event
type closedMsg struct{}
type tickMsg time.Time

// commandResultMsg carries the finished output of an architect command. They
// query Sensei and can take seconds, so they run off the update loop and the
// composer stays usable while they do.
type commandResultMsg struct {
	name string
	text string
	err  error
}
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
	currentTask   string
	resumable     []session.Interrupted
	// lastResponse is the most recent thing the architect said, kept as the
	// plain text the event carried. Recovering it from `lines` would mean
	// parsing speaker headings back out of rendered styling, which is a guess
	// about presentation; the event already knows.
	lastResponse string
	// mouseOff releases the mouse so the terminal can select text again. See
	// the View comment: tracking the wheel costs drag-to-select.
	mouseOff bool
	// pasteWaiting is set between asking the terminal for the clipboard and
	// hearing back, so an unanswered read can explain itself once.
	pasteWaiting bool
	// running names the architect command awaiting its result, so the status
	// bar can say which one rather than only that something is happening.
	running string
	pending *authority.Decision
	mode    mode
	// taskMode and taskModeWhy are the mode of the task actually running, taken
	// from the engine's mode.selected event. They are never derived from
	// configuration: the bar must report what is happening, not what a setting
	// implies.
	taskMode    string
	taskModeWhy string
	frame       int
	startedAt   time.Time
	wordIdx     int
	activity    string
	// scrollUp is how many rows above the newest the transcript is scrolled,
	// zero meaning it follows new output.
	//
	// It is an offset from the bottom rather than an absolute row, because the
	// bottom is the only position both this and the renderer can agree on
	// without duplicating the height arithmetic. An earlier version stored the
	// top row and recomputed it here with a slightly different formula, so a
	// one-line scroll was clamped straight back and the arrow keys appeared
	// dead.
	scrollUp int
}

func New(ctx context.Context, engine *workflow.Engine, events <-chan event.Event, history []event.Event) Model {
	ta := textarea.New()
	ta.Placeholder = "Describe a task for Sensei Code..."
	ta.Prompt = "› "
	ta.SetHeight(1)
	ta.SetWidth(80)
	ta.ShowLineNumbers = false
	focusCmd := ta.Focus()
	m := Model{
		ctx:     ctx,
		engine:  engine,
		events:  events,
		input:   ta,
		initCmd: focusCmd,
		lines:   append(banner(len(history) > 0), replayConversation(history)...),
		// A task that was approved and then interrupted still has its candidate
		// on disk. Offering it is the difference between resuming work and
		// silently abandoning it.
		resumable: session.FindInterrupted(history),
	}
	if n := len(m.resumable); n > 0 {
		m.lines = append(m.lines,
			authorityStyle.Render("⚑ INTERRUPTED"),
			"  "+m.resumable[n-1].Task,
			dimStyle.Render("  its candidate is still on disk · /resume to continue it"), "")
	}
	return m
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
	case tea.MouseWheelMsg:
		switch msg.Button {
		case tea.MouseWheelUp:
			m.scrollUp += 3
		case tea.MouseWheelDown:
			m.scrollUp = max(0, m.scrollUp-3)
		}
		cmds = append(cmds, tea.ClearScreen)
	case tickMsg:
		if m.busy || m.running != "" {
			m.frame++
			// Change the word every ~5s so it reads as progress, not noise.
			if m.frame%(int(5*time.Second/tickInterval)) == 0 {
				m.wordIdx++
			}
		}
		return m, tick()
	case commandResultMsg:
		m.running = ""
		if msg.err != nil {
			m.lines = append(m.lines, errorStyle.Render("✗ "+strings.ToUpper(strings.TrimPrefix(msg.name, "/"))),
				"  "+msg.err.Error(), "")
		} else {
			m.lines = append(m.lines, senseiStyle.Render("◆ "+strings.ToUpper(strings.TrimPrefix(msg.name, "/"))),
				indentBlock(msg.text, "  "), "")
		}
		cmds = append(cmds, tea.ClearScreen)
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
		if e.Kind == event.ModeSelected {
			// The mode shown is the one the engine reports for the task that is
			// actually running, with the reason it gives. Nothing here consults
			// configuration, so no local flag can make the bar claim a posture
			// the workflow is not in.
			var payload struct {
				Mode       string `json:"mode"`
				Provenance string `json:"provenance"`
			}
			if json.Unmarshal(e.Payload, &payload) == nil && payload.Mode != "" {
				m.taskMode = payload.Mode
				m.taskModeWhy = payload.Provenance
			}
		}
		// The transcript is the conversation with the architect. Sensei
		// receipts, worker output, git and retries are activity: they feed the
		// status bar, and reach the transcript only in streaming mode.
		if isConversation(e) {
			if line := renderEvent(e); line != "" {
				// Not everything in the transcript is a response. Guidance is
				// the human's own words queued for a worker, and handing it
				// back as "the last response" would be the interface quoting
				// the reader to themselves.
				if text := strings.TrimSpace(e.Summary); text != "" && e.Source != event.SourceUser {
					m.lastResponse = text
				}
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
				if line := renderActivity(e); line != "" {
					m.lines = append(m.lines, line)
					cmds = append(cmds, tea.ClearScreen)
				}
			}
		}
		if e.Kind == event.WorkflowCompleted || e.Kind == event.WorkflowFailed ||
			e.Kind == event.WorkflowStopped || e.Kind == event.WorkflowAwaitingAuthority {
			m.busy = false
			m.pending = nil
			m.pendingTask = ""
		}
		cmds = append(cmds, waitEvent(m.events))
	case tea.ClipboardMsg:
		m.pasteWaiting = false
		text := msg.Content
		if text == "" {
			return m, tea.Batch(cmds...)
		}
		updated, cmd := m.input.Update(tea.PasteMsg{Content: text})
		m.input = updated
		return m, tea.Batch(append(cmds, cmd, tea.ClearScreen)...)
	case pasteUnansweredMsg:
		if !m.pasteWaiting {
			return m, tea.Batch(cmds...)
		}
		m.pasteWaiting = false
		m.lines = append(m.lines, dimStyle.Render(
			"this terminal did not answer a clipboard read — use its own paste (usually ctrl+shift+v), which arrives here intact"), "")
		return m, tea.Batch(append(cmds, tea.ClearScreen)...)
	case closedMsg:
		return m, tea.Quit
	case tea.KeyPressMsg:
		if updated, handled := m.scroll(msg.String()); handled {
			return updated, tea.Batch(append(cmds, tea.ClearScreen)...)
		}
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
			status, err := mcpconfig.Configure(m.engine.Repo.Root, agent, m.engine.Config.Sensei.Command, m.engine.Config.Sensei.Args, m.engine.Config.SenseiAddressIsStated())
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
			if key == "esc" {
				// Esc at an authority boundary defers the question; it does not
				// answer it. Once Sensei has established that this decision is
				// a person's, there is no architect-authorized continuation
				// left, so a keystroke must not be able to mean "no" — that
				// would be a third answer nobody gave. The question is kept
				// whole and /resume asks it again.
				if !m.engine.DeferAuthority(m.pendingTask) {
					return m, tea.Batch(cmds...)
				}
				m.pending = nil
				m.pendingTask = ""
				m.busy = false
				m.lines = append(m.lines, dimStyle.Render("deferred · the question stands, /resume asks it again"), "")
				cmds = append(cmds, tea.ClearScreen)
				return m, tea.Batch(cmds...)
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
		case "ctrl+y":
			// Copy the composer. A single-line composer has no selection, so
			// the whole line is the only thing "copy" can honestly mean.
			text := m.input.Value()
			if strings.TrimSpace(text) == "" {
				m.lines = append(m.lines, dimStyle.Render("nothing to copy: the composer is empty  ·  ctrl+r copies the last response"), "")
				return m, tea.Batch(append(cmds, tea.ClearScreen)...)
			}
			m.lines = append(m.lines, dimStyle.Render("copied the composer"), "")
			return m, tea.Batch(append(cmds, copyToClipboard(text), tea.ClearScreen)...)
		case "ctrl+x":
			text := m.input.Value()
			if strings.TrimSpace(text) == "" {
				m.lines = append(m.lines, dimStyle.Render("nothing to cut: the composer is empty"), "")
				return m, tea.Batch(append(cmds, tea.ClearScreen)...)
			}
			m.input.Reset()
			m.lines = append(m.lines, dimStyle.Render("cut the composer"), "")
			return m, tea.Batch(append(cmds, copyToClipboard(text), tea.ClearScreen)...)
		case "ctrl+r":
			if strings.TrimSpace(m.lastResponse) == "" {
				m.lines = append(m.lines, dimStyle.Render("nothing to copy: no response yet"), "")
				return m, tea.Batch(append(cmds, tea.ClearScreen)...)
			}
			m.lines = append(m.lines, dimStyle.Render("copied the last response"), "")
			return m, tea.Batch(append(cmds, copyToClipboard(m.lastResponse), tea.ClearScreen)...)
		case "ctrl+v":
			// Ask the terminal for its clipboard. The textarea's own ctrl+v
			// shells out to xclip and is shadowed here deliberately; see
			// clipboard.go for why that path is not trustworthy.
			m.pasteWaiting = true
			return m, tea.Batch(append(cmds, tea.ReadClipboard, pasteHint())...)
		case "esc":
			// The human's "no" during a governed run. There is no approval
			// prompt to decline any more, so this is where withdrawing
			// attention happens — and it has to reach the worker process
			// rather than merely stop drawing, or the interface would be
			// lying about what it just did.
			//
			// Nothing is cleaned up: the candidate stays as it stands, so the
			// work can be resumed. Esc on an assisted turn does nothing, and
			// says so, because a conversation has nothing to withdraw.
			if !m.busy || m.currentTask == "" {
				return m, tea.Batch(cmds...)
			}
			if !m.engine.Stop(m.currentTask) {
				return m, tea.Batch(cmds...)
			}
			m.lines = append(m.lines, dimStyle.Render("stopped · the candidate is left as it stands, /resume picks it up"), "")
			cmds = append(cmds, tea.ClearScreen)
			return m, tea.Batch(cmds...)
		case "shift+tab", "ctrl+o":
			m.mode = (m.mode + 1) % 2
			m.verbose = m.mode == modeAutoStreaming
			return m, tea.Batch(cmds...)
		case "enter":
			text := strings.TrimSpace(m.input.Value())
			if text == "" {
				return m, tea.Batch(cmds...)
			}
			if cmd, arg, ok := command.Lookup(text); ok {
				m.input.Reset()
				model, c := m.runCommand(cmd, arg)
				return model, tea.Batch(append(cmds, c)...)
			}
			if command.IsUnknown(text) {
				// A mistyped command handed to the architect arrives as a
				// feature request, which is worse than saying it is unknown.
				m.input.Reset()
				line := "unknown command " + strings.Fields(text)[0]
				if guess := command.Suggest(text); guess != "" {
					line += " · did you mean " + guess + "?"
				}
				m.lines = append(m.lines, dimStyle.Render(line+"  ·  /help lists them all"), "")
				cmds = append(cmds, tea.ClearScreen)
				return m, tea.Batch(cmds...)
			}
			if m.busy {
				// Work already running: the message becomes guidance rather
				// than being swallowed. A worker mid-cycle cannot be
				// interrupted, so it is queued for the next one and the line
				// says so instead of implying it landed immediately.
				if m.engine.Note(m.currentTask, text) {
					m.input.Reset()
					m.lines = append(m.lines, "", userStyle.Render("You")+dimStyle.Render("  · queued for the next worker cycle"),
						promptGlyphStyle.Render("› ")+text, "")
					cmds = append(cmds, tea.ClearScreen)
				}
				return m, tea.Batch(cmds...)
			}
			m.busy = true
			m.startedAt = time.Now()
			m.frame = 0
			m.wordIdx++
			m.lines = append(m.lines, "", userStyle.Render("You"), promptGlyphStyle.Render("› ")+text, "")
			m.input.Reset()
			m.currentTask = m.engine.SubmitAssisted(m.ctx, text)
			m.scrollUp = 0
			cmds = append(cmds, tea.ClearScreen)
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
	if e.Kind == event.GuidanceDelivered {
		prefix = userStyle.Render("⚑ YOUR GUIDANCE") + dimStyle.Render("  · read by the worker")
		indent = "  "
	}
	if e.Kind == event.DecisionRecorded {
		prefix = senseiStyle.Render("◆ DECISION")
		indent = "  "
	}
	if e.Kind == event.ContextConsulted {
		// Collapsed with the rest of the activity by default. It is there to be
		// checked when an answer looks wrong, not to be read every turn.
		prefix = dimStyle.Render("· CONTEXT")
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
	if e.Kind == event.WorkflowAwaitingAuthority {
		prefix = dimStyle.Render("◇ DEFERRED")
		indent = "  "
	}
	if e.Kind == event.WorkflowStopped {
		// Not styled as an error. Nothing went wrong; the human changed their
		// mind, and rendering that in red would teach them that stopping is a
		// fault rather than a normal use of their own authority.
		prefix = dimStyle.Render("■ STOPPED")
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
	case m.running != "":
		spin := spinnerFrames[m.frame%len(spinnerFrames)]
		return workingStyle.Render(spin+" "+m.running) + hintStyle.Render("  asking Sensei…")
	case m.busy:
		word := workingWords[m.wordIdx%len(workingWords)]
		spin := spinnerFrames[m.frame%len(spinnerFrames)]
		elapsed := time.Since(m.startedAt).Round(time.Second)
		line := workingStyle.Render(fmt.Sprintf("%s %s…", spin, word)) +
			hintStyle.Render(fmt.Sprintf("  (%s · ctrl+c to quit)", elapsed))
		if m.activity != "" {
			line += hintStyle.Render("  " + m.activity)
		}
		return line + hintStyle.Render("  · type to steer")
	default:
		return hintStyle.Render("● ready — describe a task, or /help for commands")
	}
}

// modeLine is the bar under the composer.
//
// It names the task mode first because that is the consequential one: assisted
// changes nothing, governed cuts a candidate and asks for approval. The
// display posture (streaming or not) comes second, since it only affects how
// much scrolls past.
func (m Model) modeLine() string {
	task := m.taskMode
	if task == "" {
		task = "assisted"
	}
	line := modeStyle.Render("»  " + task)
	if m.taskModeWhy != "" {
		line += hintStyle.Render(" · " + m.taskModeWhy)
	}
	if task == "assisted" {
		line += hintStyle.Render("  — /run to carry work out")
	}
	return line + hintStyle.Render("  ·  "+m.mode.label()+" (shift+tab to cycle)")
}

func (m Model) View() tea.View {
	width := max(40, m.width)
	inner := max(20, width)

	var composer string
	switch {
	case m.pending != nil:
		composer = renderAuthority(*m.pending, inner)
	case m.mcpMenu:
		composer = renderMCP(m.engine.Repo.Root, m.engine.Config.Sensei.Args, m.engine.Config.SenseiAddressIsStated(), inner)
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

	// The window is clamped here rather than when the key is pressed, because
	// how many rows the transcript occupies depends on the width it is wrapped
	// to, and that is only known while rendering.
	hidden, visible := 0, available
	if len(rows) > available {
		// The "earlier lines" note occupies a row, so it takes one from the
		// content rather than overwriting it: hiding a line to announce that
		// lines are hidden is the opposite of the point.
		visible = available - 1
		// Clamped here, where the wrapped row count is finally known. Scrolling
		// past the start simply rests at the start.
		top := len(rows) - visible - min(m.scrollUp, max(0, len(rows)-visible))
		hidden = top
		rows = rows[top : top+visible]
	}
	body := strings.Join(rows, "\n")
	if pad := available - lipgloss.Height(body); pad > 0 {
		blank := strings.Repeat(" ", width)
		head := make([]string, pad)
		for i := range head {
			head[i] = blank
		}
		body = strings.Join(head, "\n") + "\n" + body
	}

	if len(rows) < available {
		// Say what is above and how to reach it; a reader otherwise has no way
		// to know the transcript continues past the top of the window.
		note := fmt.Sprintf("  ↑ %d earlier line%s · ↑↓ or wheel to scroll · ctrl+g for the end", hidden, plural(hidden))
		if hidden == 0 {
			note = "  ↑ top of the conversation · ↓ or ctrl+g to return"
		}
		body = padRow(hintStyle.Render(note), width) + "\n" + body
	}
	v := tea.NewView(body + "\n" + bottom)
	// The wheel is what most people try first, and in the alternate screen the
	// terminal's own scrollback is unavailable, so without this there is no
	// pointer gesture that works at all.
	//
	// It is not free: while the program is tracking the mouse, the terminal
	// stops treating a drag as a selection, so the wheel is bought with
	// copy-by-mouse. Neither is obviously worth more than the other, so /mouse
	// hands the choice back rather than this line deciding for everyone. Many
	// terminals also let shift+drag select while tracking is on.
	v.MouseMode = tea.MouseModeCellMotion
	if m.mouseOff {
		v.MouseMode = tea.MouseModeNone
	}
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
	case event.ArchitectSpoke, event.PlanProposed, event.ChangeReported, event.AuthorityRequired, event.AuthorityResolved,
		event.WorkflowFailed, event.WorkflowStopped, event.WorkflowAwaitingAuthority:
		return true
	case event.ArchitectReconciliation:
		// Why the loop took the branch it took when two agents disagreed. Filed
		// as activity it would scroll past, and the next thing the human sees is
		// a run that changed course for no visible reason.
		return true
	case event.GuidanceDelivered:
		return true
	case event.DecisionRecorded:
		// Whether the reason for this work reached Sensei is the architect's
		// business. Filed as activity it was never shown, so a decision that
		// went unrecorded looked exactly like one that was captured.
		return true
	case event.TaskCreated, event.Output, event.SenseiResult, event.ModeSelected:
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

// renderActivity shows streamed agent work as a readable trace rather than the
// raw protocol it arrives in, so an architect watching a run can see a worker
// reading the wrong file or running the wrong command while there is still time
// to say so.
func renderActivity(e event.Event) string {
	if e.Kind != event.Output {
		return renderEvent(e)
	}
	name := "codex"
	if e.Source == event.SourceClaude {
		name = "claude"
	}
	text := agent.Activity(name, e.Summary)
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return "    " + dimStyle.Render("· "+text)
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
	// A finding is the only part of a review a worker can act on, so it stays
	// visible: an adversarial loop whose findings scroll past as unlabelled
	// agent output is one nobody can follow.
	switch e.Kind {
	case event.ReviewStarted, event.ReviewFinding, event.ReviewCompleted:
		return firstLine(e.Summary)
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
func renderMCP(repoRoot string, want []string, stated bool, width int) string {
	var b strings.Builder
	b.WriteString(architectStyle.Render("Sensei MCP access"))
	b.WriteString("\n\nEach agent reaches Sensei through its own MCP configuration, so what it\nsees is what Sensei said. Sensei Code never answers for Sensei.\n")
	for i, status := range mcpconfig.Describe(repoRoot, want, stated) {
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

// runCommand dispatches one architect command. Commands that need Sensei run
// off the update loop, so a query taking seconds never freezes the composer.
func (m Model) runCommand(c command.Command, arg string) (Model, tea.Cmd) {
	if c.NeedsIdle && m.busy {
		m.lines = append(m.lines,
			dimStyle.Render(c.Name+" needs an idle session; a task is running. Type to steer it, or wait."), "")
		return m, tea.ClearScreen
	}
	m.lines = append(m.lines, "", userStyle.Render("You"), promptGlyphStyle.Render("› ")+c.Name+argSuffix(arg), "")

	switch c.Name {
	case "/help":
		m.lines = append(m.lines, renderHelp(), "")
		return m, tea.ClearScreen
	case "/login":
		m.loginMenu = true
		return m, nil
	case "/mcp":
		m.mcpMenu = true
		return m, nil
	case "/copy":
		what := strings.ToLower(strings.TrimSpace(arg))
		switch what {
		case "", "last":
			if strings.TrimSpace(m.lastResponse) == "" {
				m.lines = append(m.lines, dimStyle.Render("nothing to copy: no response yet"), "")
				return m, tea.ClearScreen
			}
			m.lines = append(m.lines, dimStyle.Render("copied the last response"), "")
			return m, tea.Batch(copyToClipboard(m.lastResponse), tea.ClearScreen)
		case "all":
			text := plainTranscript(m.lines)
			if text == "" {
				m.lines = append(m.lines, dimStyle.Render("nothing to copy: the transcript is empty"), "")
				return m, tea.ClearScreen
			}
			m.lines = append(m.lines, dimStyle.Render("copied the transcript"), "")
			return m, tea.Batch(copyToClipboard(text), tea.ClearScreen)
		default:
			m.lines = append(m.lines, dimStyle.Render("/copy takes nothing (the last response) or 'all' (the transcript)"), "")
			return m, tea.ClearScreen
		}
	case "/mouse":
		m.mouseOff = !m.mouseOff
		if m.mouseOff {
			m.lines = append(m.lines, dimStyle.Render("mouse released · drag to select and copy with your terminal; the wheel no longer scrolls here (arrows, pgup and ctrl+u still do)"), "")
		} else {
			m.lines = append(m.lines, dimStyle.Render("mouse captured · the wheel scrolls again, and the terminal will not drag-select"), "")
		}
		return m, tea.ClearScreen
	case "/clear":
		m.lines = banner(false)
		m.activity = ""
		m.resumable = nil
		if err := m.engine.RotateSession(); err != nil {
			m.lines = append(m.lines, errorStyle.Render("✗ SESSION"), "  "+err.Error(), "")
		}
		return m, tea.ClearScreen
	case "/resume":
		if len(m.resumable) == 0 {
			m.lines = append(m.lines, dimStyle.Render("nothing to resume: no task was left mid-flight or holding a deferred decision"), "")
			return m, tea.ClearScreen
		}
		task := m.resumable[len(m.resumable)-1]
		m.resumable = nil
		m.busy = true
		m.startedAt = time.Now()
		m.frame = 0
		m.currentTask = m.engine.Resume(m.ctx, task)
		return m, tea.ClearScreen
	case "/run":
		if strings.TrimSpace(arg) == "" {
			m.lines = append(m.lines, dimStyle.Render("/run needs the work to carry out: /run add a --json flag to the report command"), "")
			return m, tea.ClearScreen
		}
		m.busy = true
		m.startedAt = time.Now()
		m.frame = 0
		m.currentTask = m.engine.SubmitGoverned(m.ctx, strings.TrimSpace(arg))
		return m, tea.ClearScreen
	case "/refactor":
		if strings.TrimSpace(arg) == "" {
			m.lines = append(m.lines, dimStyle.Render("/refactor needs a target: /refactor internal/tui"), "")
			return m, tea.ClearScreen
		}
		// A refactor is a change, so it goes through the same governed path as
		// any other task rather than round the side of it.
		m.busy = true
		m.startedAt = time.Now()
		m.frame = 0
		m.currentTask = m.engine.SubmitGoverned(m.ctx, "Propose a governed refactor of "+arg+
			". Read Sensei's evidence for it first, and keep the plan bounded.")
		return m, tea.ClearScreen
	}

	m.running = c.Name
	return m, tea.Batch(tea.ClearScreen, m.senseiCommand(c, arg))
}

func argSuffix(arg string) string {
	if strings.TrimSpace(arg) == "" {
		return ""
	}
	return " " + arg
}

// senseiCommand runs a read-only Sensei query in the background.
func (m Model) senseiCommand(c command.Command, arg string) tea.Cmd {
	ctx, engine, width := m.ctx, m.engine, max(60, m.width)
	return func() tea.Msg {
		switch c.Name {
		case "/gate":
			text, err := architect.RunGate(ctx, engine.Repo.Root, senseiDomain(ctx, engine))
			return commandResultMsg{name: c.Name, text: text, err: err}
		case "/audit":
			text, err := architect.RunAudit(ctx, engine.Repo.Root)
			return commandResultMsg{name: c.Name, text: text, err: err}
		case "/debt":
			text, err := architect.RunDebt(engine.Repo.Root, 12)
			return commandResultMsg{name: c.Name, text: text, err: err}
		case "/candidates":
			list, err := candidate.List(engine.Repo.Root)
			if err != nil {
				return commandResultMsg{name: c.Name, err: err}
			}
			return commandResultMsg{name: c.Name, text: candidate.Render(list)}
		case "/setup":
			// Report only. The checks carry their repairs so `sensei-code setup
			// --apply` can act on the same report, but none is invoked here: a
			// session that quietly rewrote a shared graph, the domain registry or
			// an agent's MCP configuration because somebody asked what was wrong
			// would be exactly the silent rearrangement the CLI refuses to do.
			return commandResultMsg{name: c.Name, text: setup.InspectRepository(ctx, engine.Repo.Root, engine.Config).Render()}
		}

		client, err := sensei.Start(ctx, engine.Repo.Root, engine.Config.Sensei.Command, engine.Config.Sensei.Args)
		if err != nil {
			return commandResultMsg{name: c.Name, err: err}
		}
		defer client.Close()
		domain := domainFrom(client, engine.Repo.Root)
		switch c.Name {
		case "/report":
			text, err := architect.RunReport(client, domain, width)
			// The Level-1 dark run is appended rather than merged: it is this
			// session's own measurement of its own runs, not something the
			// graph holds, and printing it under Sensei's report would attribute
			// it to Sensei.
			if line := routineDarkRun(engine); line != "" {
				text = strings.TrimRight(text, "\n") + "\n\n" + line + "\n"
			}
			return commandResultMsg{name: c.Name, text: text, err: err}
		case "/focus":
			text, err := architect.RunFocus(client, domain, arg)
			return commandResultMsg{name: c.Name, text: text, err: err}
		case "/why":
			text, err := architect.RunWhy(client, domain, arg)
			return commandResultMsg{name: c.Name, text: text, err: err}
		case "/learn":
			text, err := architect.RunLearn(client, domain, arg)
			return commandResultMsg{name: c.Name, text: text, err: err}
		}
		return commandResultMsg{name: c.Name, err: errors.New("this command has no implementation yet")}
	}
}

// routineDarkRun renders what the Level-1 classification has observed so far,
// or "" when it has observed nothing. A session that has run no governed task
// prints no line at all, because a tally of zero out of zero reads like a
// finding and is not one.
func routineDarkRun(engine *workflow.Engine) string {
	if engine == nil || engine.Store == nil {
		return ""
	}
	events, err := engine.Store.Load()
	if err != nil {
		return ""
	}
	return workflow.RoutineSummary(events).Render()
}

// senseiDomain resolves the repository domain for CLI-backed commands, which
// need it to scope a graph that hosts more than one repository.
func senseiDomain(ctx context.Context, engine *workflow.Engine) string {
	client, err := sensei.Start(ctx, engine.Repo.Root, engine.Config.Sensei.Command, engine.Config.Sensei.Args)
	if err != nil {
		return ""
	}
	defer client.Close()
	return domainFrom(client, engine.Repo.Root)
}

func domainFrom(client *sensei.Client, repoRoot string) string {
	status, err := client.CallTool("sensei_workspace_status", map[string]any{"repo": repoRoot})
	if err != nil {
		return ""
	}
	return sensei.RepositoryDomain(status)
}

// renderHelp is generated from the registry so it cannot describe a command
// that does not exist, or omit one that does.
func renderHelp() string {
	var b strings.Builder
	b.WriteString(architectStyle.Render("Commands"))
	for _, group := range command.Groups() {
		b.WriteString("\n\n  " + group.Title + "\n")
		for _, c := range group.Commands {
			name := c.Name
			if c.Arg != "" {
				name += " " + c.Arg
			}
			b.WriteString(fmt.Sprintf("    %-28s %s\n", name, c.Summary))
		}
	}
	b.WriteString("\n\n  Clipboard\n")
	for _, k := range [][2]string{
		{"ctrl+r", "copy the last response"},
		{"ctrl+y", "copy the composer"},
		{"ctrl+x", "cut the composer"},
		{"ctrl+v", "paste (your terminal's own paste always works too)"},
	} {
		b.WriteString(fmt.Sprintf("    %-28s %s\n", k[0], k[1]))
	}
	b.WriteString("\n  Anything that is not a command is a task: describe it and the architect plans it.")
	return b.String()
}

// indentBlock shifts a rendered command result under its heading.
func indentBlock(text, prefix string) string {
	return prefix + strings.ReplaceAll(strings.TrimRight(text, "\n"), "\n", "\n"+prefix)
}

// scroll moves the transcript window. It reports whether it handled the key, so
// every other binding is left untouched.
func (m Model) scroll(key string) (Model, bool) {
	page := max(1, m.height-8)
	switch key {
	// Arrow keys scroll: the composer is a single line, so up and down do
	// nothing there, and they are the first thing a reader reaches for. PgUp is
	// kept but cannot be relied on alone, because terminals and multiplexers
	// routinely capture it for their own scrollback before the program sees it.
	case "up", "shift+up", "ctrl+p":
		m.scrollUp++
	case "down", "shift+down", "ctrl+n":
		m.scrollUp = max(0, m.scrollUp-1)
	case "ctrl+u":
		m.scrollUp += page / 2
	case "ctrl+d":
		m.scrollUp = max(0, m.scrollUp-page/2)
	case "pgup":
		m.scrollUp += page
	case "pgdown":
		m.scrollUp = max(0, m.scrollUp-page)
	case "ctrl+g", "end":
		// Back to following new output, without paging down through everything
		// that arrived while reading.
		m.scrollUp = 0
	default:
		return m, false
	}
	return m, true
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
