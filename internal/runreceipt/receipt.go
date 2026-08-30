// Package runreceipt is the governed run's own account of itself.
//
// A governed run already knows who governed it, what it was based on, what
// candidate it produced, who reviewed it and how it ended. Until now an
// external witness had to reconstruct all of that from a general-purpose event
// stream after the fact. That reconstruction was 673 lines of laboratory
// apparatus, and it destroyed an expensive run when a 62-line ad-hoc parser met
// one event whose nested field was a string where the parser's author had
// assumed an object -- a shape that already existed in the previous run's
// preserved log, unexamined.
//
// So the receipt exists to move those facts from reconstruction to emission.
//
// # Two axes, never one
//
// Completeness and Outcome are orthogonal and must stay that way. An
// instrument that folds them together loses exactly the observation it was
// built to make: a run that completed faithfully and reports that no reviewer
// produced a bounded verdict is COMPLETE/UNREVIEWED -- a valid record carrying
// real evidence -- and calling it void would discard a measurement because its
// semantic result was unwelcome. VOID is therefore not an Outcome. It is what a
// reader concludes about an INCOMPLETE receipt, and it is never a value this
// package can emit.
//
// # Re-derivable before attested
//
// Most of what a receipt states can be recomputed later from Git and from file
// digests: commits, trees, first parents, diff digests, binary digests. Those
// fields are RE_DERIVABLE, and a verifier should recompute rather than trust
// them -- a signature over a fact Git already proves is decoration. Only a
// small residue cannot be reconstructed after the run: which process actually
// answered awareness, which executable the reviewer actually was, what the
// verdict said, in what order things happened. Those are OBSERVED, and they are
// the whole trusted surface. Keeping that surface small is the design goal.
//
// # The receipt may report evidence. It may not grant authority to itself.
//
// Nothing here decides admission. A receipt states what happened and whether
// its own record is complete; who may act on that is a question outside this
// package, and TestAReceiptCannotAdmitAnything holds the line.
package runreceipt

import (
	"fmt"
	"sort"
	"strings"
)

// SchemaVersion identifies the shape of an emitted receipt. A reader that does
// not recognise the version reports UNSUPPORTED rather than guessing: a
// receipt parsed under the wrong schema is a fabricated specimen.
// v2 added the PlanState axis and the STOPPED outcome. v3 adds the reviewed
// tree, the canonical diff-digest relation, and redefines CandidateState to
// mean MEASURED WORK rather than "a worktree exists". Each changes what a
// COMPLETE receipt means, so the version moves with them: a reader on the wrong
// version misreads the record, which is the fabricated specimen this comment
// warns about.
const SchemaVersion = "sensei-code.governed-run-receipt/v3"

// Completeness is the instrument axis: does this record contain what a record
// of a governed run must contain?
type Completeness string

const (
	Complete   Completeness = "COMPLETE"
	Incomplete Completeness = "INCOMPLETE"
)

// Outcome is the run axis: what happened. VOID is deliberately absent -- it
// belongs to the other axis, and a reader derives it from INCOMPLETE.
type Outcome string

const (
	// OutcomeAccepted: an independent reviewer returned a bounded acceptance.
	OutcomeAccepted Outcome = "ACCEPTED"
	// OutcomeRefused: a bounded verdict that was not an acceptance.
	OutcomeRefused Outcome = "REFUSED"
	// OutcomeFailed: the run ended without reaching a verdict at all.
	OutcomeFailed Outcome = "FAILED"
	// OutcomeUnreviewed: the run reached its end and no provider produced a
	// bounded verdict. A measured absence, never "clean by exhaustion".
	OutcomeUnreviewed Outcome = "UNREVIEWED"
	// OutcomeDeferred: the run reached a human-owned authority boundary and the
	// human declined to answer. Nothing failed and nobody withdrew -- one
	// question was left standing, and the run ended without touching the
	// candidate or calling another provider.
	//
	// The first real governed run ended this way and emitted NOTHING, because
	// this terminal was classified non-terminal on the grounds that the run is
	// resumable. That is true inside the process model and false outside it:
	// the process exited, and the only account of it was the event stream --
	// the reconstruction this record exists to abolish.
	OutcomeDeferred Outcome = "DEFERRED"
	// OutcomeStopped: a human ended the run. This is a real terminal outcome,
	// not instrument incompleteness and not workflow failure -- recording it as
	// FAILED would teach the behavioural record that this task shape breaks,
	// and recording it as UNKNOWN would leave a known outcome unnamed.
	// COMPLETE / STOPPED is a fully meaningful record.
	OutcomeStopped Outcome = "STOPPED"
	// OutcomeUnknown: the record does not say. This is an admission of
	// ignorance the reader can act on, not a default that hides one.
	OutcomeUnknown Outcome = "UNKNOWN"
)

// Valid reports whether o is a value this schema defines. Membership is read
// by enumeration, never by exclusion: an unrecognised string -- "VOID" above
// all -- is not a new outcome, it is an invalid one.
func (o Outcome) Valid() bool {
	switch o {
	case OutcomeAccepted, OutcomeRefused, OutcomeFailed, OutcomeUnreviewed,
		OutcomeStopped, OutcomeDeferred, OutcomeUnknown:
		return true
	}
	return false
}

// SufficientForComplete reports whether o can appear in a COMPLETE receipt.
// UNKNOWN is representable -- a reader must be able to say the record does not
// state what happened -- but a record that cannot say what happened is not a
// complete record of a governed run.
func (o Outcome) SufficientForComplete() bool { return o.Valid() && o != OutcomeUnknown }

// CandidateState is the machine-readable condition under which candidate
// evidence is required.
//
// C5's third amendment: a conditional artifact needs a condition IN CODE, and
// the condition must be cross-checked rather than trusted. "Required iff a
// candidate exists" is not the same as "required iff the candidate was
// committed", and a receipt that names a candidate while its describing
// evidence is absent is the same fail-open one level up.
type CandidateState string

const (
	// CandidateNone: the run produced no candidate. A read-only run is the
	// ordinary case, and it is a fact, not an absence of one.
	CandidateNone CandidateState = "NONE"
	// CandidatePresent: a candidate exists, so the evidence describing it --
	// commit, tree, first parent, diff digest -- is required.
	CandidatePresent CandidateState = "PRESENT"
	// CandidateUnknown: the record does not say. Never sufficient for COMPLETE.
	CandidateUnknown CandidateState = "UNKNOWN"
)

// Valid reads membership by enumeration, for the same reason Outcome does.
func (c CandidateState) Valid() bool {
	switch c {
	case CandidateNone, CandidatePresent, CandidateUnknown:
		return true
	}
	return false
}

// ReviewDecision is the closed vocabulary a bounded review may return.
//
// It mirrors internal/roles.Decision deliberately rather than importing it: a
// serialization schema that borrows a live internal type changes meaning
// whenever that type does, silently. The two are held equal by
// TestTheReceiptVocabularyMatchesTheReviewerContract, so drift is caught
// mechanically instead of avoided by coupling.
type ReviewDecision string

const (
	DecisionAccept   ReviewDecision = "accept"
	DecisionRevise   ReviewDecision = "revise"
	DecisionEscalate ReviewDecision = "escalate"
)

// Valid reads membership by enumeration. A verdict outside the vocabulary is
// not a new kind of review; it is an invalid record of one.
func (d ReviewDecision) Valid() bool {
	switch d {
	case DecisionAccept, DecisionRevise, DecisionEscalate:
		return true
	}
	return false
}

// DeliveryState is whether one reviewer attempt actually produced a verdict.
//
// It was a raw bool, which escaped the law every other fact obeys: `true`
// needed no source, and `false` could not distinguish "measured: this provider
// did not deliver" from "nobody set this field". It is load-bearing -- a
// bounded outcome is only believable if some attempt delivered -- so it is
// measured like everything else.
type DeliveryState string

const (
	Delivered      DeliveryState = "DELIVERED"
	NotDelivered   DeliveryState = "NOT_DELIVERED"
	DeliveryUnsaid DeliveryState = "UNKNOWN"
)

// Valid reads membership by enumeration.
func (d DeliveryState) Valid() bool {
	switch d {
	case Delivered, NotDelivered, DeliveryUnsaid:
		return true
	}
	return false
}

// DeliveryValue records a measured delivery state with its source.
func DeliveryValue(state DeliveryState, source string) Value {
	return MeasuredValue(string(state), source)
}

// PlanState is the condition under which a plan's identity is required.
//
// The third independent instance of one law: requiredness is conditional
// evidence. A conversational answer genuinely has no plan, and demanding a
// digest for an artifact that does not exist is not rigour, it is the wrong
// predicate.
type PlanState string

const (
	// PlanPresent: the run carried a bound, so that bound must have an identity
	// a later reader can check a candidate against.
	PlanPresent PlanState = "PRESENT"
	// PlanNone: the run carried no plan at all. A positive claim, so the digest
	// must be an explicitly recorded absence.
	PlanNone PlanState = "NONE"
	// PlanUnknown: the record does not say. Never sufficient for COMPLETE.
	PlanUnknown PlanState = "UNKNOWN"
)

// Valid reads membership by enumeration.
func (p PlanState) Valid() bool {
	switch p {
	case PlanPresent, PlanNone, PlanUnknown:
		return true
	}
	return false
}

// DigestRelation says whether the review's representation and the minted
// object's representation agree.
//
// Once C^{tree} == T and C^1 == B, rendering B->C and rendering B->T through
// the SAME renderer are renderings of the same two objects. A difference is
// therefore not "two mechanisms disagreed" -- it means one of the identities we
// call equal is not. DIFFER is a contradiction, not an alternative outcome.
type DigestRelation string

const (
	// RelationMatch: the canonical rendering of the minted object equals the
	// digest the review was given.
	RelationMatch DigestRelation = "MATCH"
	// RelationDiffer: they disagree. The record is internally contradictory.
	RelationDiffer DigestRelation = "DIFFER"
	// RelationUnknown: not measured.
	RelationUnknown DigestRelation = "UNKNOWN"
)

// Valid reads membership by enumeration.
func (d DigestRelation) Valid() bool {
	switch d {
	case RelationMatch, RelationDiffer, RelationUnknown:
		return true
	}
	return false
}

// Knownness is how a single fact stands in the record. Every read of untrusted
// input lands on exactly one of these, so no input shape can crash extraction.
type Knownness string

const (
	// Known: measured, and the value is present.
	Known Knownness = "KNOWN"
	// Unknown: the run did not report it. Absence, stated.
	Unknown Knownness = "UNKNOWN"
	// Malformed: reported, but not in the shape this schema models. The C5
	// defect is exactly this case: payload.provenance arrived as a string
	// where an object was expected, and an assumption crashed instead of a
	// classification being recorded.
	Malformed Knownness = "MALFORMED"
	// Unsupported: a shape from a schema version this reader does not model.
	Unsupported Knownness = "UNSUPPORTED"
)

// Valid reads membership by enumeration. Knownness is an exported string type,
// so Knownness("CERTAIN") is constructible; a state outside the four defined
// ones is invalid, not a fifth kind of knowledge.
func (k Knownness) Valid() bool {
	switch k {
	case Known, Unknown, Malformed, Unsupported:
		return true
	}
	return false
}

// Derivation says whether a verifier can recompute a field or must trust it.
type Derivation string

const (
	// Rederivable: recomputable later from Git or from a file on disk. A
	// verifier that trusts one of these instead of recomputing it is choosing
	// a weaker check than the one available.
	Rederivable Derivation = "RE_DERIVABLE"
	// Observed: only the run could see it. This is the trusted surface.
	Observed Derivation = "OBSERVED"
)

// Value is one recorded fact together with how it stands. Text is meaningful
// only when Known; Detail says why otherwise, so an absence is never mistaken
// for an empty measurement.
//
// Source states HOW the fact was measured, and a Known value without one is
// invalid. This does not make lying impossible -- a caller can write any
// source it likes -- and for re-derivable fields Source is not proof anyway,
// because Verify recomputes those. What it removes is CASUAL promotion: a
// string cannot drift into the record as knowledge without someone writing
// down where it came from.
type Value struct {
	Text   string    `json:"text,omitempty"`
	State  Knownness `json:"state"`
	Source string    `json:"source,omitempty"`
	Detail string    `json:"detail,omitempty"`
}

// MeasuredValue records a fact together with how it was measured. Both are
// required: an empty value is not a measurement, and an unsourced one is an
// assertion wearing a measurement's clothes.
//
// Examples of a source:
//
//	git rev-parse HEAD
//	sha256:/proc/2375957/exe
//	event:agent.role.assigned.payload.provider
func MeasuredValue(text, source string) Value {
	if strings.TrimSpace(text) == "" {
		// An empty string is not a measurement. Saying so here keeps the
		// "label present, value empty" defect from having a hiding place.
		return Value{State: Unknown, Source: source, Detail: "reported empty"}
	}
	if strings.TrimSpace(source) == "" {
		return Value{Text: text, State: Malformed,
			Detail: "claimed as measured with no stated source; that is an assertion, not a measurement"}
	}
	return Value{Text: text, State: Known, Source: source}
}

// UnknownValue records that the run did not report a fact, and why.
func UnknownValue(detail string) Value { return Value{State: Unknown, Detail: detail} }

// MalformedValue records that a fact was reported in a shape this schema does
// not model, and what that shape was.
func MalformedValue(detail string) Value { return Value{State: Malformed, Detail: detail} }

// Attempt is one reviewer attempt. The trail is kept whole because a run in
// which one provider fails and another delivers must record both: recording
// only the provider that answered would say a different provider reviewed than
// the one that did.
type Attempt struct {
	Provider Value `json:"provider"`
	// Delivery carries a DeliveryState, measured like any other fact. UNKNOWN
	// never satisfies a bounded-review requirement.
	Delivery Value `json:"delivery"`
	Verdict  Value `json:"verdict"`
	Digest   Value `json:"reviewed_digest"`
	// Tree is the content this attempt's verdict was bound to. Without it the
	// receipt could name a reviewed tree while the delivered attempt proves only
	// a digest -- the same law, one more axis.
	Tree       Value  `json:"reviewed_tree"`
	Diagnostic string `json:"diagnostic,omitempty"`
}

// DeliveredVerdict reports whether this attempt is MEASURED to have delivered.
// Anything else -- not delivered, unsaid, unmeasured, or a state outside the
// vocabulary -- is not a delivery.
func (a Attempt) DeliveredVerdict() bool {
	return a.Delivery.State == Known && DeliveryState(a.Delivery.Text) == Delivered
}

// Receipt is one governed run's account of itself.
type Receipt struct {
	Schema string `json:"schema"`

	// Re-derivable identities. A verifier recomputes these.
	GovernorCommit       Value `json:"governor_commit"`
	GovernorBinarySHA256 Value `json:"governor_binary_sha256"`
	BaseCommit           Value `json:"base_commit"`
	PlanDigest           Value `json:"plan_digest"`
	GraphDigest          Value `json:"graph_digest"`
	CandidateCommit      Value `json:"candidate_commit"`
	CandidateTree        Value `json:"candidate_tree"`
	CandidateFirstParent Value `json:"candidate_first_parent"`
	CandidateDigest      Value `json:"candidate_digest"`

	// CandidateCommitDiffDigest is the canonical rendering of the MINTED object,
	// digested. It exists to be compared with CandidateDigest.
	CandidateCommitDiffDigest Value `json:"candidate_commit_diff_digest"`
	// CandidateDigestRelation is that comparison. DIFFER is a contradiction.
	CandidateDigestRelation DigestRelation `json:"candidate_digest_relation"`

	// Observed identities. Nothing can reconstruct these afterwards.
	ServingProducer    Value `json:"serving_producer"`
	ReviewerProvider   Value `json:"reviewer_provider"`
	ReviewerExecutable Value `json:"reviewer_executable"`
	ReviewVerdict      Value `json:"review_verdict"`
	ReviewedDigest     Value `json:"reviewed_digest"`
	// DeferredQuestion is the authority question a DEFERRED run left standing.
	//
	// Required when the outcome is DEFERRED: a record saying "a question was
	// deferred" without saying WHICH is the same shape as a candidate that
	// cannot name its own commit.
	DeferredQuestion Value `json:"deferred_question"`

	// ReviewedTree is the content the verdict's envelope named. A receipt that
	// states a candidate tree and a reviewed digest, while proving nothing about
	// whether the verdict was bound to THAT tree, sends a later adjudicator back
	// into the event stream -- the work this record exists to remove.
	ReviewedTree Value `json:"reviewed_tree"`

	// PlanState is the condition that makes the plan's identity required, and
	// it is cross-checked against PlanDigest the way CandidateState is.
	PlanState PlanState `json:"plan_state"`

	// CandidateState is the condition that makes candidate evidence required.
	// It is cross-checked against the candidate fields themselves: a receipt
	// naming a candidate while claiming none is inconsistent, not complete.
	CandidateState CandidateState `json:"candidate_state"`

	// Attempts is the ordered reviewer trail, fallbacks included.
	Attempts []Attempt `json:"attempts,omitempty"`

	// Terminal records whether the run reached a terminal event. A record that
	// stops mid-stream is incomplete however many fields it happens to carry.
	Terminal Value `json:"terminal"`

	// Outcome is the run axis. Never VOID.
	Outcome Outcome `json:"outcome"`

	// Diagnostics carries what the reader could not model: unparseable lines,
	// unrecognised kinds, shapes from another schema. It is evidence about the
	// instrument and must never be silently empty when something was skipped.
	Diagnostics []string `json:"diagnostics,omitempty"`
}

// Field is one receipt fact with its name and derivation, so a verifier can
// iterate the re-derivable set without knowing the struct.
type Field struct {
	Name       string
	Value      Value
	Derivation Derivation
	Required   bool
}

// Fields lists every classified fact, with Required computed FOR THIS RECEIPT.
//
// Requiredness is conditional evidence, not a static boolean attached to a
// field. Candidate evidence is required exactly when a candidate exists;
// reviewer evidence is required exactly when the run claims a bounded verdict.
// A fixed Required flag would let ACCEPTED coexist with no reviewer and
// PRESENT coexist with no candidate tree -- both COMPLETE, both false.
func (r Receipt) Fields() []Field {
	planned := r.PlanState == PlanPresent
	candidate := r.CandidateState == CandidatePresent
	// ACCEPTED and REFUSED both assert that a reviewer returned a bounded
	// verdict. Neither may be said without the evidence that says who, what
	// and about which candidate revision.
	reviewed := r.Outcome == OutcomeAccepted || r.Outcome == OutcomeRefused
	deferred := r.Outcome == OutcomeDeferred
	return []Field{
		{"governor_commit", r.GovernorCommit, Rederivable, true},
		{"governor_binary_sha256", r.GovernorBinarySHA256, Rederivable, true},
		{"base_commit", r.BaseCommit, Rederivable, true},
		{"plan_digest", r.PlanDigest, Rederivable, planned},
		{"graph_digest", r.GraphDigest, Rederivable, true},
		{"candidate_commit", r.CandidateCommit, Rederivable, candidate},
		{"candidate_tree", r.CandidateTree, Rederivable, candidate},
		{"candidate_first_parent", r.CandidateFirstParent, Rederivable, candidate},
		{"candidate_digest", r.CandidateDigest, Rederivable, candidate},
		{"serving_producer", r.ServingProducer, Observed, true},
		{"reviewer_provider", r.ReviewerProvider, Observed, reviewed},
		{"reviewer_executable", r.ReviewerExecutable, Observed, false},
		{"review_verdict", r.ReviewVerdict, Observed, reviewed},
		{"reviewed_digest", r.ReviewedDigest, Observed, reviewed},
		{"reviewed_tree", r.ReviewedTree, Observed, reviewed},
		{"candidate_commit_diff_digest", r.CandidateCommitDiffDigest, Rederivable, candidate},
		{"deferred_question", r.DeferredQuestion, Observed, deferred},
		{"terminal", r.Terminal, Observed, true},
	}
}

// Rederivable returns the fields a verifier must recompute rather than trust.
func (r Receipt) Rederivable() []Field {
	var out []Field
	for _, f := range r.Fields() {
		if f.Derivation == Rederivable {
			out = append(out, f)
		}
	}
	return out
}

// Completeness reports whether the record contains what a governed run's record
// must contain, and names every reason it does not. A missing required field is
// never silently defaulted: it is returned as a reason, and the caller reads
// INCOMPLETE as VOID.
func (r Receipt) Completeness() (Completeness, []string) {
	var missing []string
	if r.Schema != SchemaVersion {
		missing = append(missing, fmt.Sprintf("schema %q is not %q", r.Schema, SchemaVersion))
	}

	// Every value is validated, wherever it lives. An earlier draft walked
	// Fields() only, so a sourceless claim inside a reviewer Attempt passed
	// unnoticed while the docs said such a claim was invalid everywhere.
	for _, f := range r.Fields() {
		missing = append(missing, validateValue(f.Name, f.Value)...)
		if !f.Required {
			continue
		}
		if f.Value.State != Known {
			detail := f.Value.Detail
			if detail == "" {
				detail = string(f.Value.State)
			}
			missing = append(missing, fmt.Sprintf("%s: %s", f.Name, detail))
		}
	}
	for i, a := range r.Attempts {
		p := fmt.Sprintf("attempts[%d]", i)
		missing = append(missing, validateValue(p+".provider", a.Provider)...)
		missing = append(missing, validateValue(p+".delivery", a.Delivery)...)
		missing = append(missing, validateValue(p+".verdict", a.Verdict)...)
		missing = append(missing, validateValue(p+".reviewed_digest", a.Digest)...)
		if a.Delivery.State == Known && !DeliveryState(a.Delivery.Text).Valid() {
			missing = append(missing, fmt.Sprintf("%s.delivery %q is not a value this schema defines", p, a.Delivery.Text))
		}
		missing = append(missing, validateValue(p+".reviewed_tree", a.Tree)...)
		if a.Verdict.State == Known && !ReviewDecision(a.Verdict.Text).Valid() {
			missing = append(missing, fmt.Sprintf("%s.verdict %q is outside the closed review vocabulary", p, a.Verdict.Text))
		}
	}

	// The plan axis, and its cross-check.
	switch {
	case !r.PlanState.Valid():
		missing = append(missing, fmt.Sprintf("plan_state %q is not a value this schema defines", r.PlanState))
	case r.PlanState == PlanUnknown:
		missing = append(missing, "plan_state UNKNOWN: a record that cannot say whether a plan governed the run is not complete")
	case r.PlanState == PlanNone && r.PlanDigest.State != Unknown:
		missing = append(missing, fmt.Sprintf(
			"plan_digest is %s while plan_state is NONE: claiming no plan requires the absence to be recorded", r.PlanDigest.State))
	}

	// The candidate axis, and its cross-check. A receipt that names a candidate
	// while claiming none is inconsistent: the condition must agree with the
	// evidence, not merely accompany it.
	switch {
	case !r.CandidateState.Valid():
		missing = append(missing, fmt.Sprintf("candidate_state %q is not a value this schema defines", r.CandidateState))
	case r.CandidateState == CandidateUnknown:
		missing = append(missing, "candidate_state UNKNOWN: a record that cannot say whether a candidate exists is not complete")
	case r.CandidateState == CandidateNone:
		// NONE is a POSITIVE claim that no candidate exists, so every piece of
		// candidate evidence must be an explicitly recorded absence. MALFORMED
		// or UNSUPPORTED means the instrument could not read the thing it is
		// claiming does not exist, and it may not make that claim.
		for _, f := range r.Fields() {
			if !strings.HasPrefix(f.Name, "candidate_") {
				continue
			}
			switch f.Value.State {
			case Unknown:
				// The absence is recorded, which is what NONE asserts.
			case Known:
				missing = append(missing, fmt.Sprintf(
					"%s is measured while candidate_state is NONE: the condition contradicts the evidence", f.Name))
			default:
				missing = append(missing, fmt.Sprintf(
					"%s is %s while candidate_state is NONE: claiming no candidate requires the absence to be recorded, not unreadable",
					f.Name, f.Value.State))
			}
		}
	}

	// The run axis, and its cross-check.
	switch {
	case r.Outcome == "":
		missing = append(missing, "outcome: absent")
	case !r.Outcome.Valid():
		missing = append(missing, fmt.Sprintf("outcome %q is not a value this schema defines", r.Outcome))
	case !r.Outcome.SufficientForComplete():
		missing = append(missing, "outcome UNKNOWN: a record that cannot say what happened is not complete")
	case r.Outcome == OutcomeAccepted || r.Outcome == OutcomeRefused:
		missing = append(missing, r.checkBoundedReview()...)
	case r.Outcome == OutcomeDeferred && r.ReviewVerdict.State == Known:
		missing = append(missing, "outcome DEFERRED while a bounded verdict is recorded: a run that stopped at an authority boundary did not also get judged")
	case r.Outcome == OutcomeUnreviewed && r.ReviewVerdict.State == Known:
		missing = append(missing, "outcome UNREVIEWED while a bounded verdict is recorded: the condition contradicts the evidence")
	}

	// The canonical rendering relation. Once C^{tree} == T and C^1 == B, a
	// DIFFER is an internal contradiction rather than a tolerable outcome, and
	// UNKNOWN means nobody looked.
	if r.CandidateState == CandidatePresent {
		switch {
		case !r.CandidateDigestRelation.Valid():
			missing = append(missing, fmt.Sprintf("candidate_digest_relation %q is not a value this schema defines", r.CandidateDigestRelation))
		case r.CandidateDigestRelation == RelationUnknown:
			missing = append(missing, "candidate_digest_relation UNKNOWN: the minted object's rendering was never compared with the reviewed one")
		case r.CandidateDigestRelation == RelationDiffer:
			missing = append(missing,
				"candidate_digest_relation DIFFER: the minted object renders differently from what the review was given, so two identities this record calls equal are not")
		}
	}

	// The reviewed tree and the candidate tree are the same content or the
	// record is claiming an identity it did not establish.
	if r.ReviewedTree.State == Known && r.CandidateTree.State == Known &&
		r.ReviewedTree.Text != r.CandidateTree.Text {
		missing = append(missing, fmt.Sprintf(
			"candidate_tree %s is not the reviewed_tree %s: the object minted is not the content the verdict was bound to",
			shortSHA(r.CandidateTree.Text), shortSHA(r.ReviewedTree.Text)))
	}

	if len(missing) > 0 {
		sort.Strings(missing)
		return Incomplete, missing
	}
	return Complete, nil
}

func shortSHA(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// checkBoundedReview holds the review binding.
//
// An ACCEPTED or REFUSED receipt asserts that a named reviewer returned a
// particular decision about a particular candidate revision. It is not enough
// that SOME attempt delivered and that the top-level fields are populated: one
// measured fact must not support a different claim. The delivering attempt has
// to be THE one the receipt is talking about.
func (r Receipt) checkBoundedReview() []string {
	var missing []string
	decision := ReviewDecision(r.ReviewVerdict.Text)
	if r.ReviewVerdict.State == Known {
		if !decision.Valid() {
			missing = append(missing, fmt.Sprintf("review_verdict %q is outside the closed review vocabulary (accept, revise, escalate)", r.ReviewVerdict.Text))
		} else {
			switch {
			case r.Outcome == OutcomeAccepted && decision != DecisionAccept:
				missing = append(missing, fmt.Sprintf("outcome ACCEPTED while the review decision is %q", decision))
			case r.Outcome == OutcomeRefused && decision == DecisionAccept:
				missing = append(missing, "outcome REFUSED while the review decision is \"accept\"")
			}
		}
	}
	bound := false
	for _, a := range r.Attempts {
		if !a.DeliveredVerdict() {
			continue
		}
		if a.Provider.Text == r.ReviewerProvider.Text &&
			a.Verdict.Text == r.ReviewVerdict.Text &&
			a.Digest.Text == r.ReviewedDigest.Text &&
			a.Tree.Text == r.ReviewedTree.Text {
			bound = true
		}
	}
	if !bound {
		missing = append(missing, fmt.Sprintf(
			"outcome %s claims a bounded verdict, but no DELIVERED attempt carries the same provider, decision, reviewed digest and reviewed tree as the receipt",
			r.Outcome))
	}
	return missing
}

// validateValue holds the two rules every recorded fact obeys, wherever it
// appears: its state must be one this schema defines, and a claim of knowledge
// must say how it was measured.
func validateValue(name string, v Value) []string {
	var bad []string
	if !v.State.Valid() {
		bad = append(bad, fmt.Sprintf("%s: state %q is not a value this schema defines", name, v.State))
		return bad
	}
	if v.State == Known && strings.TrimSpace(v.Source) == "" {
		bad = append(bad, name+": claimed as measured with no stated source")
	}
	return bad
}

// MismatchKind is why a re-derivable fact failed verification. All of these
// fail; they are kept distinct because they are epistemically different, and a
// consumer should switch on the state rather than parse English prose.
type MismatchKind string

const (
	// MismatchDisagreement: recomputed, and it disagrees.
	MismatchDisagreement MismatchKind = "DISAGREEMENT"
	// MismatchUnrecomputable: the verifier could not measure it. Silence from
	// a verifier is not agreement.
	MismatchUnrecomputable MismatchKind = "UNRECOMPUTABLE"
	// MismatchRecordedUnknown: a required fact the record never measured.
	MismatchRecordedUnknown MismatchKind = "RECORDED_UNKNOWN"
	// MismatchRecordedMalformed: reported in a shape the schema does not model.
	MismatchRecordedMalformed MismatchKind = "RECORDED_MALFORMED"
	// MismatchRecordedUnsupported: reported under a schema version this reader
	// does not model. Collapsing it into RECORDED_UNKNOWN would reintroduce the
	// prose-parsing the typed kinds exist to remove.
	MismatchRecordedUnsupported MismatchKind = "RECORDED_UNSUPPORTED"
)

// Mismatch is a re-derivable fact that did not survive verification.
type Mismatch struct {
	Kind       MismatchKind
	Field      string
	Recorded   string
	Recomputed string
	Detail     string
}

// Verify recomputes every re-derivable field through recompute and reports the
// disagreements. recompute returns the measured value and whether it could
// measure at all; a field it cannot measure is reported rather than passed,
// because silence from a verifier is not agreement.
func (r Receipt) Verify(recompute func(field string) (string, bool)) []Mismatch {
	var out []Mismatch
	for _, f := range r.Rederivable() {
		if f.Value.State != Known {
			if f.Required {
				kind := MismatchRecordedUnknown
				switch f.Value.State {
				case Malformed:
					kind = MismatchRecordedMalformed
				case Unsupported:
					kind = MismatchRecordedUnsupported
				}
				out = append(out, Mismatch{Kind: kind, Field: f.Name, Detail: f.Value.Detail})
			}
			continue
		}
		got, ok := recompute(f.Name)
		if !ok {
			out = append(out, Mismatch{Kind: MismatchUnrecomputable, Field: f.Name, Recorded: f.Value.Text,
				Detail: "the verifier could not measure this field; silence is not agreement"})
			continue
		}
		if got != f.Value.Text {
			out = append(out, Mismatch{Kind: MismatchDisagreement, Field: f.Name, Recorded: f.Value.Text, Recomputed: got})
		}
	}
	return out
}
