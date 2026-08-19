// Package investigate is the read-only evidence a conversational turn may
// gather about the repository itself.
//
// The architect is supposed to be a strong investigator and a non-mutating one,
// and "non-mutating" was previously a sentence in a prompt. A prompt is not a
// boundary. This package is the boundary: it names the exact git subcommands it
// may run, refuses everything else by construction, and has no path that
// writes, commits, pushes, or starts a worker.
//
//	The list is a closed allowlist, not a denylist of dangerous verbs. A
//	denylist has to anticipate; an allowlist only has to be short. `git log`
//	with the wrong flags can still only read, whereas any denylist long enough
//	to be safe is long enough to have a gap in it.
package investigate

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// readOnly is every git subcommand this package may invoke. Adding to it is a
// deliberate act, which is the point.
var readOnly = map[string]bool{
	"status":     true,
	"log":        true,
	"diff":       true,
	"show":       true,
	"rev-parse":  true,
	"branch":     true,
	"describe":   true,
	"shortlog":   true,
	"blame":      true,
	"ls-files":   true,
	"cat-file":   true,
	"merge-base": true,
}

// ErrNotReadOnly reports a refusal to run something outside the allowlist.
type ErrNotReadOnly struct{ Subcommand string }

func (e *ErrNotReadOnly) Error() string {
	return fmt.Sprintf("git %s is not a read-only investigation surface; a conversational turn may look, never change", e.Subcommand)
}

// Allowed reports whether a subcommand may be run here. Exported so the
// boundary can be asserted directly rather than inferred from behaviour.
func Allowed(subcommand string) bool { return readOnly[strings.TrimSpace(subcommand)] }

// Surfaces lists the allowlist, for a person asking what this can see.
func Surfaces() []string {
	out := make([]string, 0, len(readOnly))
	for k := range readOnly {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Repository is the read-only git surface.
type Repository struct {
	Root    string
	Timeout time.Duration
}

// Run executes one allowlisted git subcommand and returns its output.
func (r Repository) Run(ctx context.Context, subcommand string, args ...string) (string, error) {
	if !Allowed(subcommand) {
		return "", &ErrNotReadOnly{Subcommand: subcommand}
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", r.Root, subcommand}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", subcommand, err, strings.TrimSpace(string(out)))
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// Evidence is what the repository itself says right now.
type Evidence struct {
	Head     string
	Branch   string
	Dirty    bool
	Recent   string
	Touching map[string]string
	// Unavailable records what could not be read and why, rather than leaving a
	// blank field that reads as "nothing to report".
	Unavailable []string
}

// Gather collects the standing repository evidence, plus the recent history of
// any paths the turn is actually about.
//
// paths comes from the same selection the Sensei retrieval uses, so the git
// evidence is about the same subject as the graph evidence rather than being a
// second, differently-scoped dump.
func (r Repository) Gather(ctx context.Context, paths []string, limit int) Evidence {
	if limit <= 0 {
		limit = 5
	}
	ev := Evidence{Touching: map[string]string{}}
	note := func(what string, err error) { ev.Unavailable = append(ev.Unavailable, what+": "+err.Error()) }

	if head, err := r.Run(ctx, "rev-parse", "--short", "HEAD"); err == nil {
		ev.Head = head
	} else {
		note("head", err)
	}
	if branch, err := r.Run(ctx, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		ev.Branch = branch
	} else {
		note("branch", err)
	}
	if status, err := r.Run(ctx, "status", "--porcelain"); err == nil {
		ev.Dirty = strings.TrimSpace(status) != ""
	} else {
		note("status", err)
	}
	if recent, err := r.Run(ctx, "log", "--oneline", fmt.Sprintf("-%d", limit)); err == nil {
		ev.Recent = recent
	} else {
		note("recent history", err)
	}
	for _, p := range paths {
		if strings.TrimSpace(p) == "" {
			continue
		}
		if out, err := r.Run(ctx, "log", "--oneline", fmt.Sprintf("-%d", limit), "--", p); err == nil && strings.TrimSpace(out) != "" {
			ev.Touching[p] = out
		}
	}
	return ev
}

// Render states what the repository evidence is, including what it could not read.
func (e Evidence) Render() string {
	var b strings.Builder
	dirty := "clean"
	if e.Dirty {
		dirty = "uncommitted changes present"
	}
	fmt.Fprintf(&b, "head %s on %s (%s)", orUnknown(e.Head), orUnknown(e.Branch), dirty)
	if strings.TrimSpace(e.Recent) != "" {
		b.WriteString("\n\nrecent commits:\n" + indent(e.Recent))
	}
	keys := make([]string, 0, len(e.Touching))
	for k := range e.Touching {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "\n\ncommits touching %s:\n%s", k, indent(e.Touching[k]))
	}
	if len(e.Unavailable) != 0 {
		fmt.Fprintf(&b, "\n\ncould not be read: %s", strings.Join(e.Unavailable, "; "))
	}
	return b.String()
}

func indent(s string) string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = "  " + lines[i]
	}
	return strings.Join(lines, "\n")
}

func orUnknown(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(unknown)"
	}
	return s
}
