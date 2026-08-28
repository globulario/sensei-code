// Package report summarises what a candidate actually changed, for an
// architect rather than for a reviewer of diffs.
//
// Every number here is counted from the candidate diff or quoted from Sensei.
// Nothing is a model's description of its own work: an agent summarising what
// it believes it did is the least reliable witness available, and this report
// exists precisely so the architect does not have to take that summary on
// trust. What the report cannot establish is stated as such.
package report

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// Status is how a file was affected by the candidate.
type Status string

const (
	Added    Status = "added"
	Modified Status = "modified"
	Deleted  Status = "deleted"
	Renamed  Status = "renamed"
)

// FileChange is one file the candidate touched.
type FileChange struct {
	Path   string `json:"path"`
	Status Status `json:"status"`
	// OldPath is the path a renamed file had before the change.
	OldPath string `json:"old_path,omitempty"`
}

// Change is the architectural shape of a candidate.
type Change struct {
	Files []FileChange `json:"files"`
	// Awareness counts governance entries the candidate added, keyed by the
	// kind Sensei calls them.
	Awareness map[string]int `json:"awareness,omitempty"`
	// Audit is Sensei's own verdict on the diff, quoted.
	Audit string `json:"audit,omitempty"`
	// Risk is the preflight risk class for the files actually touched.
	Risk string `json:"risk,omitempty"`
	// Governing lists invariants Sensei reports over the touched files.
	Governing []string `json:"governing,omitempty"`
}

// awarenessKinds maps an awareness source file to the entry kind it holds.
var awarenessKinds = map[string]string{
	"invariants.yaml":        "invariants",
	"failure_modes.yaml":     "failure modes",
	"forbidden_fixes.yaml":   "forbidden fixes",
	"incident_patterns.yaml": "incident patterns",
	"decisions.yaml":         "decisions",
	"contracts.yaml":         "contracts",
	"high_risk_files.yaml":   "high-risk file entries",
	"activation_rules.yaml":  "activation rules",
	// The meta-principle pack is installed by `sensei init`, not authored for
	// this change. Counting it as governance the candidate added would overstate
	// the work by more than a hundred entries.
	"meta_principles.yaml": "meta principles (installed pack, not authored here)",
}

// FromDiff derives the change shape from a unified diff. It reads the diff
// itself rather than asking the worker what it did.
func FromDiff(diff string) Change {
	change := Change{Awareness: map[string]int{}}
	var current, old string
	status := Modified
	flush := func() {
		if current == "" {
			return
		}
		change.Files = append(change.Files, FileChange{Path: current, Status: status, OldPath: old})
	}
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			flush()
			current, old = diffPath(line), ""
			status = Modified
		case strings.HasPrefix(line, "new file mode"):
			status = Added
		case strings.HasPrefix(line, "deleted file mode"):
			status = Deleted
		case strings.HasPrefix(line, "rename from "):
			old = strings.TrimPrefix(line, "rename from ")
			status = Renamed
		case strings.HasPrefix(line, "rename to "):
			// The rename lines carry both paths whole, however many spaces
			// they hold; they outrank the header's guess.
			current = strings.TrimPrefix(line, "rename to ")
		case strings.HasPrefix(line, "+++ b/") && current == "":
			current = strings.TrimSuffix(strings.TrimPrefix(line, "+++ b/"), "\t")
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			if kind := awarenessKind(current); kind != "" && isEntryStart(line) {
				change.Awareness[kind]++
			}
		}
	}
	flush()
	return change
}

// diffPath takes the b-side path from a `diff --git a/P b/P` header.
//
// It used to split the line on whitespace, which lost every path containing
// a space -- and a granted test file at such a path then read as untouched
// and skipped inspection (sensei-code#101 review). Git does not quote spaces
// in this header, so the line is parsed by its shape instead: for an
// unrenamed file both halves are the same path, and the one split that makes
// them equal is the answer. A header whose halves differ (a rename) yields
// "" here and is completed by the `rename to` line that follows it.
func diffPath(line string) string {
	rest := strings.TrimPrefix(line, "diff --git ")
	if !strings.HasPrefix(rest, "a/") {
		return ""
	}
	for i := len(rest) - 1; i > 0; i-- {
		if !strings.HasPrefix(rest[i:], " b/") {
			continue
		}
		left, right := strings.TrimPrefix(rest[:i], "a/"), strings.TrimPrefix(rest[i+1:], "b/")
		if left == right {
			return right
		}
	}
	return ""
}

func awarenessKind(file string) string {
	if !strings.Contains(file, "docs/awareness/") {
		return ""
	}
	if kind, ok := awarenessKinds[path.Base(file)]; ok {
		return kind
	}
	return "awareness entries"
}

// isEntryStart matches the start of a YAML list entry carrying an id, which is
// how every Sensei awareness entry begins.
func isEntryStart(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(line, "+")), "- id:")
}

// Counts returns how many files were added, modified and deleted.
func (c Change) Counts() (added, modified, deleted int) {
	for _, f := range c.Files {
		switch f.Status {
		case Added:
			added++
		case Deleted:
			deleted++
		default:
			modified++
		}
	}
	return
}

// GoFiles counts non-test Go files, and the test files separately, because
// "five new files, none of them tests" is the kind of thing an architect wants
// to notice without reading the diff.
func (c Change) GoFiles() (source, tests int) {
	for _, f := range c.Files {
		if !strings.HasSuffix(f.Path, ".go") {
			continue
		}
		if strings.HasSuffix(f.Path, "_test.go") {
			tests++
			continue
		}
		source++
	}
	return
}

// Render writes the report an architect reads. It always ends with what the
// report does not establish, because a change summary that reads as a
// completion claim is exactly the false green Sensei exists to prevent.
func (c Change) Render(task string) string {
	var b strings.Builder
	b.WriteString("Architectural change report\n")
	if t := strings.TrimSpace(task); t != "" {
		b.WriteString("  for: " + t + "\n")
	}

	added, modified, deleted := c.Counts()
	b.WriteString(fmt.Sprintf("\n  files: %d added, %d modified, %d deleted\n", added, modified, deleted))
	if source, tests := c.GoFiles(); source+tests > 0 {
		b.WriteString(fmt.Sprintf("  go: %d source, %d test\n", source, tests))
	}

	if len(c.Awareness) != 0 {
		b.WriteString("\n  governance added:\n")
		for _, kind := range sortedKeys(c.Awareness) {
			b.WriteString(fmt.Sprintf("    %d %s\n", c.Awareness[kind], kind))
		}
	}

	if len(c.Governing) != 0 {
		b.WriteString("\n  governed by:\n")
		for _, id := range c.Governing {
			b.WriteString("    " + id + "\n")
		}
	}

	if r := strings.TrimSpace(c.Risk); r != "" {
		b.WriteString("\n  Sensei risk class: " + r + "\n")
	}
	if a := strings.TrimSpace(c.Audit); a != "" {
		b.WriteString("\n  Sensei diff audit:\n    " + strings.ReplaceAll(a, "\n", "\n    ") + "\n")
	}

	b.WriteString("\n  not established by this report:\n")
	for _, line := range c.NotEstablished() {
		b.WriteString("    - " + line + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// NotEstablished names the claims this report is not entitled to make. It is
// not boilerplate: each line corresponds to a step that genuinely has not run.
func (c Change) NotEstablished() []string {
	out := []string{
		"admission: not requested, so this candidate is not admitted",
		"correctness: reviewer acceptance is not proof, and passing tests are not proof",
	}
	if strings.TrimSpace(c.Audit) == "" {
		out = append(out, "Sensei did not audit this diff")
	}
	if len(c.Governing) == 0 {
		out = append(out, "no invariant was found governing the touched files, which may mean the area is ungoverned rather than safe")
	}
	if _, tests := c.GoFiles(); tests == 0 {
		if source, _ := c.GoFiles(); source > 0 {
			out = append(out, "no test file was added or changed alongside the Go sources")
		}
	}
	return out
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
