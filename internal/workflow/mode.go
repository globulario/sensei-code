package workflow

// Task mode.
//
// Assisted is what this product is most of the time: a person thinking out
// loud with an architect that can read the graph, in their own checkout, with
// no candidate and nothing to admit. Governed is the rigorous path — candidate
// worktree, bounded plan, reviewer, audit, admission — and it is entered
// deliberately, per task, by asking for it.
//
// Launching straight into governed mode was wrong in a specific way rather
// than merely heavy. It made every question into a change proposal: asking
// "why does this reload path exist" would produce a plan, a worktree and an
// approval prompt. That trains a person to stop asking questions, which is the
// opposite of what an architecture tool is for.
//
// Mode is derived from what a task actually is, never from a configuration
// flag. There is deliberately no config key that makes assisted tasks governed:
// a setting that silently upgraded a conversation into a governed candidate
// would reintroduce exactly the confusion this separation removes, and it would
// do it invisibly, at a distance, in a file nobody re-reads.

import "strings"

// Mode is the posture a single task runs in.
type Mode string

const (
	// Assisted is the default: conversational, in the developer's own
	// checkout, producing no candidate and no receipts.
	Assisted Mode = "assisted"
	// Governed is opt-in per task: candidate worktree, bounded plan, reviewer,
	// audit.
	Governed Mode = "governed"
)

// Provenance says why a task is in the mode it is in, so the UI can show the
// reason rather than just the label. A mode a person cannot account for is one
// they will assume is wrong at the worst moment.
type Provenance string

const (
	// DefaultEntry is an ordinary message: no one asked for governance.
	DefaultEntry Provenance = "default interactive entry"
	// RequestedByHuman is /run: the human explicitly asked for governed
	// execution of this task.
	RequestedByHuman Provenance = "requested by the human with /run"
	// ResumedGoverned is a governed task continuing after an interruption.
	ResumedGoverned Provenance = "resumed governed task"
)

// TaskMode is the mode of one task together with why.
type TaskMode struct {
	Mode       Mode
	Provenance Provenance
}

// Label is the short form for a status bar.
func (t TaskMode) Label() string {
	if t.Mode == "" {
		return string(Assisted)
	}
	return string(t.Mode)
}

// Describe is the long form: mode and the reason for it.
func (t TaskMode) Describe() string {
	mode := t.Label()
	if strings.TrimSpace(string(t.Provenance)) == "" {
		return mode
	}
	return mode + " · " + string(t.Provenance)
}

// IsGoverned reports whether candidate machinery applies to this task.
func (t TaskMode) IsGoverned() bool { return t.Mode == Governed }

// assistedMode and governedMode are the only two constructors, so a mode is
// always created with a stated provenance rather than defaulting to an
// unexplained one.
func assistedMode() TaskMode { return TaskMode{Mode: Assisted, Provenance: DefaultEntry} }

func governedMode(p Provenance) TaskMode { return TaskMode{Mode: Governed, Provenance: p} }
