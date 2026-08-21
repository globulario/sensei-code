// Package command is the catalogue of what an architect can ask Sensei Code to
// do without starting a task.
//
// It exists so the surface is discoverable. An architect cannot use a
// capability they cannot find, and a tool whose commands live in an if-chain
// has no way to list them, explain them, or tell the difference between a typo
// and a command that does not exist yet. Every command is declared once here
// and the help is generated from that declaration, so the two can never drift.
package command

import (
	"sort"
	"strings"
)

// Kind groups commands in help so the list reads as a workflow rather than an
// alphabet.
type Kind string

const (
	// Understand answers questions about the codebase from Sensei's evidence.
	Understand Kind = "understand"
	// Control checks work in progress against what governs it.
	Control Kind = "control"
	// Record adds durable knowledge back to Sensei.
	Record Kind = "record"
	// Session manages the local session and providers.
	Session Kind = "session"
)

// Command is one thing the architect can invoke.
type Command struct {
	Name string
	// Arg names the argument in help, empty when the command takes none.
	Arg string
	// Summary is one line, written for someone deciding whether this is the
	// command they want.
	Summary string
	// Detail explains what evidence it uses and what it cannot tell you. A
	// command that overstates its reach is worse than one that is missing.
	Detail string
	Kind   Kind
	// NeedsIdle marks commands that cannot run while a task is working.
	NeedsIdle bool
}

// Registry is the full catalogue, in the order help presents each group.
var Registry = []Command{
	{
		Name: "/report", Kind: Understand, NeedsIdle: true,
		Summary: "architecture report: what Sensei knows about this repository",
		Detail:  "Counts invariants, failure modes, forbidden fixes, contracts, decisions and patterns for this repository's domain, and names the classes it holds nothing for. Absence means Sensei was never told, not that the repository is safe.",
	},
	{
		Name: "/focus", Arg: "<path>", Kind: Understand, NeedsIdle: true,
		Summary: "what governs one file, before you change it",
		Detail:  "Sensei's briefing and impact for a path: the invariants that protect it, the failure modes recorded against it, the fixes forbidden there, and the tests that must pass.",
	},
	{
		Name: "/why", Arg: "<invariant or failure id>", Kind: Understand, NeedsIdle: true,
		Summary: "read the rule behind a name Sensei gave you",
		Detail:  "Resolves one invariant, failure mode or forbidden fix and shows what it says, what it protects and which tests prove it. A rule you cannot read is a rule you cannot follow.",
	},
	{
		Name: "/debt", Kind: Understand, NeedsIdle: true,
		Summary: "where this repository is ungoverned",
		Detail:  "Ranks the surfaces carrying code that no invariant protects. This is the debt an agent accumulates fastest, because ungoverned code is where nothing can refuse a bad change.",
	},
	{
		Name: "/run", Arg: "<what to build>", Kind: Control, NeedsIdle: true,
		Summary: "carry the work out under governed execution",
		Detail:  "Enters governed mode for this one task, and authorizes it: candidate worktree cut from an exact base, bounded plan shown to you, worker, reviewer and a Sensei diff audit that a reviewer cannot accept over. You are asked again only when Sensei cannot certify a decision, or before anything is published. Esc stops a running task; at a decision it defers the question rather than answering it. Ordinary messages stay conversational and change nothing; this is how work actually gets done.",
	},
	{
		Name: "/audit", Kind: Control, NeedsIdle: true,
		Summary: "Sensei's evidence-based evaluation of the repository",
		Detail:  "Runs Sensei's own repository evaluation and corpus validation, and reports its verdict verbatim, including what that verdict does not verify.",
	},
	{
		Name: "/gate", Kind: Control, NeedsIdle: true,
		Summary: "check the current working diff against what governs it",
		Detail:  "Runs Sensei's enforcing gate over uncommitted changes. It reports blocking findings; a pass is not admission and not proof of correctness.",
	},
	{
		Name: "/refactor", Arg: "<path or description>", Kind: Control, NeedsIdle: true,
		Summary: "ask the architect for a governed refactor plan",
		Detail:  "The architect reads Sensei's evidence for the target and proposes a bounded plan, then carries it out under the same governed execution /run authorizes. Nothing is published without your decision.",
	},
	{
		Name: "/candidates", Kind: Understand, NeedsIdle: true,
		Summary: "what candidates exist, and why each one is still here",
		Detail:  "Lists every recorded candidate with its terminal disposition and the reason for it. A candidate reported as unresolved is one nobody decided anything about, which is different from one deliberately retained.",
	},
	{
		Name: "/learn", Arg: "<what broke>", Kind: Record, NeedsIdle: true,
		Summary: "record a scar so the next agent cannot repeat it",
		Detail:  "Queues a typed failure mode for Sensei's review queue. This is how a mistake stops being repeated instead of being fixed again.",
	},
	{
		Name: "/resume", Kind: Session, NeedsIdle: true,
		Summary: "continue a task that was interrupted after approval",
		Detail:  "Re-enters the implementation stage with the existing candidate and the reviewer's outstanding findings.",
	},
	{
		Name: "/login", Kind: Session, NeedsIdle: true,
		Summary: "connect a provider",
		Detail:  "Credentials stay with each provider's own client. Sensei Code stores no tokens.",
	},
	{
		Name: "/mcp", Kind: Session, NeedsIdle: true,
		Summary: "check or repair each agent's access to Sensei",
		Detail:  "A registered MCP server whose tools an agent cannot call is not access to Sensei, and is reported as partial rather than configured.",
	},
	{
		Name: "/setup", Kind: Session, NeedsIdle: true,
		Summary: "readiness report: what this machine and checkout still need",
		Detail: "Reports every readiness check — what is broken, what you would see when it bites, and the command that fixes it. It repairs nothing: " +
			"setup reaches a shared graph, a registry outside this repository and each agent's own MCP configuration, so changing any of it stays with `sensei-code setup --apply`.",
	},
	{
		Name: "/copy", Arg: "[all]", Kind: Session,
		Summary: "put the conversation on your clipboard",
		Detail:  "Copies the architect's last response, or the whole transcript with 'all', through the terminal's own clipboard escape — so it works over SSH and needs no xclip installed. ctrl+r does the same for the last response, ctrl+y copies the composer and ctrl+x cuts it.",
	},
	{
		Name: "/mouse", Kind: Session,
		Summary: "hand the mouse back to your terminal, or take it",
		Detail:  "While Sensei Code tracks the mouse the wheel scrolls the transcript, but your terminal will not let you drag to select text. This releases the mouse so selection and copy work normally; the keyboard keeps scrolling either way. Many terminals also let shift+drag select without releasing anything.",
	},
	{
		Name: "/clear", Kind: Session, NeedsIdle: true,
		Summary: "start a fresh conversation",
		Detail:  "Begins a new session. The previous record is kept, not deleted.",
	},
	{
		Name: "/help", Kind: Session,
		Summary: "list these commands",
		Detail:  "Generated from the same declaration the commands are dispatched from, so it cannot fall out of date.",
	},
}

// Lookup finds the command a line invokes, and the argument it carries. It
// returns ok=false for anything that is not a slash command, so ordinary task
// text is never mistaken for one.
func Lookup(line string) (Command, string, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "/") {
		return Command{}, "", false
	}
	name, arg, _ := strings.Cut(line, " ")
	for _, c := range Registry {
		if c.Name == name {
			return c, strings.TrimSpace(arg), true
		}
	}
	return Command{}, "", false
}

// IsUnknown reports a line that looks like a command but names none. Treating
// it as a task instead would hand "/reprot" to an architect as a feature
// request.
func IsUnknown(line string) bool {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "/") {
		return false
	}
	_, _, ok := Lookup(line)
	return !ok
}

// Suggest offers the closest command name for a mistyped one, so a typo costs a
// keystroke rather than a wrong task.
func Suggest(line string) string {
	line = strings.TrimSpace(line)
	name, _, _ := strings.Cut(line, " ")
	best, bestScore := "", 0
	for _, c := range Registry {
		score := commonPrefix(name, c.Name)
		if score > bestScore {
			best, bestScore = c.Name, score
		}
	}
	if bestScore < 2 {
		return ""
	}
	return best
}

func commonPrefix(a, b string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

// Groups returns the catalogue grouped for help, in workflow order.
func Groups() []struct {
	Kind     Kind
	Title    string
	Commands []Command
} {
	titles := []struct {
		Kind  Kind
		Title string
	}{
		{Understand, "Understand the codebase"},
		{Control, "Keep a change under control"},
		{Record, "Teach Sensei"},
		{Session, "Session"},
	}
	var out []struct {
		Kind     Kind
		Title    string
		Commands []Command
	}
	for _, t := range titles {
		var group []Command
		for _, c := range Registry {
			if c.Kind == t.Kind {
				group = append(group, c)
			}
		}
		sort.SliceStable(group, func(i, j int) bool { return group[i].Name < group[j].Name })
		if len(group) != 0 {
			out = append(out, struct {
				Kind     Kind
				Title    string
				Commands []Command
			}{t.Kind, t.Title, group})
		}
	}
	return out
}
