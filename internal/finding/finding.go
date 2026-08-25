// Package finding carries what an observation established into the task that
// may repair it.
//
// The dangerous shortcut this exists to prevent is letting the observation lane
// mutate because it found something true. It does not. A finding is EVIDENCE
// and PROVENANCE for a later governed change; it is never an admission, never a
// grant, and never a reason for the next task to skip anything.
//
// The law for this slice:
//
//	Observation may discover work. It may not perform the work. A repair must
//	begin as a new governed change whose provenance points back to the
//	observation.
package finding

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

// Provenance is where a finding's claim came from, as the observer reported it.
//
// Deliberately a small closed set with an explicit unknown, and Unrecognised is
// the zero value so an unset or unfamiliar label is never mistaken for
// evidence. That ordering is the whole point of the type: this package exists
// because a neighbouring classifier read only one bad value and let every other
// unrecognised one through.
type Provenance int

const (
	// Unrecognised is anything this classifier has no reading for, including
	// the empty string. Zero value on purpose.
	Unrecognised Provenance = iota
	// Repository: the observer opened a file and read what it describes.
	Repository
	// Graph: a Sensei tool returned it.
	Graph
	// Inference: the observer reasoned to it and did not observe it.
	Inference
)

func (p Provenance) String() string {
	switch p {
	case Repository:
		return "repository"
	case Graph:
		return "graph"
	case Inference:
		return "inference"
	default:
		return "unrecognised"
	}
}

// EvidenceBearing reports whether this provenance rests on something the next
// task could independently re-check.
//
// Inference and Unrecognised are both false, for different reasons that matter:
// inference is honestly labelled reasoning, and unrecognised is a label nobody
// has read. Neither is evidence, and treating the second as weaker-but-usable
// would reintroduce exactly the fail-open shape this package was built after.
func (p Provenance) EvidenceBearing() bool { return p == Repository || p == Graph }

// Classify reads a source label. Unknown values are Unrecognised, never a
// nearest match.
func Classify(source string) Provenance {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "repository":
		return Repository
	case "graph":
		return Graph
	case "inference":
		return Inference
	default:
		return Unrecognised
	}
}

// Finding is one thing an observation established, with what established it.
type Finding struct {
	// ID is stable across runs for the same claim about the same place at the
	// same revision, so a repeated observation does not manufacture new work.
	ID string
	// ObservationTask and World say which run saw this, and in what state of
	// the repository. A repair re-checks the CURRENT world; these say what was
	// true then, not what must be true now.
	ObservationTask string
	World           string
	// Objective is the human's original request, carried so a repair can say
	// what it descends from without re-asking.
	Objective string

	Statement string
	About     string
	Files     []string
	Source    Provenance
	// ReadFiles are the files the observation actually opened.
	//
	// Carried because Source is a label the model typed. The defect this
	// package is named after -- an unchecked value read as evidence -- applies
	// to the finding itself: an architect can write source "repository" about a
	// file it never read, and that word alone would open autonomous repair
	// work.
	//
	// Checking this does not make the claim true. It establishes the weaker
	// thing that CAN be checked here: the finding is about something the
	// observation looked at. Verifying the statement is the repair task's job,
	// and the repair re-checks the current world before changing anything.
	ReadFiles []string
}

// Eligible reports whether this finding may become repair WORK.
//
// Evidence-bearing provenance only. An inference may be reported to a human and
// may not silently become a change objective — the observer reasoning its way
// to a conclusion is not a reason to edit the repository, and this is the point
// where that distinction has to hold, because everything downstream treats a
// task as a task.
//
// It must also name where it is, or the repair has nothing to re-check, and
// every file it names must be one the observation actually opened. A claimed
// provenance is not evidence on its own -- the lesson of the very defect this
// bridge first carried -- so the label must at least agree with what the run
// did.
func (f Finding) Eligible() bool {
	if !f.Source.EvidenceBearing() || strings.TrimSpace(f.Statement) == "" || len(f.Files) == 0 {
		return false
	}
	return f.AboutFilesWereRead()
}

// AboutFilesWereRead reports whether every file this finding is about was
// opened by the observation that produced it.
func (f Finding) AboutFilesWereRead() bool {
	if len(f.ReadFiles) == 0 {
		// Nothing establishes what was read, so nothing establishes that this
		// finding is about it. Fail closed, like every other unknown here.
		return false
	}
	read := make(map[string]bool, len(f.ReadFiles))
	for _, r := range f.ReadFiles {
		read[strings.TrimSpace(r)] = true
	}
	for _, name := range f.Files {
		if !read[name] {
			return false
		}
	}
	return true
}

// New builds a finding and computes its identity.
func New(observationTask, world, objective, statement, about string, files, readFiles []string, source string) Finding {
	f := Finding{
		ObservationTask: observationTask, World: world, Objective: objective,
		Statement: strings.TrimSpace(statement), About: strings.TrimSpace(about),
		Files: normalise(files), ReadFiles: normalise(readFiles), Source: Classify(source),
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{
		f.World, f.Statement, f.About, strings.Join(f.Files, ","),
	}, "\x00")))
	f.ID = "finding-" + hex.EncodeToString(sum[:])[:16]
	return f
}

func normalise(files []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range files {
		f = strings.TrimSpace(f)
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// RepairObjective is the task text a repair begins from.
//
// It states the defect and where it was seen, and deliberately does NOT state
// the fix. The observation found a problem; the repair has to work out the
// answer through the ordinary path, or the experiment proves nothing.
//
// It also tells the repair to re-check the current world. A finding is a claim
// about the revision that was inspected, and the repository may have moved --
// a repair that cannot still see the defect must be free to refuse rather than
// change something to justify its own existence.
func (f Finding) RepairObjective() string {
	var b strings.Builder
	b.WriteString("Repair a defect an earlier read-only audit of this repository observed.\n\n")
	b.WriteString("Observed defect: " + f.Statement + "\n")
	if f.About != "" {
		b.WriteString("Where: " + f.About + "\n")
	}
	b.WriteString("Files named by the observation: " + strings.Join(f.Files, ", ") + "\n")
	b.WriteString("Observed at revision: " + f.World + "\n")
	b.WriteString("Source of the observation: " + f.Source.String() + "\n\n")
	b.WriteString("This finding is EVIDENCE, not authority. It grants nothing. Before changing anything:\n")
	b.WriteString("  - re-check the CURRENT repository yourself and confirm the defect is still present;\n")
	b.WriteString("  - if it is absent, already fixed, or you cannot establish it, say so and change nothing;\n")
	b.WriteString("  - decide the correct fix yourself. No fix has been supplied to you.\n\n")
	b.WriteString("Keep the change minimal and do not alter unrelated behaviour. ")
	b.WriteString("Add whatever tests establish that the defect cannot return.")
	return b.String()
}
