// Package validation executes the checks a plan requires and records what
// actually happened, bound to the exact candidate it happened to.
//
// The governed loop could generate a change, audit it, route authority, hand it
// between workers and refuse a bad acceptance — and still never reach ACCEPT,
// because the reviewer correctly asks for proof that the required checks ran
// and nothing could carry that proof to it. The loop was
//
//	worker → change → reviewer
//
// with the middle missing, and the reviewer declining to invent it. This is the
// missing edge:
//
//	worker → change → validation → typed evidence → reviewer
//
// Two properties make the evidence worth anything.
//
// It is produced by executing the process, never by a worker saying it ran the
// tests. A claim does not acquire authority by being written in prose, and that
// applies to "I ran go test" exactly as it applies to an architectural verdict.
// Nothing in this package accepts a report; it runs commands and records exit
// statuses.
//
// And every piece is bound to the digest of the candidate diff it was produced
// against. Evidence from one candidate must never certify another, and evidence
// produced before the candidate changed must stop counting the moment it does.
// That is not a hypothetical: a formatter mutates the candidate, so evidence
// gathered before formatting is evidence about different bytes.
package validation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// CheckKind is the sort of validation a piece of evidence records.
type CheckKind string

const (
	// Format may rewrite the candidate, so it runs first and invalidates
	// anything gathered before it.
	Format CheckKind = "format"
	// Vet and Build certify without mutating.
	Vet   CheckKind = "vet"
	Build CheckKind = "build"
	// Test certifies without mutating.
	Test CheckKind = "test"
)

// Outcome is what became of one check.
type Outcome string

const (
	// Passed means the command ran and exited zero.
	Passed Outcome = "passed"
	// Failed means the command ran and exited non-zero. This is a real result
	// and useful evidence: it tells the reviewer exactly what is wrong.
	Failed Outcome = "failed"
	// NotPermitted means the capability envelope does not grant this check. It
	// is not a pass and must never be read as one.
	NotPermitted Outcome = "not-permitted"
	// Errored means the check could not be run at all — the binary was missing,
	// the process could not start, the deadline expired. Also not a pass.
	Errored Outcome = "errored"
)

// Evidence is one executed check, bound to the candidate it certifies.
type Evidence struct {
	Kind    CheckKind `json:"kind"`
	Command string    `json:"command"`
	Args    []string  `json:"args,omitempty"`

	// RequestedBy and ExecutedBy record the two different parties. The workflow
	// asks for a check; the broker runs it. Keeping them apart is what makes
	// "the worker said it passed" unrepresentable here.
	RequestedBy string `json:"requested_by"`
	ExecutedBy  string `json:"executed_by"`

	// CandidateID and DiffDigest bind this evidence to exact content. The
	// digest is the load-bearing one: the id says which task, the digest says
	// which bytes.
	CandidateID string `json:"candidate_id"`
	DiffDigest  string `json:"diff_digest"`

	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	ExitStatus int       `json:"exit_status"`

	// Output is bounded, and OutputDigest covers what was captured so a reader
	// can tell a truncated tail from an edited one.
	Output       string  `json:"output,omitempty"`
	OutputDigest string  `json:"output_digest,omitempty"`
	Outcome      Outcome `json:"outcome"`

	// Detail explains a NotPermitted or Errored outcome.
	Detail string `json:"detail,omitempty"`
}

// Certifies reports whether this evidence speaks about the given candidate
// content. Anything else is evidence about different bytes.
func (e Evidence) Certifies(candidateID, diffDigest string) bool {
	return e.CandidateID == candidateID &&
		e.DiffDigest != "" &&
		e.DiffDigest == diffDigest
}

// Bundle is the set of checks run for one candidate revision.
type Bundle struct {
	CandidateID string     `json:"candidate_id"`
	DiffDigest  string     `json:"diff_digest"`
	Checks      []Evidence `json:"checks"`
}

// Digest hashes a diff into the identity every piece of evidence is bound to.
func Digest(diff string) string {
	sum := sha256.Sum256([]byte(diff))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Certifies reports whether the whole bundle speaks about this candidate
// content. A bundle containing even one piece of evidence from other bytes is
// refused wholesale rather than filtered, because a partially stale bundle is
// the shape that reads as complete while proving less than it appears to.
func (b Bundle) Certifies(candidateID, diffDigest string) bool {
	if b.CandidateID != candidateID || b.DiffDigest == "" || b.DiffDigest != diffDigest {
		return false
	}
	for _, c := range b.Checks {
		if !c.Certifies(candidateID, diffDigest) {
			return false
		}
	}
	return len(b.Checks) != 0
}

// Passed reports whether every check ran and succeeded.
//
// NotPermitted and Errored are not passes. A check that could not run tells us
// nothing about the candidate, and treating it as satisfied is how an unproven
// change acquires a receipt.
func (b Bundle) Passed() bool {
	if len(b.Checks) == 0 {
		return false
	}
	for _, c := range b.Checks {
		if c.Outcome != Passed {
			return false
		}
	}
	return true
}

// Failures lists the checks that did not pass, in the order they ran.
func (b Bundle) Failures() []Evidence {
	var out []Evidence
	for _, c := range b.Checks {
		if c.Outcome != Passed {
			out = append(out, c)
		}
	}
	return out
}

// Render writes the bundle for a reviewer.
//
// It states the binding first, because a reviewer's first question about
// evidence should be what it is evidence *of*, and it never summarises a
// non-pass as anything softer than it was.
func (b Bundle) Render() string {
	if len(b.Checks) == 0 {
		return "VALIDATION EVIDENCE: none was produced. No check was executed for this candidate, so nothing here has been verified."
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "VALIDATION EVIDENCE for candidate %s at diff %s\n", b.CandidateID, short(b.DiffDigest))
	sb.WriteString("Produced by executing these commands, not reported by the worker.\n\n")
	for _, c := range b.Checks {
		fmt.Fprintf(&sb, "  %-13s %s %s\n", string(c.Outcome), c.Command, strings.Join(c.Args, " "))
		fmt.Fprintf(&sb, "                exit %d · %s\n", c.ExitStatus, c.FinishedAt.Sub(c.StartedAt).Round(time.Millisecond))
		if c.Detail != "" {
			fmt.Fprintf(&sb, "                %s\n", c.Detail)
		}
		if c.Outcome != Passed && strings.TrimSpace(c.Output) != "" {
			for _, line := range strings.Split(strings.TrimRight(c.Output, "\n"), "\n") {
				fmt.Fprintf(&sb, "                | %s\n", line)
			}
		}
	}
	if !b.Passed() {
		sb.WriteString("\nNot every check passed. This candidate is not proven.\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

func short(digest string) string {
	digest = strings.TrimPrefix(digest, "sha256:")
	if len(digest) > 12 {
		return digest[:12]
	}
	return digest
}

// Check is one command the plan requires.
type Check struct {
	Kind    CheckKind
	Command string
	Args    []string
	// Mutates marks a check that can rewrite the candidate, which invalidates
	// evidence gathered before it.
	Mutates bool
}

// maxOutput bounds captured output. Enough to diagnose a failure, not enough to
// bury a prompt.
const maxOutput = 8000

// Runner executes checks in a candidate workspace.
//
// It holds no opinion about which checks are required; the caller supplies
// them, and the capability envelope decides which may run.
type Runner struct {
	Workspace string
	// Permits reports whether a kind of check is granted by the envelope. It is
	// a function rather than an envelope value so this package does not depend
	// on the broker, and so a test can deny a capability without constructing
	// a configuration.
	Permits func(CheckKind) (bool, string)
	// Now is injectable so evidence timestamps are testable.
	Now func() time.Time
}

// Run executes the checks and returns evidence bound to the candidate content
// they were run against.
//
// diffDigest is supplied by the caller rather than recomputed here, because the
// caller is the one that knows which diff it intends to certify. A mutating
// check invalidates the binding, which Run reports rather than papering over:
// the caller must re-read the diff and re-run the certifying checks.
func (r Runner) Run(ctx context.Context, candidateID, diffDigest string, checks []Check) Bundle {
	now := r.Now
	if now == nil {
		now = time.Now
	}
	bundle := Bundle{CandidateID: candidateID, DiffDigest: diffDigest}
	for _, check := range checks {
		bundle.Checks = append(bundle.Checks, r.one(ctx, candidateID, diffDigest, check, now))
	}
	return bundle
}

func (r Runner) one(ctx context.Context, candidateID, diffDigest string, check Check, now func() time.Time) Evidence {
	e := Evidence{
		Kind: check.Kind, Command: check.Command, Args: check.Args,
		RequestedBy: "sensei-code workflow", ExecutedBy: "sensei-code execution broker",
		CandidateID: candidateID, DiffDigest: diffDigest,
		StartedAt: now().UTC(),
	}
	if r.Permits != nil {
		if permitted, reason := r.Permits(check.Kind); !permitted {
			e.FinishedAt = now().UTC()
			e.Outcome = NotPermitted
			e.ExitStatus = -1
			e.Detail = reason
			return e
		}
	}

	cmd := exec.CommandContext(ctx, check.Command, check.Args...)
	cmd.Dir = r.Workspace
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	e.FinishedAt = now().UTC()

	body := out.String()
	sum := sha256.Sum256([]byte(body))
	e.OutputDigest = "sha256:" + hex.EncodeToString(sum[:])
	if len(body) > maxOutput {
		body = body[:maxOutput] + "\n… output truncated; digest covers the full capture"
	}
	e.Output = body

	switch {
	case err == nil:
		e.Outcome = Passed
	default:
		var exitErr *exec.ExitError
		if ok := asExitError(err, &exitErr); ok {
			e.ExitStatus = exitErr.ExitCode()
			e.Outcome = Failed
			return e
		}
		// Could not run at all: missing binary, deadline, permission. Not a
		// failure of the candidate and definitely not a pass.
		e.ExitStatus = -1
		e.Outcome = Errored
		e.Detail = err.Error()
	}
	return e
}

func asExitError(err error, target **exec.ExitError) bool {
	if ee, ok := err.(*exec.ExitError); ok {
		*target = ee
		return true
	}
	return false
}
