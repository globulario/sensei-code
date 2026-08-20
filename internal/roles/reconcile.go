package roles

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Reconciliation is the architect's account of why the loop took the branch it
// took when two agents disagreed.
//
// It is an orchestration receipt and not a governance one. Sensei owns whether
// a change may be admitted; this explains why the workflow sent the candidate
// back rather than forward, which is a question Sensei is not being asked.
// Recording it locally is safe precisely because it decides nothing — and it is
// worth recording because the alternative is a run that silently changed course
// and nobody can reconstruct why.
type Reconciliation struct {
	Provenance Provenance `json:"provenance"`
	// Disputed is the claim the agents disagreed about, stated once so the
	// receipt is about a question rather than about two personalities.
	Disputed string `json:"disputed"`
	// Inputs are what each agent said, attributed. They are inputs, not votes:
	// the count is never consulted.
	Inputs []Claim `json:"inputs"`
	// Canonical is the evidence the decision actually rests on — Sensei nodes,
	// repository facts, executed proof. Without at least one of these there is
	// nothing to decide from except agreement, and agreement is not evidence.
	Canonical []Evidence `json:"canonical"`
	Decision  string     `json:"decision"`
	// Authority says who owns this decision. An architect may resolve ordinary
	// architectural questions; it may not resolve a human-owned one by writing
	// a confident receipt.
	Authority Level `json:"authority"`
	// Remaining is what the decision did not settle. A receipt with no residual
	// uncertainty is usually a receipt that stopped looking.
	Remaining string `json:"remaining,omitempty"`
}

// Claim is one agent's position.
type Claim struct {
	Agent    string `json:"agent"`
	Role     Role   `json:"role"`
	Position string `json:"position"`
}

// Evidence is something canonical the decision rests on.
type Evidence struct {
	// Kind is where it came from. A Sensei node and a model's recollection of a
	// Sensei node are not the same thing and must not be recorded the same way.
	Kind EvidenceKind `json:"kind"`
	// Reference is the id, path or command that can be checked.
	Reference string `json:"reference"`
	Detail    string `json:"detail,omitempty"`
}

type EvidenceKind string

const (
	// GraphEvidence is a governed node: an invariant, failure mode, audit
	// verdict — something Sensei holds and can be re-read.
	GraphEvidence EvidenceKind = "graph"
	// RepositoryEvidence is something in the code or in a file here.
	RepositoryEvidence EvidenceKind = "repository"
	// ProofEvidence is a check that was executed, with its outcome.
	ProofEvidence EvidenceKind = "proof"
)

func (k EvidenceKind) Valid() bool {
	return k == GraphEvidence || k == RepositoryEvidence || k == ProofEvidence
}

// Level is who owns a decision.
type Level string

const (
	// ArchitectAuthority is an ordinary architectural decision the architect may
	// make alone.
	ArchitectAuthority Level = "architect"
	// HumanAuthority is a decision that changes human-owned intent, policy,
	// contract or trust. An architect naming this level is declining to decide,
	// which is the correct outcome and not a failure of the reconciliation.
	HumanAuthority Level = "human"
)

func (l Level) Valid() bool { return l == ArchitectAuthority || l == HumanAuthority }

// Validate refuses a receipt that would let agreement stand in for evidence.
//
// This is the mechanical form of "agents specialize; they do not vote". There
// is no majority function in this package to call, and there is no path by
// which unanimity alone produces a decision: a reconciliation with no canonical
// evidence is refused however many agents agreed, and the refusal says so.
func (r Reconciliation) Validate() error {
	if strings.TrimSpace(r.Disputed) == "" {
		return errors.New("reconciliation does not say what was disputed")
	}
	if strings.TrimSpace(r.Decision) == "" {
		return errors.New("reconciliation records no decision")
	}
	if !r.Authority.Valid() {
		return fmt.Errorf("reconciliation authority must be architect or human, got %q", r.Authority)
	}
	if len(r.Inputs) < 2 {
		return errors.New("reconciliation needs the positions it reconciled")
	}
	var canonical int
	for _, e := range r.Canonical {
		if !e.Kind.Valid() {
			return fmt.Errorf("evidence kind must be graph, repository, or proof, got %q", e.Kind)
		}
		if strings.TrimSpace(e.Reference) == "" {
			return errors.New("evidence must name something that can be checked")
		}
		canonical++
	}
	if canonical == 0 {
		return fmt.Errorf(
			"reconciliation of %q rests on no canonical evidence: %s. "+
				"Agreement between agents is not evidence, and a decision with nothing to check is a preference",
			r.Disputed, describeAgreement(r.Inputs))
	}
	return nil
}

// Unanimous reports whether every input said the same thing.
//
// It exists so the transcript can say "all three agreed, and that is not why
// this was decided" — never so a caller can branch on it. Nothing in this
// package consumes it, which is the point: the fact is reportable and not
// actionable.
func (r Reconciliation) Unanimous() bool {
	if len(r.Inputs) < 2 {
		return false
	}
	first := normalizePosition(r.Inputs[0].Position)
	for _, in := range r.Inputs[1:] {
		if normalizePosition(in.Position) != first {
			return false
		}
	}
	return true
}

// Describe is the receipt as a human reads it.
func (r Reconciliation) Describe() string {
	var b strings.Builder
	b.WriteString("disputed: " + strings.TrimSpace(r.Disputed) + "\n")
	for _, in := range r.Inputs {
		b.WriteString("  " + in.Agent + " (" + in.Role.Label() + "): " + strings.TrimSpace(in.Position) + "\n")
	}
	b.WriteString("evidence:\n")
	for _, e := range r.Canonical {
		line := "  " + string(e.Kind) + " " + e.Reference
		if d := strings.TrimSpace(e.Detail); d != "" {
			line += " — " + d
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("decision (" + string(r.Authority) + "): " + strings.TrimSpace(r.Decision) + "\n")
	if rem := strings.TrimSpace(r.Remaining); rem != "" {
		b.WriteString("still open: " + rem + "\n")
	}
	if r.Unanimous() {
		b.WriteString("(the inputs agreed; the decision rests on the evidence above, not on the agreement)\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func describeAgreement(inputs []Claim) string {
	positions := map[string][]string{}
	for _, in := range inputs {
		key := normalizePosition(in.Position)
		positions[key] = append(positions[key], in.Agent)
	}
	if len(positions) == 1 {
		return fmt.Sprintf("all %d inputs agreed", len(inputs))
	}
	keys := make([]string, 0, len(positions))
	for k := range positions {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%d for %q", len(positions[k]), k))
	}
	return strings.Join(parts, ", ")
}

func normalizePosition(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}
