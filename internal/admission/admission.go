// Package admission builds the exact invocations that cross a reviewed
// candidate into Sensei's canonical admission, application and verification.
//
// Sensei Code stops at "candidate ready for governed admission". The step after
// that is not a format this repository gets to invent: admission evaluates a
// convergence bundle — session, status report, iteration, maintained claims,
// maintenance, plane assessment, closure before and after, dialogue, question
// report, probes — which is Sensei's own synthesis output. Composing something
// bundle-shaped locally would produce records that satisfy the schema and mean
// nothing, which is worse than not admitting at all.
//
// So this package builds argv and reads exit codes. Nothing here decides
// anything: every judgement belongs to the command being invoked, and the
// chain's own rules are quoted rather than reimplemented.
//
//	synthesis-run    seals a candidate and writes its lineage
//	synthesis-admit  derives the admission REQUEST from that lineage
//	admit-change     DECIDES, against the bundle and the graph
//	synthesis-apply  materialises the exact admitted candidate
//	verify-admission checks the resulting tree stayed inside the envelope
//
// Two of those distinctions are load-bearing and easy to lose. Admission is
// permission to attempt, not proof of correctness. And scope compliance is not
// correctness certification — a verified application is a change that stayed
// inside its envelope, not a change that works.
//
// Separating argv construction from execution is deliberate and follows
// internal/decision: the command a step would run can be asserted in a test
// without running it, which is the only way to check this chain at all while a
// governed run is unavailable.
package admission

import (
	"fmt"
	"strings"
)

// Step names one stage of the canonical chain. The vocabulary is closed because
// the chain is: a step this package does not know is a step Sensei Code has no
// business inventing.
type Step string

const (
	Compose Step = "synthesis-admit"
	Decide  Step = "admit-change"
	Apply   Step = "synthesis-apply"
	Verify  Step = "verify-admission"
)

// Invocation is one exact command, with the reason it is being run.
type Invocation struct {
	Step Step
	Args []string
}

// Command renders the invocation for a receipt or a transcript.
func (i Invocation) Command() string { return "sensei " + strings.Join(i.Args, " ") }

// Request is what Sensei Code knows about a candidate it wants admitted.
//
// Every field is a path or an identity produced by something else. There is no
// field here describing what the change does, because admission does not take
// this repository's word for that: synthesis-admit derives the mutation scope
// by diffing the sealed manifest against the base revision, and explicitly
// never takes it from a template, a declared scope, or a description.
type Request struct {
	// LineagePath is the <candidate-digest>.lineage.json a completed
	// synthesis-run persisted beside the sealed candidate.
	LineagePath string
	// BundleDir is the convergence bundle the decision is evaluated against.
	BundleDir string
	// RequestPath is where synthesis-admit writes, and admit-change reads.
	RequestPath string
	// DecisionPath is where admit-change writes its canonical decision.
	DecisionPath string
	// GraphNT is the graph snapshot the decision is evaluated against.
	GraphNT string
	// Repo is the checkout admission is about; Target is the dedicated,
	// clean worktree an admitted candidate is applied into. They are separate
	// because apply refuses a target whose HEAD is not the admitted base.
	Repo   string
	Target string
	// PolicyID is Sensei's admission policy. Empty means the command's own
	// default, which is strict; this package never softens it.
	PolicyID string
}

// Validate reports whether the chain can be run at all.
func (r Request) Validate() error {
	missing := []string{}
	for name, value := range map[string]string{
		"lineage": r.LineagePath, "bundle": r.BundleDir, "request": r.RequestPath,
		"decision": r.DecisionPath, "graph-nt": r.GraphNT, "repo": r.Repo, "target": r.Target,
	} {
		if strings.TrimSpace(value) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) != 0 {
		return fmt.Errorf("admission chain needs %s; a step invoked without them would be refused by Sensei, "+
			"and guessing them here would be inventing inputs admission is supposed to derive", strings.Join(sorted(missing), ", "))
	}
	if r.Repo == r.Target {
		return fmt.Errorf("the admitted candidate must be applied into a dedicated worktree, not into %s: "+
			"apply refuses a target that is dirty or not at the admitted base, and the checkout under discussion is neither guaranteed to be", r.Repo)
	}
	return nil
}

// Chain is the ordered invocations that carry a sealed candidate through
// admission to verification.
//
// synthesis-run is deliberately absent. It is the producer of the candidate and
// the bundle, and it runs before this chain exists; folding it in here would
// suggest Sensei Code can produce a convergence session, which is exactly the
// thing it must not claim.
func (r Request) Chain() ([]Invocation, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	compose := []string{string(Compose), "--lineage", r.LineagePath, "--output", r.RequestPath}
	decide := []string{string(Decide),
		"--bundle", r.BundleDir, "--request", r.RequestPath,
		"--graph-nt", r.GraphNT, "--repo", r.Repo,
		"--output", r.DecisionPath, "--mode", "full"}
	if p := strings.TrimSpace(r.PolicyID); p != "" {
		decide = append(decide, "--policy", p)
	}
	apply := []string{string(Apply), "--lineage", r.LineagePath, "--decision", r.DecisionPath, "--target", r.Target}
	verify := []string{string(Verify), "--decision", r.DecisionPath, "--bundle", r.BundleDir, "--repo", r.Target}

	return []Invocation{
		{Step: Compose, Args: compose},
		{Step: Decide, Args: decide},
		{Step: Apply, Args: apply},
		{Step: Verify, Args: verify},
	}, nil
}

func sorted(in []string) []string {
	out := append([]string(nil), in...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// Outcome is what one step reported. It is Sensei's verdict, carried, never
// this package's interpretation of it.
type Outcome struct {
	Step Step
	// Code is the process exit status. synthesis-admit documents a vocabulary
	// richer than success/failure and it is preserved rather than flattened.
	Code int
	// Refused distinguishes "Sensei declined this candidate" from "the step
	// could not run". Collapsing them would let a broken toolchain read as an
	// architectural refusal, and a refusal read as an outage.
	Refused bool
	Detail  string
	Output  string
}

// Interpret turns a step's exit status into a stated outcome.
//
// The codes are Sensei's, quoted from synthesis-admit's own documentation. A
// code this build does not recognise is reported as unrecognised rather than
// assumed to be failure or success: a future Sensei that adds an outcome must
// not have it silently mapped onto an existing meaning.
func Interpret(step Step, code int, output string) Outcome {
	out := Outcome{Step: step, Code: code, Output: output}
	if code == 0 {
		out.Detail = "completed"
		return out
	}
	if step == Compose {
		switch code {
		case 1:
			out.Detail = "inputs did not resolve"
			return out
		case 2:
			out.Detail = "invalid invocation"
			return out
		case 3:
			out.Refused, out.Detail = true, "the candidate performs an operation admission does not support "+
				"(add, delete, mode or type change, symlink mutation); a refusal receipt was written"
			return out
		case 4:
			out.Refused, out.Detail = true, "the candidate changes nothing at its base revision, so there is nothing to admit"
			return out
		}
	}
	out.Detail = fmt.Sprintf("%s exited %d, which this build does not recognise; "+
		"treat it as unresolved rather than as either success or refusal", step, code)
	return out
}

// Admitted reports whether the chain reached a decision that admitted the
// change. It is deliberately narrow: only the deciding step can answer it, and
// only a completed one.
//
// Nothing else in this repository may compute this. Reviewer acceptance is not
// admission, a passing diff audit is not admission, and a chain that ran
// without error is not admission either — admission is what admit-change said.
func Admitted(outcomes []Outcome) bool {
	for _, o := range outcomes {
		if o.Step == Decide {
			return o.Code == 0 && !o.Refused
		}
	}
	return false
}

// Verified reports whether the applied result stayed inside the admitted
// envelope. Scope compliance is not correctness certification, and this name is
// as strong a claim as the chain supports.
func Verified(outcomes []Outcome) bool {
	var applied, verified bool
	for _, o := range outcomes {
		switch o.Step {
		case Apply:
			applied = o.Code == 0
		case Verify:
			verified = o.Code == 0
		}
	}
	return applied && verified && Admitted(outcomes)
}

// Summary renders what the chain established, in the words the chain supports.
func Summary(outcomes []Outcome) string {
	if len(outcomes) == 0 {
		return "admission: not attempted, so this candidate is not admitted"
	}
	var b strings.Builder
	for _, o := range outcomes {
		fmt.Fprintf(&b, "%s: exit %d — %s\n", o.Step, o.Code, o.Detail)
	}
	switch {
	case Verified(outcomes):
		b.WriteString("admitted, applied, and verified to have stayed inside the envelope; " +
			"scope compliance is not correctness certification")
	case Admitted(outcomes):
		b.WriteString("admitted — permission to attempt, not proof of correctness — and not verified")
	default:
		b.WriteString("not admitted")
	}
	return b.String()
}
