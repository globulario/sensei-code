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
const SchemaVersion = "sensei-code.governed-run-receipt/v1"

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
	// OutcomeUnknown: the record does not say. This is an admission of
	// ignorance the reader can act on, not a default that hides one.
	OutcomeUnknown Outcome = "UNKNOWN"
)

// Valid reports whether o is a value this schema defines. Membership is read
// by enumeration, never by exclusion: an unrecognised string -- "VOID" above
// all -- is not a new outcome, it is an invalid one.
func (o Outcome) Valid() bool {
	switch o {
	case OutcomeAccepted, OutcomeRefused, OutcomeFailed, OutcomeUnreviewed, OutcomeUnknown:
		return true
	}
	return false
}

// SufficientForComplete reports whether o can appear in a COMPLETE receipt.
// UNKNOWN is representable -- a reader must be able to say the record does not
// state what happened -- but a record that cannot say what happened is not a
// complete record of a governed run.
func (o Outcome) SufficientForComplete() bool { return o.Valid() && o != OutcomeUnknown }

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
	Provider   Value  `json:"provider"`
	Delivered  bool   `json:"delivered"`
	Verdict    Value  `json:"verdict"`
	Digest     Value  `json:"reviewed_digest"`
	Diagnostic string `json:"diagnostic,omitempty"`
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

	// Observed identities. Nothing can reconstruct these afterwards.
	ServingProducer    Value `json:"serving_producer"`
	ReviewerProvider   Value `json:"reviewer_provider"`
	ReviewerExecutable Value `json:"reviewer_executable"`
	ReviewVerdict      Value `json:"review_verdict"`
	ReviewedDigest     Value `json:"reviewed_digest"`

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

// Fields lists every classified fact. Required marks what a complete record of
// a governed run must contain; reviewer facts are deliberately NOT required,
// because COMPLETE/UNREVIEWED is a truthful record, not a broken one.
func (r Receipt) Fields() []Field {
	return []Field{
		{"governor_commit", r.GovernorCommit, Rederivable, true},
		{"governor_binary_sha256", r.GovernorBinarySHA256, Rederivable, true},
		{"base_commit", r.BaseCommit, Rederivable, true},
		{"plan_digest", r.PlanDigest, Rederivable, true},
		{"graph_digest", r.GraphDigest, Rederivable, true},
		{"candidate_commit", r.CandidateCommit, Rederivable, false},
		{"candidate_tree", r.CandidateTree, Rederivable, false},
		{"candidate_first_parent", r.CandidateFirstParent, Rederivable, false},
		{"candidate_digest", r.CandidateDigest, Rederivable, false},
		{"serving_producer", r.ServingProducer, Observed, true},
		{"reviewer_provider", r.ReviewerProvider, Observed, false},
		{"reviewer_executable", r.ReviewerExecutable, Observed, false},
		{"review_verdict", r.ReviewVerdict, Observed, false},
		{"reviewed_digest", r.ReviewedDigest, Observed, false},
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
	for _, f := range r.Fields() {
		// A claim of knowledge with no stated source is invalid wherever it
		// appears, required or not.
		if f.Value.State == Known && strings.TrimSpace(f.Value.Source) == "" {
			missing = append(missing, f.Name+": claimed as measured with no stated source")
		}
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
	switch {
	case r.Outcome == "":
		missing = append(missing, "outcome: absent")
	case !r.Outcome.Valid():
		missing = append(missing, fmt.Sprintf("outcome %q is not a value this schema defines", r.Outcome))
	case !r.Outcome.SufficientForComplete():
		missing = append(missing, "outcome UNKNOWN: a record that cannot say what happened is not complete")
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return Incomplete, missing
	}
	return Complete, nil
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
				if f.Value.State == Malformed {
					kind = MismatchRecordedMalformed
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
