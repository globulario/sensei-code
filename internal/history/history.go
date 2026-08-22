// Package history reads the durable decisions and audits this repository keeps
// about itself.
//
// It exists because an architect that plans a change should know what has
// already been decided here and what a previous audit found, and neither was
// reachable. The graph holds rules; the checkout holds code; these two corpora
// hold the record of judgements — and they were sitting in the repository
// unread.
//
// Everything here is DERIVED from files on disk that can be re-read and
// disagreed with. Nothing is remembered. That is the whole basis on which this
// may enter an architectural brief: sensei-code's continuity layer deliberately
// stores the identity of a conversation rather than its architectural
// conclusions, and a brief that narrated recalled claims under a governed
// heading would undo that. A brief that quotes a committed decision file does
// not.
package history

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Decision is one recorded human-authority resolution.
type Decision struct {
	Task      string
	Condition string
	Chosen    string
	DecidedAt string
}

// Audit is one recorded audit document and the findings it names.
type Audit struct {
	Name     string
	Findings []string
}

// Record is what this repository has decided and found about itself.
type Record struct {
	Decisions []Decision
	Audits    []Audit
	// Unavailable records what could not be read and why, rather than leaving a
	// blank field that reads as "nothing to report". A corpus that failed to
	// parse and a corpus that is genuinely empty are different facts.
	Unavailable []string
	// Truncated names any corpus that had more entries than the limit allowed,
	// so a bounded brief never reads as a complete one.
	Truncated []string
}

// Gather reads both corpora, bounded.
func Gather(root string, limit int) Record {
	if limit <= 0 {
		limit = 6
	}
	var r Record
	r.Decisions, r.Unavailable, r.Truncated = readDecisions(root, limit, r.Unavailable, r.Truncated)
	r.Audits, r.Unavailable, r.Truncated = readAudits(root, limit, r.Unavailable, r.Truncated)
	return r
}

// proposalFields are the lines a `sensei propose` authority record carries. They
// are matched by exact prefix inside the description block rather than parsed as
// YAML, because this repository has no YAML dependency and adding one for a
// brief section is a larger change than the section is worth.
//
// The cost is stated rather than hidden: a file that does not carry these
// prefixes is reported unreadable and is NOT guessed at. Silently extracting
// three of four fields would put a decision in the brief with a missing half.
const (
	fieldCondition = "Certifiability condition:"
	fieldChosen    = "Chosen:"
	fieldTask      = "Task:"
	fieldDecided   = "Decided at:"
)

func readDecisions(root string, limit int, unavailable, truncated []string) ([]Decision, []string, []string) {
	dir := filepath.Join(root, "docs", "awareness", "candidates", "proposals")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			unavailable = append(unavailable, "authority decisions: "+err.Error())
		}
		return nil, unavailable, truncated
	}
	var out []Decision
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".yaml") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			unavailable = append(unavailable, "authority decision "+name+": "+err.Error())
			continue
		}
		d, ok := decisionFrom(string(data))
		if !ok {
			unavailable = append(unavailable, "authority decision "+name+": does not carry the expected decision fields")
			continue
		}
		if len(out) == limit {
			truncated = append(truncated, "authority decisions")
			break
		}
		out = append(out, d)
	}
	return out, unavailable, truncated
}

func decisionFrom(text string) (Decision, bool) {
	var d Decision
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, fieldCondition):
			d.Condition = strings.TrimSpace(strings.TrimPrefix(line, fieldCondition))
		case strings.HasPrefix(line, fieldChosen):
			d.Chosen = strings.TrimSpace(strings.TrimPrefix(line, fieldChosen))
		case strings.HasPrefix(line, fieldTask):
			d.Task = strings.TrimSpace(strings.TrimPrefix(line, fieldTask))
		case strings.HasPrefix(line, fieldDecided):
			d.DecidedAt = strings.TrimSpace(strings.TrimPrefix(line, fieldDecided))
		}
	}
	// All four or none. A decision missing its condition or its choice is not a
	// decision anybody can act on, and half of one in a brief is worse than its
	// absence.
	if d.Condition == "" || d.Chosen == "" || d.Task == "" || d.DecidedAt == "" {
		return Decision{}, false
	}
	return d, true
}

func readAudits(root string, limit int, unavailable, truncated []string) ([]Audit, []string, []string) {
	dir := filepath.Join(root, "docs", "audit")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			unavailable = append(unavailable, "audits: "+err.Error())
		}
		return nil, unavailable, truncated
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			names = append(names, e.Name())
		}
	}
	// Newest first: dated filenames sort lexically, so reversing is enough.
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	var out []Audit
	for _, name := range names {
		if len(out) == limit {
			truncated = append(truncated, "audits")
			break
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			unavailable = append(unavailable, "audit "+name+": "+err.Error())
			continue
		}
		findings, more := findingsIn(string(data), limit*2)
		if more {
			truncated = append(truncated, "findings in "+name)
		}
		out = append(out, Audit{Name: name, Findings: findings})
	}
	return out, unavailable, truncated
}

// findingsIn takes the finding headings, and reports whether it left any behind.
// The body is deliberately dropped: the brief needs to know what was found, and
// an architect that wants the argument can open the file.
//
// The second return is the point. Silently keeping the first N findings would
// hand the architect a partial list that reads as a complete one, which is the
// same defect this package labels audits to avoid -- a bounded record
// presenting itself as the whole picture.
func findingsIn(text string, limit int) (found []string, more bool) {
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, "### ") {
			continue
		}
		if len(found) == limit {
			return found, true
		}
		found = append(found, strings.TrimSpace(strings.TrimPrefix(line, "### ")))
	}
	return found, false
}

// Render is the brief's section. It states what these records are and are not:
// an audit document is a point-in-time finding, not a live defect list, and
// reading it as current state would have an architect plan around bugs that are
// already fixed.
func (r Record) Render() string {
	var b strings.Builder
	if len(r.Decisions) != 0 {
		b.WriteString("Human authority decisions already recorded for this repository.\n")
		b.WriteString("They bind: a condition answered once has been answered.\n")
		for _, d := range r.Decisions {
			b.WriteString("- " + d.DecidedAt + " (" + d.Task + "): on \"" + d.Condition + "\"\n")
			b.WriteString("  decided: " + d.Chosen + "\n")
		}
	}
	for _, a := range r.Audits {
		b.WriteString("\nAudit " + a.Name + " — findings AS RECORDED THEN, not a live defect list.\n")
		b.WriteString("Several may since have been fixed; check before planning around one.\n")
		for _, f := range a.Findings {
			b.WriteString("- " + f + "\n")
		}
	}
	for _, t := range r.Truncated {
		b.WriteString("\n(" + t + ": more exist than are shown here)\n")
	}
	for _, u := range r.Unavailable {
		b.WriteString("\nnot read — " + u + "\n")
	}
	return strings.TrimSpace(b.String())
}

// Empty reports a record that found nothing and failed at nothing.
func (r Record) Empty() bool {
	return len(r.Decisions) == 0 && len(r.Audits) == 0 && len(r.Unavailable) == 0
}
