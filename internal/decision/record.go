// Package decision records accepted architectural decisions into Sensei, so the
// reason a piece of work was authorized outlives the session that authorized it
// and is readable by every agent, not just the one that was in the room.
//
// Sensei owns the record. This package only invokes Sensei's own `propose`
// surface; it never writes awareness YAML itself and never promotes anything
// into the live graph. The entry is appended for a human to review and commit.
package decision

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Record is one accepted architectural decision, in the shape Sensei requires.
type Record struct {
	Title        string
	Rationale    string
	Context      string
	Consequences string
	SourceFiles  []string
	Invariants   []string
	Repo         string
	Domain       string
	RepoRoot     string
}

// ErrNotLinked reports a decision that Sensei would refuse. Sensei enforces
// contract-first: a decision must connect to something real. Rather than invent
// a link to satisfy the check, an unlinked decision is left unrecorded and the
// caller says so.
var ErrNotLinked = errors.New("decision is not linked to an invariant or a source file")

// ErrUnavailable reports that the sensei CLI is not installed. Decisions are
// the only Sensei surface with no awareness-mcp equivalent, so recording them
// requires the binary.
var ErrUnavailable = errors.New("the sensei CLI is not installed, so the decision was not recorded")

// Validate reports whether Sensei would accept this record.
func (r Record) Validate() error {
	if strings.TrimSpace(r.Title) == "" || strings.TrimSpace(r.Rationale) == "" {
		return errors.New("decision needs a title and a rationale")
	}
	if len(r.Invariants) == 0 && len(r.SourceFiles) == 0 {
		return ErrNotLinked
	}
	return nil
}

// Args builds the exact sensei argv. It is separated from execution so the
// command can be asserted in tests without running anything.
func (r Record) Args() []string {
	args := []string{
		"propose", "--kind", "decision",
		"--title", r.Title,
		"--description", r.Rationale,
		"--architectural-plane", "intended",
		// The entry is appended only. Rebuilding would republish the whole
		// graph and rotate its marker, staling every other repository that
		// shares the store; promotion stays a deliberate human step.
		"--no-rebuild",
	}
	if c := strings.TrimSpace(r.Context); c != "" {
		args = append(args, "--context", c)
	}
	if c := strings.TrimSpace(r.Consequences); c != "" {
		args = append(args, "--consequences", c)
	}
	for _, file := range r.SourceFiles {
		if f := strings.TrimSpace(file); f != "" {
			args = append(args, "--source-file", f)
		}
	}
	for _, inv := range r.Invariants {
		if i := strings.TrimSpace(inv); i != "" {
			args = append(args, "--related-invariant", i)
		}
	}
	if r.Repo != "" {
		args = append(args, "--repo", r.Repo)
	}
	if r.Domain != "" {
		args = append(args, "--domain", r.Domain)
	}
	args = append(args, "--target-repo", r.RepoRoot)
	return args
}

// Write appends the decision through Sensei's own propose surface.
func Write(ctx context.Context, r Record) error {
	if err := r.Validate(); err != nil {
		return err
	}
	path, err := exec.LookPath("sensei")
	if err != nil {
		return ErrUnavailable
	}
	cmd := exec.CommandContext(ctx, path, r.Args()...)
	cmd.Dir = r.RepoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("sensei refused the decision record: %s", strings.TrimSpace(string(out)))
	}
	return nil
}
