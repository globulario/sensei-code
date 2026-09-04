package roles

import (
	"errors"
	"fmt"
	"strings"
)

// Severity is how much a finding matters. It is a closed vocabulary because a
// free-text severity cannot be compared, and a reviewer that grades its own
// findings on an invented scale can grade a blocking one down to nothing.
type Severity string

const (
	// Blocking means the candidate must not be accepted as it stands.
	Blocking Severity = "blocking"
	// Major means a real defect that does not by itself refuse the candidate.
	Major Severity = "major"
	// Minor is worth saying and does not affect the decision.
	Minor Severity = "minor"
)

func (s Severity) Valid() bool { return s == Blocking || s == Major || s == Minor }

// Finding is one concrete objection, attributable to something a person can go
// and look at.
//
// Reference is required for a blocking finding and the requirement is not
// bureaucratic: an objection with nowhere to point is one the worker cannot
// act on, and a review cycle spent on an unactionable objection produces a
// byte-identical diff and consumes the budget for nothing. That happened three
// times in one run before anybody noticed why.
type Finding struct {
	ID       string   `json:"id"`
	Severity Severity `json:"severity"`
	// Claim is what the finding challenges: the assertion the candidate or its
	// evidence makes that the reviewer believes is not established.
	Claim string `json:"claim"`
	// Reference is the file, component or piece of evidence it is about.
	Reference string `json:"reference,omitempty"`
	Reason    string `json:"reason"`
	// Correction is the required repair, or the proof that is missing. One of
	// the two: a finding that names neither is a complaint.
	Correction string `json:"correction,omitempty"`
	ProofGap   string `json:"proof_gap,omitempty"`
}

func (f Finding) Line() string {
	var b strings.Builder
	if f.ID != "" {
		b.WriteString("[" + f.ID + "] ")
	}
	if f.Severity != "" {
		b.WriteString(string(f.Severity) + ": ")
	}
	b.WriteString(strings.TrimSpace(f.Claim))
	if f.Reference != "" {
		b.WriteString(" (" + f.Reference + ")")
	}
	if r := strings.TrimSpace(f.Reason); r != "" {
		b.WriteString(" — " + r)
	}
	if c := strings.TrimSpace(f.Correction); c != "" {
		b.WriteString(" → " + c)
	} else if g := strings.TrimSpace(f.ProofGap); g != "" {
		b.WriteString(" → missing proof: " + g)
	}
	return b.String()
}

// Decision is the reviewer's bounded conclusion.
type Decision string

const (
	// Accept is the reviewer's opinion that the candidate stands. It is not
	// admission, and nothing in this package can make it into admission.
	Accept Decision = "accept"
	// Revise returns bounded instructions to the implementer.
	Revise Decision = "revise"
	// Escalate raises an architectural-authority question. It goes to the
	// architect, never to the human directly: a reviewer is not an authority
	// router, and letting one reach the human turns nervousness into a
	// Level-3 event.
	Escalate Decision = "escalate"
)

func (d Decision) Valid() bool { return d == Accept || d == Revise || d == Escalate }

// ReviewVerdict is one reviewer's structured conclusion about one exact
// candidate revision.
type ReviewVerdict struct {
	Provenance   Provenance `json:"provenance"`
	Decision     Decision   `json:"decision"`
	Summary      string     `json:"summary"`
	Instructions string     `json:"instructions,omitempty"`
	Findings     []Finding  `json:"findings,omitempty"`
}

// Accepts reports the reviewer's own conclusion and nothing more.
//
// The name is deliberately not "Accepted". A candidate is accepted when Sensei
// admits it; this is a reviewer saying it found nothing to object to, which is
// a different and weaker statement, and the two were conflated once already.
func (v ReviewVerdict) Accepts() bool { return v.Decision == Accept }

// Blocking returns the findings that refuse the candidate as it stands.
func (v ReviewVerdict) Blocking() []Finding {
	var out []Finding
	for _, f := range v.Findings {
		if f.Severity == Blocking {
			out = append(out, f)
		}
	}
	return out
}

// Validate refuses a verdict that cannot govern the next branch of the loop.
//
// implementer is the provider that produced the candidate. It is a parameter
// rather than a field because self-certification is a relation between two
// artifacts, and a verdict that carried its own answer to "did the author write
// this?" would be answering the question it is being asked.
func (v ReviewVerdict) Validate(b Binding, implementer string) error {
	if !v.Decision.Valid() {
		return fmt.Errorf("review decision must be accept, revise, or escalate, got %q", v.Decision)
	}
	if v.Provenance.Role != Reviewer {
		return fmt.Errorf("a %s cannot conclude a review", roleOrUnknown(v.Provenance.Role))
	}
	if err := b.Verify(v.Provenance); err != nil {
		if b.Stale(v.Provenance) {
			return fmt.Errorf("this review is about an earlier revision of the candidate and no longer applies: %w", err)
		}
		return err
	}
	if impl := strings.TrimSpace(implementer); impl != "" && strings.EqualFold(impl, strings.TrimSpace(v.Provenance.Provider)) {
		// Not a rule about trust in a particular model. An author reviewing its
		// own work has already decided the question, and its agreement carries
		// no information about whether the work is right.
		return fmt.Errorf("%s implemented this candidate and cannot also review it", v.Provenance.Provider)
	}
	if !v.Provenance.Independent() {
		return errors.New("a review must run in a session that inherited nothing from the work it is judging")
	}
	if strings.TrimSpace(v.Summary) == "" {
		return errors.New("review returned no summary")
	}
	for i, f := range v.Findings {
		if !f.Severity.Valid() {
			return fmt.Errorf("finding %d has severity %q, which is not blocking, major, or minor", i+1, f.Severity)
		}
		if f.Severity == Blocking && strings.TrimSpace(f.Reference) == "" {
			return fmt.Errorf("blocking finding %q points at nothing a worker could open", f.ID)
		}
	}
	if v.Decision == Revise && strings.TrimSpace(v.Instructions) == "" && len(v.Findings) == 0 {
		return errors.New("review asked for a revision without saying what to change")
	}
	if v.Decision == Accept && len(v.Blocking()) != 0 {
		// Accepting over one's own blocking finding is not a judgement call. It
		// is a verdict that disagrees with itself, and the disagreement would be
		// resolved silently in favour of the softer half.
		return fmt.Errorf("review accepted while recording %d blocking finding(s)", len(v.Blocking()))
	}
	return nil
}

// Instruction renders what the next implementer cycle must reconcile. Findings
// come first because they are specific; the prose summary is context around
// them, not a substitute for them.
func (v ReviewVerdict) Instruction() string {
	var b strings.Builder
	for _, f := range v.Findings {
		if f.Severity == Minor {
			continue
		}
		b.WriteString("- " + f.Line() + "\n")
	}
	if extra := strings.TrimSpace(v.Instructions); extra != "" {
		b.WriteString("\n" + extra)
	}
	if b.Len() == 0 {
		return strings.TrimSpace(v.Summary)
	}
	return strings.TrimRight(b.String(), "\n")
}

func roleOrUnknown(r Role) string {
	if strings.TrimSpace(string(r)) == "" {
		return "verdict with no stated role"
	}
	return r.Label()
}

// Advisory is a reviewer's conclusion whose independence was never established.
//
// It is a distinct type rather than a flag on ReviewVerdict, and the distinction
// is load-bearing in the only way that matters in Go: a function taking a
// ReviewVerdict cannot be handed one of these. Somewhere downstream there will
// be a caller that counts reviews, and the flag version of this design is the
// one where that caller forgets to read the flag. Here it cannot compile.
//
// What it IS: a real review, by a real party, with real findings, about an exact
// candidate. It may return REVISE and drive the repair loop, and it may
// afterwards return ACCEPT. What it is NOT: evidence that anybody independent
// looked. The adversarial-independence invariant is not weakened by this type;
// it is the reason this type exists instead of a looser ReviewVerdict.
type Advisory struct {
	ReviewVerdict
}

// NewAdvisory types a verdict whose isolation was not established.
func NewAdvisory(v ReviewVerdict) Advisory { return Advisory{ReviewVerdict: v} }

// SatisfiesAdversarialObligation is always false, and is a method rather than a
// comment so that a caller asking the question gets the answer in code.
func (a Advisory) SatisfiesAdversarialObligation() bool { return false }

// Validate refuses an advisory verdict that cannot govern the next branch of
// the loop.
//
// It shadows ReviewVerdict.Validate deliberately: every check there applies
// except the independence one, which is not skipped so much as inapplicable --
// this type exists precisely for the case where independence was not
// established. Everything else is unchanged, including the exact-candidate
// binding and the refusal to let an author review its own work.
//
// It additionally refuses a verdict that CLAIMS independence. An advisory
// verdict marked Fresh is a contradiction: something recorded isolation for a
// turn nobody observed, and the safe reading of that is that the recording is
// wrong rather than that the isolation is real.
func (a Advisory) Validate(b Binding, implementer string) error {
	if a.Provenance.Independent() {
		return errors.New("this review was typed advisory and its provenance claims an independent session; one of the two is wrong, and it is not safe to assume it is the typing")
	}
	if !a.Decision.Valid() {
		return fmt.Errorf("review decision must be accept, revise, or escalate, got %q", a.Decision)
	}
	if a.Provenance.Role != Reviewer {
		return fmt.Errorf("a %s cannot conclude a review", roleOrUnknown(a.Provenance.Role))
	}
	if err := b.Verify(a.Provenance); err != nil {
		if b.Stale(a.Provenance) {
			return fmt.Errorf("this review is about an earlier revision of the candidate and no longer applies: %w", err)
		}
		return err
	}
	if impl := strings.TrimSpace(implementer); impl != "" && strings.EqualFold(impl, strings.TrimSpace(a.Provenance.Provider)) {
		return fmt.Errorf("%s implemented this candidate and cannot also review it", a.Provenance.Provider)
	}
	if strings.TrimSpace(a.Summary) == "" {
		return errors.New("review returned no summary")
	}
	for i, f := range a.Findings {
		if !f.Severity.Valid() {
			return fmt.Errorf("finding %d has severity %q, which is not blocking, major, or minor", i+1, f.Severity)
		}
		if f.Severity == Blocking && strings.TrimSpace(f.Reference) == "" {
			return fmt.Errorf("blocking finding %q points at nothing a worker could open", f.ID)
		}
	}
	if a.Decision == Revise && strings.TrimSpace(a.Instructions) == "" && len(a.Findings) == 0 {
		return errors.New("review asked for a revision without saying what to change")
	}
	if a.Decision == Accept && len(a.Blocking()) != 0 {
		return fmt.Errorf("review accepted while recording %d blocking finding(s)", len(a.Blocking()))
	}
	return nil
}

// Describe is the line the record carries, written so an advisory accept cannot
// be read as more than it is.
func (a Advisory) Describe() string {
	return "advisory " + string(a.Decision) + " by " + a.Provenance.Provider +
		" (session " + string(a.Provenance.SessionMode) + "): satisfies no adversarial-review obligation, and is not admission"
}
