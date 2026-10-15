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
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
	// Failed means the command ran, exited non-zero, and the candidate is
	// responsible: the same check passes against the base it was cut from.
	Failed Outcome = "candidate-failure"
	// Infrastructure means the command exited non-zero for a reason the
	// candidate cannot affect, established by the same check failing against a
	// clean checkout of the base.
	//
	// The distinction is not bookkeeping. A mandatory check must test a
	// property the candidate can influence, and one that cannot manufactures an
	// impossible revision loop: the reviewer asks for a fix, the worker cannot
	// produce one, and every governance component behaves perfectly while the
	// system goes nowhere. A real run spent its cycles being told to repair
	// "error obtaining VCS status", which no edit to the candidate could reach.
	Infrastructure Outcome = "infrastructure-failure"
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

	// Detail explains a NotPermitted, Errored or Infrastructure outcome.
	Detail string `json:"detail,omitempty"`
	// Attribution records how a non-zero exit was assigned: "candidate" when
	// the base passed, "pre-existing" when the base failed the same way, and
	// "unattributed" when no baseline was available to compare against.
	// Unattributed is reported rather than guessed, because guessing in either
	// direction is worse than saying which question was not asked.
	Attribution string `json:"attribution,omitempty"`
}

// CandidateFailures lists only the failures the worker can actually act on.
//
// This is what a revision instruction may be built from. Handing a worker an
// infrastructure failure produces a request it cannot satisfy.
func (b Bundle) CandidateFailures() []Evidence {
	var out []Evidence
	for _, c := range b.Checks {
		if c.Outcome == Failed {
			out = append(out, c)
		}
	}
	return out
}

// Unactionable lists results that block acceptance but are not the candidate's
// fault, so a caller can escalate them instead of demanding a revision.
func (b Bundle) Unactionable() []Evidence {
	var out []Evidence
	for _, c := range b.Checks {
		switch c.Outcome {
		case Infrastructure, NotPermitted, Errored:
			out = append(out, c)
		}
	}
	return out
}

// Certifies reports whether this evidence speaks about the given candidate
// content. Anything else is evidence about different bytes. A worker's own
// tool-time check certifies nothing, even about the bytes it ran against.
func (e Evidence) Certifies(candidateID, diffDigest string) bool {
	return e.RequestedBy != ToolTimeRequester &&
		e.CandidateID == candidateID &&
		e.DiffDigest != "" &&
		e.DiffDigest == diffDigest
}

// Bundle is the set of checks run for one candidate revision.
type Bundle struct {
	CandidateID string     `json:"candidate_id"`
	DiffDigest  string     `json:"diff_digest"`
	Checks      []Evidence `json:"checks"`
}

// ToolTimeRequester is the requesting party of every check an implementer
// asked for through its execution tools (N3). Such evidence is advisory: it
// never certifies, whatever it says, because the engine validates the
// captured candidate itself. The marker is a field every ValidationRun record
// already carries, so it survives the record exactly.
const ToolTimeRequester = "implementer via sensei_exec"

// ToolTime reports whether any check in the bundle was a worker's own.
func (b Bundle) ToolTime() bool {
	for _, c := range b.Checks {
		if c.RequestedBy == ToolTimeRequester {
			return true
		}
	}
	return false
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
	sb.WriteString("Produced by executing these commands, not reported by the worker.\n")
	if b.ToolTime() {
		sb.WriteString("ADVISORY: requested by the implementer through its execution tools; it certifies nothing.\n")
	}
	sb.WriteString("\n")
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
	if u := b.Unactionable(); len(u) != 0 {
		sb.WriteString("\nSome results are NOT the candidate's fault and cannot be fixed by editing it:\n")
		for _, c := range u {
			fmt.Fprintf(&sb, "  %s %s — %s\n", string(c.Outcome), c.Command, c.Detail)
		}
		sb.WriteString("Do not ask the worker to revise code for these. They are an environment or\n")
		sb.WriteString("capability problem and need a human or a configuration change.\n")
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
	// FailIfOutput treats any output as failure even when the command exits
	// zero. Some verifiers report by printing rather than by exit status --
	// `gofmt -l` lists unformatted files and exits zero either way -- and
	// reading only the exit code would record a silent pass.
	FailIfOutput bool
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
	// Baseline returns a clean checkout of the candidate's base, used only to
	// attribute a failure. It is called at most once, lazily, and only when a
	// check has already failed -- attribution costs a second execution and is
	// worth paying only for a result that is going to be acted on.
	//
	// When nil, failures are reported as unattributed rather than assumed to be
	// the candidate's fault.
	Baseline func() (string, error)
	// Confine, when set, is the pinned confinement every command must run
	// under. It is never optional once set: a wrapper that is missing, does
	// not match its pinned digest, or reports its own failure yields Errored
	// with SandboxUnavailable, and the check is never run on the host instead.
	// Nil is the unconfined broker every existing caller uses.
	Confine *Confinement
	// Refusal, when set, refuses every check with SandboxUnavailable and
	// this reason before anything runs. It is how a caller that requires
	// confinement it cannot establish says so without a host fallback.
	Refusal string
	// RequestedBy overrides the requesting party recorded on evidence. Empty
	// is the workflow.
	RequestedBy string
}

// SandboxUnavailable prefixes the Detail of a check refused because its
// confinement could not be established. Nothing ran.
const SandboxUnavailable = "REFUSED_SANDBOX_UNAVAILABLE"

// Confinement is one pinned execution profile: a wrapper that runs
// `Command Args... -- check.Command check.Args...` inside a confined
// executor.
//
// The digest is the pin. A wrapper path alone names whatever happens to be
// installed there, and a confinement nobody can identify is not one.
type Confinement struct {
	Profile string
	Command string
	Args    []string
	// SHA256 is the hex digest the wrapper binary must have.
	SHA256 string
	// FailureExit is the exit status by which the wrapper reports that IT
	// failed rather than the confined command. Zero means 125, the
	// convention env, podman and docker use for the same distinction.
	// Under RequireStatus it is not consulted.
	FailureExit int
	// RequireStatus selects the launcher status protocol (StatusProtocol):
	// the wrapper's own record on a status descriptor, never an exit code,
	// says whether the confined command ran and how it exited. A wrapper that
	// writes no valid record is refused, so one that does not speak the
	// protocol fails closed. Validation of a candidate always sets it; false
	// is the N3 tool-time exit-code convention.
	RequireStatus bool
}

// StatusProtocol is the launcher status protocol a RequireStatus wrapper
// speaks. The wrapper is started with:
//
//   - fd 3: the verified wrapper itself (what the kernel executes);
//   - fd 4: the status descriptor, write-only;
//   - fd 5: the nonce descriptor, one line holding this execution's nonce;
//   - an environment of exactly PATH and the SENSEI_CONFINE_* variables
//     (statusEnv), which name the protocol, the workspace, its role
//     (candidate or baseline), whether it is writable, and the check kind.
//
// The wrapper is the supervisor of the confined command, and the two
// capabilities are its alone: it reads the nonce from fd 5, keeps the status
// descriptor fd 4 for itself, and starts the confined command with fds 3, 4
// and 5 closed, so the command can neither learn the nonce nor write a status.
// After the command ends the supervisor writes exactly one line to fd 4:
//
//	sensei-confine-status/1 <nonce> ran <exit 0-255>
//	sensei-confine-status/1 <nonce> refused <reason>
//
// and, for "ran", exits with that same status. Anything else -- no record, two
// records, a wrong nonce, a status the wrapper's own exit contradicts, a
// descriptor still held open after the exit -- is a refusal: whatever ran is
// not evidence about the candidate. A status that can no longer become a
// record ends the execution at the byte that made it so: the bound reached
// without the newline that ends every record, or anything after that
// newline. The broker terminates the wrapper at that byte and refuses, so a
// launcher cannot hold validation by writing without end, or by writing what
// can never be read and then waiting. A record still being written, short of
// the bound, is waited for however slow the wrapper is; a record written, or
// a status stream ended, is the end of the command, and the wrapper then has
// the grace to exit, or the invocation is ended and its status is not
// evidence.
//
// The nonce binds a record to this execution; it does not by itself
// authenticate the writer in a process view the confined command shares. A
// command in the supervisor's pid namespace with the same uid could reach the
// supervisor's open descriptors through /proc. It still cannot forge a pass
// (it does not know the nonce, and a second record refuses), but it could turn
// its own failure into a refusal. Pid and descriptor isolation of the confined
// command from its supervisor is therefore a required property of the real
// execution profile, as are the interpreter the wrapper's shebang names and
// the tools its PATH resolves: the pinned digest proves the wrapper's bytes,
// not what those bytes run.
const StatusProtocol = "sensei-confine-status/1"

// Describe names the profile as evidence records it.
func (c Confinement) Describe() string {
	d := fmt.Sprintf("profile %s via %s (sha256:%s)", c.Profile, c.Command, strings.TrimPrefix(strings.ToLower(strings.TrimSpace(c.SHA256)), "sha256:"))
	if c.RequireStatus {
		d += " with launcher status " + StatusProtocol
	}
	return d
}

func (c Confinement) failureExit() int {
	if c.FailureExit != 0 {
		return c.FailureExit
	}
	return 125
}

// open establishes that the wrapper is the pinned one and returns it open: an
// absolute path, opened once without following a symlink, to a regular file
// whose bytes, read through that same descriptor, hash to the pinned digest.
//
// The descriptor, not the path, is what runs (commandFor), so a path swapped
// after hashing cannot change what executes. The interpreter a #! wrapper
// names is not pinned by this digest.
func (c Confinement) open() (*os.File, error) {
	pin := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(c.SHA256)), "sha256:")
	switch {
	case strings.TrimSpace(c.Profile) == "":
		return nil, fmt.Errorf("no confinement profile is named")
	case !filepath.IsAbs(c.Command):
		return nil, fmt.Errorf("the confinement wrapper %q is not an absolute path", c.Command)
	case len(pin) != 64:
		return nil, fmt.Errorf("the confinement wrapper %s has no pinned sha256", c.Command)
	}
	f, err := openWrapper(c.Command)
	if err != nil {
		return nil, fmt.Errorf("the confinement wrapper is unavailable: %w", err)
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("the confinement wrapper %s is not a regular file", c.Command)
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		f.Close()
		return nil, fmt.Errorf("the confinement wrapper is unreadable: %w", err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != pin {
		f.Close()
		return nil, fmt.Errorf("the confinement wrapper %s has sha256 %s, not the pinned %s", c.Command, short(got), short(pin))
	}
	return f, nil
}

// commandFor builds the confined process from the wrapper open() verified.
// The child inherits that descriptor as fd 3 and the kernel executes
// /proc/self/fd/3, which in the child is exactly the hashed file.
func (c Confinement) commandFor(ctx context.Context, wrapper *os.File, dir string, check Check) *exec.Cmd {
	args := append(append(append([]string(nil), c.Args...), "--", check.Command), check.Args...)
	cmd := exec.CommandContext(ctx, "/proc/self/fd/3", args...)
	cmd.Args[0] = c.Command
	cmd.ExtraFiles = []*os.File{wrapper}
	cmd.Dir = dir
	return cmd
}

// role is whose bytes an execution runs against. The baseline is never the
// candidate's workspace, and the launcher is told which one it is given.
type role string

const (
	candidateRole role = "candidate"
	baselineRole  role = "baseline"
)

// execution is the typed result of running one check: exactly one of a
// refusal, a process that could not be run, or the command's own exit status.
//
// refused is launcher-originated only -- the broker's own pre-run checks, or
// under RequireStatus the launcher's record or the absence of a valid one. No
// exit status the candidate can produce is read as a refusal under the status
// protocol, so a candidate exiting 125 is a candidate exiting 125.
type execution struct {
	refused string
	err     error
	exit    int
	output  []byte
}

// execute is the one execution primitive: every candidate check and every
// baseline attribution runs through it, confined exactly as the runner is.
func (r Runner) execute(ctx context.Context, dir string, who role, check Check) execution {
	if r.Refusal != "" {
		return execution{refused: r.Refusal}
	}
	if r.Confine == nil {
		cmd := exec.CommandContext(ctx, check.Command, check.Args...)
		cmd.Dir = dir
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		return exited(cmd.Run(), out.Bytes())
	}
	wrapper, err := r.Confine.open()
	if err != nil {
		return execution{refused: err.Error()}
	}
	return r.Confine.launch(ctx, wrapper, dir, who, check)
}

// exited reads a finished unconfined (or legacy-confined) process.
func exited(err error, out []byte) execution {
	x := execution{output: out}
	if err != nil {
		var exitErr *exec.ExitError
		if asExitError(err, &exitErr) {
			x.exit = exitErr.ExitCode()
		} else {
			x.err = err
		}
	}
	return x
}

// statusGrace bounds how long the status and output descriptors may stay open
// after the wrapper exits: one held open by anything else is a protocol
// violation, and what was read by then is not a result. It also bounds how
// long the wrapper may take to exit once it has written its record, or once
// its status stream has ended: the record comes after the command has ended,
// and a stream that has ended can carry nothing more, so a wrapper still
// there past the grace is not finishing a check, and its status is not
// evidence.
const statusGrace = 2 * time.Second

// maxStatus bounds the status record: past it, or at it without the record's
// terminator, the record can never be valid and the invocation is ended.
const maxStatus = 4096

// launch runs the check through the wrapper open() verified, and owns it.
func (c Confinement) launch(ctx context.Context, wrapper *os.File, dir string, who role, check Check) execution {
	cmd := c.commandFor(ctx, wrapper, dir, check)
	defer release(cmd)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if !c.RequireStatus {
		err := cmd.Run()
		var exitErr *exec.ExitError
		// A wrapper killed by a signal has no exit status (ExitCode -1): it
		// is the wrapper's failure as much as its own failure exit is, and
		// fails closed rather than becoming the candidate's.
		if err != nil && (!asExitError(err, &exitErr) || exitErr.ExitCode() < 0 || exitErr.ExitCode() == c.failureExit()) {
			return execution{refused: "the confinement wrapper failed: " + err.Error()}
		}
		return exited(err, out.Bytes())
	}

	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return execution{refused: "no status nonce could be drawn: " + err.Error()}
	}
	token := hex.EncodeToString(nonce)
	statusR, statusW, err := os.Pipe()
	if err != nil {
		return execution{refused: "no status descriptor: " + err.Error()}
	}
	defer statusR.Close()
	nonceR, nonceW, err := os.Pipe()
	if err != nil {
		statusW.Close()
		return execution{refused: "no nonce descriptor: " + err.Error()}
	}
	cmd.ExtraFiles = append(cmd.ExtraFiles, statusW, nonceR)
	cmd.Env = c.statusEnv(dir, who, check)
	_, werr := io.WriteString(nonceW, token+"\n")
	nonceW.Close()
	if werr != nil {
		return execution{refused: "the nonce could not be written: " + werr.Error()}
	}
	// The output pipe is the broker's own, like the status pipe: Wait then
	// returns as soon as the wrapper has exited (or been killed), and every
	// read after that is bounded by the grace rather than by whatever the
	// wrapper left holding a descriptor. A running build is never subject to
	// the grace: the bound starts at the exit.
	outR, outW, err := os.Pipe()
	if err != nil {
		return execution{refused: "no output descriptor: " + err.Error()}
	}
	defer outR.Close()
	cmd.Stdout, cmd.Stderr = outW, outW
	if err := cmd.Start(); err != nil {
		outW.Close()
		return execution{refused: "the confinement wrapper could not be started: " + err.Error()}
	}
	// Only the wrapper and what it starts hold the write ends now: EOF is
	// the last close.
	statusW.Close()
	nonceR.Close()
	outW.Close()
	type read struct {
		b   []byte
		err error
		// ended names the violation that made the status terminally invalid,
		// after which the reader stopped and ended the invocation.
		ended string
	}
	got, captured := make(chan read, 1), make(chan read, 1)
	// Closed once Wait has returned: the one fact the status reader needs
	// when a completion bound expires, which it must learn without a second
	// Wait of its own (RULING-349).
	exited := make(chan struct{})
	go func() {
		// Read as the bytes come, and stop at the first byte after which
		// nothing further could make the status a record: the bound reached
		// without the newline a record must end with, or anything after the
		// first newline, which must end the whole record. The invocation is
		// over at that byte: the wrapper this broker started is terminated
		// here, while it may still be running and before Wait could return,
		// and nothing more is read. Draining a stream that may never end has
		// no evidentiary value and is exactly how a malformed launcher would
		// hold validation forever (RULING-343, RULING-347). Below the bound
		// without a newline the record may still complete, and the stream is
		// waited for as a running build is, however slow. Each read takes at
		// most what the bound still allows plus one byte, so no more is read
		// than the violation needs, and the violation is judged on the bytes
		// in order, never on how many one read returned (RULING-348).
		var b []byte
		chunk := make([]byte, maxStatus+1)
		// A complete line opens the completion phase: the wrapper writes its
		// record after the command has ended, so what remains is for it to
		// exit and the stream to end, and that has the grace, armed here
		// without waiting for Wait (RULING-349). The end of the stream opens
		// it just the same, record or no record: nothing more can arrive,
		// and only the exit remains (RULING-350). Whichever comes first owns
		// the one deadline, completeBy; the other never restarts it. Before
		// either, nothing is bounded by elapsed time: the check may still be
		// running.
		var completeBy time.Time
		for {
			n, err := statusR.Read(chunk[:maxStatus+1-len(b)])
			b = append(b, chunk[:n]...)
			if why := terminallyInvalid(b); why != "" {
				// Termination is requested of the one process this invocation
				// started; a wrapper that has already exited is no less
				// refused, and the request is not reported as more than it is.
				_ = cmd.Process.Kill()
				got <- read{b: b, ended: why}
				return
			}
			if err == io.EOF {
				// The stream has ended. The bytes are handed over when the
				// wrapper has exited (Wait is where the main goroutine is in
				// the meantime, so a wrapper that exits sees no delay), or at
				// the deadline, when the one process this invocation started
				// is terminated and its status is not evidence.
				if completeBy.IsZero() {
					completeBy = time.Now().Add(statusGrace)
				}
				select {
				case <-exited:
					got <- read{b: b}
					return
				case <-time.After(time.Until(completeBy)):
				}
				select {
				case <-exited:
					got <- read{b: b}
				default:
					_ = cmd.Process.Kill()
					got <- read{b: b, ended: "the status stream ended before the wrapper completed; the wrapper did not complete within the completion grace; its status is not evidence"}
				}
				return
			}
			if err != nil {
				if !completeBy.IsZero() && errors.Is(err, os.ErrDeadlineExceeded) {
					select {
					case <-exited:
						// The wrapper is gone and something it started holds
						// the descriptor: the post-exit handling names that.
						got <- read{b: b, err: err}
					default:
						// The record is complete and the wrapper has not
						// completed: whether it is still running or exited in
						// this very instant is not known here, and need not be;
						// termination is requested of the one process this
						// invocation started, and its status is not evidence.
						_ = cmd.Process.Kill()
						got <- read{b: b, ended: "the wrapper did not complete within the post-record grace; its status is not evidence"}
					}
					return
				}
				got <- read{b: b, err: err}
				return
			}
			if completeBy.IsZero() && len(b) > 0 && b[len(b)-1] == '\n' {
				// Any complete line, whatever the parser will make of it: a
				// malformed line followed by a wrapper that never exits would
				// otherwise hold validation exactly as a valid one would.
				completeBy = time.Now().Add(statusGrace)
				_ = statusR.SetReadDeadline(completeBy)
			}
		}
	}()
	go func() {
		b, err := io.ReadAll(outR)
		captured <- read{b: b, err: err}
	}()
	waitErr := cmd.Wait()
	close(exited)
	// The wrapper is gone. Anything it left holding the status or the output
	// descriptor now has the grace, after which the record is read as
	// whatever it was and the capture is what it is: incomplete.
	deadline := time.Now().Add(statusGrace)
	_ = statusR.SetReadDeadline(deadline)
	_ = outR.SetReadDeadline(deadline)
	rec, capture := <-got, <-captured
	out.Reset()
	out.Write(capture.b)

	refuse := func(why string) execution {
		if ctx.Err() != nil {
			return execution{err: fmt.Errorf("the check did not finish: %w", ctx.Err()), output: out.Bytes()}
		}
		return execution{refused: "the confinement wrapper reported no valid launcher status (" + StatusProtocol + "): " + why + "; whatever ran is not evidence"}
	}
	switch {
	case rec.ended != "":
		// The violation that ended the invocation: named first, whatever the
		// terminated wrapper's exit or its descendants left behind.
		return refuse(rec.ended + "; the invocation was ended as invalid")
	case capture.err != nil:
		// The wrapper exited but something it started kept the output pipe
		// past the grace: the capture ends where the broker cut it, and an
		// incomplete capture is not a result, whatever the status and the
		// exit say.
		return refuse("an output descriptor was still held open after the wrapper exited, so the capture is incomplete")
	case rec.err != nil:
		return refuse("the status descriptor was still held open after the wrapper exited")
	}
	ran, status, reason, perr := parseStatus(rec.b, token)
	if perr != nil {
		return refuse(perr.Error())
	}
	if !ran {
		return execution{refused: "the confinement launcher refused: " + reason}
	}
	wrapperExit := 0
	if waitErr != nil {
		var exitErr *exec.ExitError
		if !asExitError(waitErr, &exitErr) || exitErr.ExitCode() < 0 {
			return refuse("the wrapper recorded exit " + strconv.Itoa(status) + " but itself ended: " + waitErr.Error())
		}
		wrapperExit = exitErr.ExitCode()
	}
	if wrapperExit != status {
		return refuse(fmt.Sprintf("the wrapper recorded exit %d but exited %d", status, wrapperExit))
	}
	return execution{exit: status, output: out.Bytes()}
}

// terminallyInvalid names the violation after which no further byte could
// make b a status record, or is empty while one still could. A record is at
// most maxStatus bytes and ends with its only newline, so b is beyond repair
// the moment it holds maxStatus bytes without that newline (no record can
// fit: RULING-343, RULING-347) or any byte after its first newline
// (RULING-347). The earlier of the two in stream order is the violation,
// whatever one Read happened to return (RULING-348): a newline within the
// bound rules out the first, and its absence rules out the second. Short of
// the bound and without a newline, b is a record still being written.
func terminallyInvalid(b []byte) string {
	i := bytes.IndexByte(b, '\n')
	if i < 0 || i >= maxStatus {
		if len(b) >= maxStatus {
			return "the status record is too large: it cannot fit within its size bound; no valid record is possible"
		}
		return ""
	}
	if len(b) > i+1 {
		return "the status contains data after its record terminator; no valid record is possible"
	}
	return ""
}

// parseStatus reads exactly one StatusProtocol record bound to nonce.
func parseStatus(b []byte, nonce string) (ran bool, exit int, reason string, err error) {
	s := string(b)
	if s == "" {
		return false, 0, "", errors.New("no status record was written")
	}
	if !strings.HasSuffix(s, "\n") || strings.Count(s, "\n") != 1 {
		return false, 0, "", errors.New("the status is not exactly one record")
	}
	f := strings.SplitN(strings.TrimSuffix(s, "\n"), " ", 4)
	if len(f) < 3 || f[0] != StatusProtocol {
		return false, 0, "", errors.New("the status record is not " + StatusProtocol)
	}
	if f[1] != nonce {
		return false, 0, "", errors.New("the status record is not bound to this execution's nonce")
	}
	switch {
	case f[2] == "ran" && len(f) == 4:
		n, cerr := strconv.Atoi(f[3])
		if cerr != nil || n < 0 || n > 255 || strconv.Itoa(n) != f[3] {
			return false, 0, "", errors.New("the status record has no valid exit status")
		}
		return true, n, "", nil
	case f[2] == "refused":
		reason = "no reason given"
		if len(f) == 4 && strings.TrimSpace(f[3]) != "" {
			reason = strings.TrimSpace(f[3])
		}
		return false, 0, reason, nil
	}
	return false, 0, "", errors.New("the status record is neither ran nor refused")
}

// statusEnv is the whole environment a RequireStatus wrapper is started with:
// nothing of the host's but PATH, and nothing the repository controls.
func (c Confinement) statusEnv(dir string, who role, check Check) []string {
	mode := "ro"
	if check.Mutates {
		mode = "rw"
	}
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"SENSEI_CONFINE_PROTOCOL=" + StatusProtocol,
		"SENSEI_CONFINE_PROFILE=" + c.Profile,
		"SENSEI_CONFINE_STATUS_FD=4",
		"SENSEI_CONFINE_NONCE_FD=5",
		"SENSEI_CONFINE_ROLE=" + string(who),
		"SENSEI_CONFINE_WORKSPACE=" + dir,
		"SENSEI_CONFINE_WORKSPACE_MODE=" + mode,
		"SENSEI_CONFINE_CHECK=" + string(check.Kind),
	}
}

// release closes the descriptors a command was given.
func release(cmd *exec.Cmd) {
	for _, f := range cmd.ExtraFiles {
		f.Close()
	}
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
		e, _ := r.one(ctx, candidateID, diffDigest, check, now)
		bundle.Checks = append(bundle.Checks, e)
	}
	return bundle
}

// one executes a single check. It also returns the full captured output, which
// Evidence keeps only a bounded prefix of: a named required test's own verdict
// line can sit past the bound, and reading it from the truncated copy would
// report a test that ran as one that did not.
func (r Runner) one(ctx context.Context, candidateID, diffDigest string, check Check, now func() time.Time) (Evidence, string) {
	e := Evidence{
		Kind: check.Kind, Command: check.Command, Args: check.Args,
		RequestedBy: "sensei-code workflow", ExecutedBy: "sensei-code execution broker",
		CandidateID: candidateID, DiffDigest: diffDigest,
		StartedAt: now().UTC(),
	}
	if r.RequestedBy != "" {
		e.RequestedBy = r.RequestedBy
	}
	if r.Permits != nil {
		if permitted, reason := r.Permits(check.Kind); !permitted {
			e.FinishedAt = now().UTC()
			e.Outcome = NotPermitted
			e.ExitStatus = -1
			e.Detail = reason
			return e, ""
		}
	}

	if r.Confine != nil {
		// The executor actually used, with the pinned profile it ran under:
		// carried in a field every ValidationRun record already declares.
		e.ExecutedBy = "sensei-code execution broker under confinement " + r.Confine.Describe()
	} else if r.Refusal != "" {
		e.ExecutedBy = "sensei-code execution broker (confinement required and unavailable; nothing executed)"
	}
	x := r.execute(ctx, r.Workspace, candidateRole, check)
	e.FinishedAt = now().UTC()
	if x.refused != "" {
		// Refused, by the broker or the launcher: there is no unconfined
		// fallback, and whatever the confined command did or did not do,
		// nothing here is evidence about the candidate.
		e.Outcome = Errored
		e.ExitStatus = -1
		e.Detail = SandboxUnavailable + ": " + x.refused + "; the check was not run as evidence"
		return e, ""
	}

	body := string(x.output)
	full := body
	sum := sha256.Sum256([]byte(body))
	e.OutputDigest = "sha256:" + hex.EncodeToString(sum[:])
	if len(body) > maxOutput {
		body = body[:maxOutput] + "\n… output truncated; digest covers the full capture"
	}
	e.Output = body

	switch {
	case x.err != nil:
		// Could not run at all: missing binary, deadline, permission. Not a
		// failure of the candidate and definitely not a pass.
		e.ExitStatus = -1
		e.Outcome = Errored
		e.Detail = x.err.Error()
	case x.exit == 0 && check.FailIfOutput && strings.TrimSpace(body) != "":
		// Exited zero but reported a problem on stdout.
		e.Outcome = Failed
		e.Detail = "the check exited zero but reported output, which this check treats as failure"
		return r.attribute(ctx, check, e), full
	case x.exit == 0:
		e.Outcome = Passed
	default:
		e.ExitStatus = x.exit
		e.Outcome = Failed
		return r.attribute(ctx, check, e), full
	}
	return e, full
}

func asExitError(err error, target **exec.ExitError) bool {
	if ee, ok := err.(*exec.ExitError); ok {
		*target = ee
		return true
	}
	return false
}

// attribute decides whether a failing check is the candidate's fault by running
// the same command against a clean checkout of the base.
//
// This makes the judgement empirical rather than a guess about which error
// messages look environmental. A check that fails identically on the base was
// not broken by this candidate, whatever the output says.
func (r Runner) attribute(ctx context.Context, check Check, e Evidence) Evidence {
	if r.Baseline == nil {
		e.Attribution = "unattributed"
		e.Detail = "no baseline was available, so this failure was not attributed to the candidate or to the environment"
		return e
	}
	base, err := r.Baseline()
	if err != nil || strings.TrimSpace(base) == "" {
		e.Attribution = "unattributed"
		e.Detail = "baseline checkout unavailable, so this failure could not be attributed"
		if err != nil {
			e.Detail += ": " + err.Error()
		}
		return e
	}

	x := r.execute(ctx, base, baselineRole, check)
	if x.refused != "" {
		// The base's own confinement failed. That says nothing about the
		// candidate, so the failure is neither the candidate's nor
		// pre-existing: it is unattributed and certifies nothing, and it is
		// an environment problem no revision of the candidate can fix.
		e.Outcome = Errored
		e.Attribution = "unattributed"
		e.Detail = SandboxUnavailable + " (baseline): the base could not be run under the same confinement, so this failure (exit " +
			strconv.Itoa(e.ExitStatus) + ") was not attributed to the candidate or to the base: " + x.refused
		return e
	}
	out := bytes.NewBuffer(x.output)

	// The base is judged under the same Check semantics as the candidate, and
	// "the same way" means the same exit status and the same complete combined
	// output. A base that merely also fails is not the candidate's failure
	// reproduced.
	baseStatus := 0
	switch {
	case x.err != nil:
		e.Attribution = "unattributed"
		e.Detail = "the check could not be run against the base commit, so this failure could not be attributed: " + x.err.Error()
		if r.Confine != nil && r.Confine.RequireStatus {
			// Under the required-status profile a baseline that was interrupted
			// or could not run is an environment outcome: the candidate's own
			// exit stays recorded as the observation it is, but the failure is
			// nobody's to act on and certifies nothing. The legacy convention
			// keeps its reading of the same case.
			e.Outcome = Errored
			e.Detail = "the baseline execution did not complete under the required confinement, so this failure (exit " +
				strconv.Itoa(e.ExitStatus) + ") was not attributed to the candidate or to the base: " + x.err.Error()
		}
		return e
	case x.exit == 0:
		if !check.FailIfOutput || strings.TrimSpace(out.String()) == "" {
			e.Attribution = "candidate"
			return e
		}
	default:
		baseStatus = x.exit
	}
	sum := sha256.Sum256(out.Bytes())
	if baseStatus == e.ExitStatus && "sha256:"+hex.EncodeToString(sum[:]) == e.OutputDigest {
		e.Outcome = Infrastructure
		e.Attribution = "pre-existing"
		e.Detail = "this check fails the same way against the base commit (same exit status and identical output), so the candidate did not cause it and no edit to the candidate can fix it"
		return e
	}
	e.Attribution = "candidate"
	if e.Detail != "" {
		e.Detail += "; "
	}
	e.Detail += fmt.Sprintf("the base commit also fails this check, but differently (base exit %d, output %s), so equivalence was not established and this failure remains the candidate's to fix", baseStatus, short("sha256:"+hex.EncodeToString(sum[:])))
	return e
}

// RequiredTest is the broker's record of one NAMED required test, executed on
// its own against one exact candidate.
//
// A green suite is not this record. "go test ./... exited zero" says nothing a
// reader can hold a named test to: the package may not have been selected, the
// test may have been skipped by name, or it may not exist at all. So the broker
// runs the named test by itself and reads that test's own verdict line, and a
// required test is discharged only by that line -- never by the suite, by the
// diff, or by anyone reporting it.
type RequiredTest struct {
	// ID is the canonical required-test id, "path/to/file_test.go:TestName".
	// It is the only key anything may correlate on.
	ID string `json:"id"`

	CandidateID string `json:"candidate_id"`
	DiffDigest  string `json:"diff_digest"`

	// Executed records that the named test itself ran: its own verdict line
	// was observed. A command that exited zero having run nothing ("no tests to
	// run") did not execute it.
	Executed bool `json:"executed"`
	// Passed records that the named test ran and its own verdict was PASS, and
	// the command that ran it passed as a whole.
	Passed bool `json:"passed"`

	// Evidence is the execution that produced this record.
	Evidence Evidence `json:"evidence"`
}

// Discharges reports whether this record proves the named test executed and
// passed against exactly this candidate content. Every leg is checked here
// rather than trusted from Passed alone, so a record assembled anywhere but
// the broker, or bound to other bytes, discharges nothing.
func (t RequiredTest) Discharges(id, candidateID, diffDigest string) bool {
	return t.ID != "" && t.ID == CanonicalRequiredTestID(id) &&
		t.CandidateID == candidateID && t.DiffDigest != "" && t.DiffDigest == diffDigest &&
		t.Executed && t.Passed &&
		t.Evidence.Outcome == Passed && t.Evidence.Kind == Test &&
		t.Evidence.Certifies(candidateID, diffDigest)
}

// CanonicalRequiredTestID reduces a required-test id to its canonical form.
//
// The graph names a test class-qualified ("test:path:Name") and the corpus
// names it bare ("path:Name"); both denote one test. Only that class prefix and
// surrounding space are removed -- nothing else is normalized, so two ids that
// differ in any other way stay two tests.
func CanonicalRequiredTestID(id string) string {
	return strings.TrimPrefix(strings.TrimSpace(id), "test:")
}

// requiredTestTarget splits a canonical id into the package directory and the
// test function, refusing anything that is not exactly one Go test in one
// repository-relative test file.
func requiredTestTarget(id string) (dir, name string, ok bool) {
	path, name, found := strings.Cut(id, ":")
	if !found || !strings.HasSuffix(path, "_test.go") || strings.HasPrefix(path, "/") ||
		strings.Contains(path, "..") || !strings.HasPrefix(name, "Test") {
		return "", "", false
	}
	for _, r := range name {
		if !(r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return "", "", false
		}
	}
	dir = "."
	if i := strings.LastIndex(path, "/"); i >= 0 {
		dir = "./" + path[:i]
	}
	return dir, name, true
}

// RunRequiredTests executes each named required test on its own against the
// candidate and records, per id, whether it executed and whether it passed.
//
// Ids are canonicalized and deduplicated; the result is in the order given. A
// malformed id, a check the envelope does not permit, or a test that did not
// run is recorded as not executed -- never dropped, because a required test
// with no record would read as one nobody asked about.
func (r Runner) RunRequiredTests(ctx context.Context, candidateID, diffDigest string, ids []string) []RequiredTest {
	now := r.Now
	if now == nil {
		now = time.Now
	}
	var out []RequiredTest
	seen := map[string]bool{}
	for _, raw := range ids {
		id := CanonicalRequiredTestID(raw)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		rec := RequiredTest{ID: id, CandidateID: candidateID, DiffDigest: diffDigest}
		dir, name, ok := requiredTestTarget(id)
		if !ok {
			at := now().UTC()
			rec.Evidence = Evidence{
				Kind: Test, RequestedBy: "sensei-code workflow", ExecutedBy: "sensei-code execution broker",
				CandidateID: candidateID, DiffDigest: diffDigest, StartedAt: at, FinishedAt: at,
				ExitStatus: -1, Outcome: Errored,
				Detail: "not a canonical required-test id (path/to/file_test.go:TestName), so nothing was run",
			}
			out = append(out, rec)
			continue
		}
		check := Check{Kind: Test, Command: "go", Args: []string{"test", "-count=1", "-v", "-run", "^" + name + "$", dir}}
		e, full := r.one(ctx, candidateID, diffDigest, check, now)
		rec.Evidence = e
		rec.Executed, rec.Passed = namedVerdict(full, name)
		rec.Passed = rec.Passed && e.Outcome == Passed
		if e.Outcome == Passed && !rec.Executed {
			rec.Evidence.Detail = "the command exited zero but " + name + " reported no verdict of its own, so it did not execute"
		}
		out = append(out, rec)
	}
	return out
}

// namedVerdict reads the named top-level test's own verdict line from
// `go test -v` output. Subtest lines are indented and never match.
func namedVerdict(output, name string) (executed, passed bool) {
	for _, line := range strings.Split(output, "\n") {
		switch {
		case strings.HasPrefix(line, "--- PASS: "+name+" ("):
			executed, passed = true, true
		case strings.HasPrefix(line, "--- FAIL: "+name+" ("):
			return true, false
		case strings.HasPrefix(line, "--- SKIP: "+name+" ("):
			// A skipped test did not run its assertions.
			return false, false
		}
	}
	return executed, passed
}

// RenderRequiredTests writes the per-test records for a reviewer.
func RenderRequiredTests(tests []RequiredTest) string {
	if len(tests) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("REQUIRED TESTS executed by name by the execution broker, not reported by the worker:\n")
	for _, t := range tests {
		state := "NOT EXECUTED"
		switch {
		case t.Executed && t.Passed:
			state = "executed, PASSED"
		case t.Executed:
			state = "executed, FAILED"
		}
		fmt.Fprintf(&sb, "  %s — %s (candidate %s at diff %s)\n", t.ID, state, t.CandidateID, short(t.DiffDigest))
		if t.Evidence.Detail != "" {
			fmt.Fprintf(&sb, "      %s\n", t.Evidence.Detail)
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}
