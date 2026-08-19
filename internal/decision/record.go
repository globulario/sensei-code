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

// Owner is who authorized the work a decision records. It is a closed
// vocabulary of two, because there are exactly two authorities in this system:
// the architect deciding inside a region Sensei certified, and the human
// answering a question Sensei could not settle.
type Owner string

const (
	// Architectural means the authority router granted the plan and no human
	// was asked. The human's contribution was authorizing the task itself.
	Architectural Owner = "architectural"
	// Human means a Level-3 condition reached a person and they answered it.
	Human Owner = "human"
)

// Authority is the provenance of a decision: who decided, on what basis, and
// what the human actually authorized.
//
// It is a type rather than a sentence because the sentence was wrong. The
// engine used to send the fixed string "Accepted by the human in an interactive
// Sensei Code session" with every decision, which was true while a rendezvous
// stood between the plan and the worker and became false the moment Level-2
// work began flowing without one. A record that overstates who approved
// something is worse than no record: it makes a human look like they personally
// signed off on every implementation detail, and a later agent reads that as
// precedent.
type Authority struct {
	Owner Owner
	// CertifiedBy identifies the graph that certified the region, so a reader
	// can tell which body of knowledge the grant rested on.
	CertifiedBy string
	// DecidedBy names the agent that decided, when the owner is architectural.
	DecidedBy string
	// HumanGrant is what the human actually authorized. For an architectural
	// decision that is the task itself, not the plan.
	HumanGrant string
	// Condition is the certifiability condition a human was asked about, and
	// Resolution is the option they chose. Both are empty unless Owner is
	// Human.
	Condition  string
	Resolution string
}

// Describe renders the provenance as the context Sensei stores. Keys are fixed
// and machine-readable, because the next reader of this line is as likely to be
// an agent as a person.
func (a Authority) Describe() string {
	parts := []string{"decision_authority: " + string(a.Owner)}
	add := func(key, value string) {
		if v := strings.TrimSpace(value); v != "" {
			parts = append(parts, key+": "+v)
		}
	}
	add("certified_by", a.CertifiedBy)
	add("decided_by", a.DecidedBy)
	add("human_authorization", a.HumanGrant)
	add("condition", a.Condition)
	add("resolution", a.Resolution)
	return strings.Join(parts, "; ")
}

// Record is one accepted architectural decision, in the shape Sensei requires.
type Record struct {
	Title        string
	Rationale    string
	Authority    Authority
	Consequences string
	SourceFiles  []string
	Invariants   []string
	Failures     []string
	Repo         string
	Domain       string
	RepoRoot     string
}

// ErrNotLinked reports a decision that Sensei would refuse. Sensei enforces
// contract-first: a decision must connect to something real. Rather than invent
// a link to satisfy the check, an unlinked decision is left unrecorded and the
// caller says so.
var ErrNotLinked = errors.New("no governing invariant to link the decision to, so it was not recorded; govern these files first")

// ErrUnavailable reports that the sensei CLI is not installed. Decisions are
// the only Sensei surface with no awareness-mcp equivalent, so recording them
// requires the binary.
var ErrUnavailable = errors.New("the sensei CLI is not installed, so the decision was not recorded")

// Validate reports whether Sensei would accept this record.
func (r Record) Validate() error {
	if strings.TrimSpace(r.Title) == "" || strings.TrimSpace(r.Rationale) == "" {
		return errors.New("decision needs a title and a rationale")
	}
	// Sensei's contract-first rule for decisions accepts a related invariant or
	// failure mode; source files alone do not satisfy it. Matching that rule
	// here means an unlinkable decision is reported as such instead of being
	// sent to be refused, and the message says what would fix it.
	if len(r.Invariants) == 0 && len(r.Failures) == 0 {
		return ErrNotLinked
	}
	// An unattributed decision is not a smaller record, it is a misleading one:
	// every reader has to guess who authorized the work, and the safe guess is
	// the wrong one.
	switch r.Authority.Owner {
	case Architectural, Human:
	default:
		return fmt.Errorf("decision needs an authority owner (%s or %s), got %q",
			Architectural, Human, r.Authority.Owner)
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
	if c := strings.TrimSpace(r.Authority.Describe()); c != "" {
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
		if i := normaliseID(inv); i != "" {
			args = append(args, "--related-invariant", i)
		}
	}
	for _, failure := range r.Failures {
		if f := normaliseID(failure); f != "" {
			args = append(args, "--related-failure", f)
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
// normaliseID strips the class prefix Sensei's graph queries return. An id read
// back as "invariant:x" and written as-is produces a decision that references
// nothing, which validation reports as a dangling reference: a link that looks
// like provenance and resolves to nowhere.
func normaliseID(id string) string {
	id = strings.TrimSpace(id)
	for _, prefix := range []string{"invariant:", "failure_mode:", "failure:", "forbidden_fix:"} {
		id = strings.TrimPrefix(id, prefix)
	}
	return strings.TrimSpace(id)
}

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
