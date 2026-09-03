package principal

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/globulario/sensei-code/internal/roles"
)

// A remote principal may hold both the architect and the reviewer role. It may
// exercise both. What it cannot do is turn holding both into independence — and
// what it also cannot do is turn holding a DIFFERENT NAME into independence.
//
// The failure this types out is already governed knowledge in this repository:
// invariant sensei_code.roles.adversarial_roles_inherit_nothing_and_bind_to_a_revision,
// failure mode sensei_code.review_forked_the_conversation_it_was_judging, and
// forbidden fix sensei_code.give_an_adversarial_role_the_architects_thread_for_context.
// A reviewer that has already read the argument for why the work is right — in
// its own words, because it wrote them — agrees more often than one that came
// to the artifact cold, and the agreement is worth nothing while being
// indistinguishable from the real thing in the transcript.
//
// Sensei Code can force a LOCAL adversarial role into a fresh session, because
// it launches the process. It cannot do that to a remote principal: the
// conversation is on the far side of the transport, and a client asserting that
// it started fresh is making exactly the claim that would need checking.
//
// The subtler half, and the one this file got wrong first: a remote client also
// supplies its own principal identity. So "architect = agent-a, reviewer =
// agent-b" is two labels chosen by one party, and reading independence out of
// them recreates the forbidden fix one layer down — the client self-asserting
// the fact that is supposed to make its review worth something. Two stated names
// are ATTRIBUTION. Independence is a property of the reviewing CONTEXT, and only
// a mechanism this side of the transport can establish it.
//
// So the response is not to relax the invariant and not to refuse the role. It
// is to type the review, and to keep the two questions apart:
//
//	who reviewed        -> Attribution, taken from what was stated
//	what it is worth    -> ReviewKind, taken from proof or withheld
//
// Nothing in this file can grant anything. ObligationUnmet only ever withholds.

// Attribution is who reviewed relative to who architected. It is taken from
// stated identities and it establishes nothing on its own — it is recorded
// because knowing which party did what is genuinely useful, and because the
// useful part must not be mistaken for the load-bearing part.
type Attribution string

const (
	// Unattributed means one of the two parties was never identified.
	Unattributed Attribution = "unattributed"
	// SameParty means one principal architected and reviewed.
	SameParty Attribution = "same_principal"
	// DistinctParties means two different identities were stated. Both may
	// still be one agent with two labels; nothing here can tell.
	DistinctParties Attribution = "distinct_principals"
)

// AllAttributions is the closed set.
var AllAttributions = []Attribution{Unattributed, SameParty, DistinctParties}

func (a Attribution) Valid() bool {
	for _, known := range AllAttributions {
		if a == known {
			return true
		}
	}
	return false
}

func (a Attribution) String() string { return string(a) }

// ReviewKind is what a review is worth as evidence, which is a different
// question from who gave it and a different question from what it says.
type ReviewKind string

const (
	// IndependentReview is a review whose isolation from the work it judges has
	// been PROVEN by a mechanism this side of the transport. It is reachable
	// only through IndependenceProof.
	//
	// It is NECESSARY and not SUFFICIENT. Independence of the architecture is
	// not independence of the implementation: whether the reviewer is also the
	// provider that wrote the candidate is a separate question, answered by
	// roles.ReviewVerdict.Validate and roles.Policy.Check, and neither this
	// value nor those checks can stand in for the other.
	IndependentReview ReviewKind = "independent"
	// AdvisoryReview is every review whose isolation has not been proven —
	// whether because the same principal architected and reviewed, or because
	// two different names were stated and nothing established that they are two
	// different contexts. It is a real review with real findings and no
	// evidentiary standing.
	AdvisoryReview ReviewKind = "advisory"
)

// AllReviewKinds is the closed set.
var AllReviewKinds = []ReviewKind{IndependentReview, AdvisoryReview}

func (k ReviewKind) Valid() bool {
	for _, known := range AllReviewKinds {
		if k == known {
			return true
		}
	}
	return false
}

func (k ReviewKind) String() string { return string(k) }

// Mechanism is a way independence can actually be established. The vocabulary
// is closed and every member of it is something SENSEI CODE does, never
// something a reviewed party reports about itself.
//
// There is exactly one member today, and no member that a remote principal can
// satisfy. That is the honest state of the world: this repository can isolate a
// process it launches and it cannot isolate a conversation it did not open.
// Adding a member for a remote transport is a deliberate, reviewable act, and
// it may only be done once that transport can genuinely produce the isolation —
// not once it can report it.
type Mechanism string

const (
	// LaunchedSession is a provider session Sensei Code started itself, in a
	// process it configured, whose session mode it observed rather than
	// received.
	LaunchedSession Mechanism = "launched_session"
)

// AllMechanisms is the closed set.
var AllMechanisms = []Mechanism{LaunchedSession}

func (m Mechanism) Valid() bool {
	for _, known := range AllMechanisms {
		if m == known {
			return true
		}
	}
	return false
}

func (m Mechanism) String() string { return string(m) }

// IndependenceProof is evidence that a reviewing party occupied a context
// isolated from the work it judges.
//
// Its fields are unexported and Prove is its only constructor, so it cannot
// arrive from a request, be decoded from a payload, or be assembled by a caller
// that would like the answer to be yes. A remote client cannot obtain one:
// there is no mechanism it can satisfy, which is the point.
type IndependenceProof struct {
	reviewer  ID
	mechanism Mechanism
	provider  string
	at        time.Time
}

// Prove mints independence evidence from an artifact Sensei Code stamped.
//
// The provenance argument must be one THIS side produced. roles.Provenance
// carries SessionMode, and SessionMode is meaningful exactly when the party
// that wrote it observed the session rather than being told about it —
// agent.CLI returns the mode the turn actually ran in. A rendezvous runner
// answering a remote client has no such observation, and must not stamp one:
// there is nothing it could have observed.
func Prove(reviewer ID, m Mechanism, p roles.Provenance) (IndependenceProof, error) {
	if !reviewer.Known() {
		return IndependenceProof{}, errors.New("independence cannot be proven for an unidentified reviewer")
	}
	if !m.Valid() {
		return IndependenceProof{}, fmt.Errorf(
			"%q is not a mechanism that establishes independence in this repository", m)
	}
	if p.Role != roles.Reviewer {
		return IndependenceProof{}, fmt.Errorf(
			"independence was offered from a %s artifact, and only a review can prove a reviewer's isolation", p.Role)
	}
	if p.Provider == "" {
		return IndependenceProof{}, errors.New("independence was offered without naming the provider that ran the session")
	}
	if !p.Independent() {
		return IndependenceProof{}, errors.New("the session this offers as proof inherited the work it judges")
	}
	return IndependenceProof{reviewer: reviewer, mechanism: m, provider: p.Provider, at: p.At}, nil
}

// Proves reports whether this evidence is about the named reviewer. A proof
// that names somebody else proves nothing about this one.
func (p IndependenceProof) Proves(reviewer ID) bool {
	return p.mechanism.Valid() && reviewer.Known() && p.reviewer == reviewer
}

// Mechanism is how independence was established, for the record.
func (p IndependenceProof) Mechanism() Mechanism { return p.mechanism }

// Qualification is what one principal's review of one task is worth.
//
// Its fields are unexported and its only constructors are Qualify and
// QualifyProven. That is the whole design: a struct another package could
// assemble would eventually be assembled with the convenient answer, by a
// caller in a hurry, and the convenient answer here is "independent". The zero
// value is advisory, so a Qualification nobody filled in claims nothing.
type Qualification struct {
	kind        ReviewKind
	attribution Attribution
	architect   ID
	reviewer    ID
	reason      string
	mechanism   Mechanism
}

// Qualify records who reviewed a task whose architecture was authored by
// architect, and withholds any claim about what the review is worth.
//
// It NEVER returns IndependentReview, including when the two identities differ.
// Both identities are stated by the party being judged, so two different names
// are one client's labelling choice until something on this side establishes
// that they are two different contexts. Use QualifyProven when there is proof.
func Qualify(architect, reviewer ID) Qualification {
	q := Qualification{kind: AdvisoryReview, architect: architect, reviewer: reviewer}
	switch {
	case !reviewer.Known():
		q.attribution = Unattributed
		q.reason = "the reviewing principal is not identified, so this review is attributable to nobody"
	case !architect.Known():
		q.attribution = Unattributed
		q.reason = "no principal is recorded as the author of this task's architecture, so this review cannot be shown to have come from anywhere else"
	case architect == reviewer:
		q.attribution = SameParty
		q.reason = string(reviewer) + " authored this task's architecture and is reviewing work produced under it"
	default:
		q.attribution = DistinctParties
		q.reason = string(reviewer) + " is a different stated identity from the author of this task's architecture (" +
			string(architect) + "), which is attribution and not proof that the two are separate contexts"
	}
	return q
}

// QualifyProven is Qualify with evidence that the reviewer was isolated.
//
// Every condition must hold: the identities must actually differ, and the proof
// must be about this reviewer. A proof cannot rescue a same-principal review —
// a party cannot be isolated from itself, so a proof offered for one is
// evidence that the proof is wrong.
func QualifyProven(architect, reviewer ID, proof IndependenceProof) Qualification {
	q := Qualify(architect, reviewer)
	if q.attribution != DistinctParties {
		return q
	}
	if !proof.Proves(reviewer) {
		return q
	}
	q.kind = IndependentReview
	q.mechanism = proof.mechanism
	q.reason = string(reviewer) + " reviewed in a context isolated from the architecture it judges, established by " +
		string(proof.mechanism) + " (" + proof.provider + ")"
	return q
}

// Kind is what this review is worth. Any value other than IndependentReview
// reads as advisory, so a Qualification that was never constructed properly
// cannot present itself as the stronger one.
func (q Qualification) Kind() ReviewKind {
	if q.kind == IndependentReview {
		return IndependentReview
	}
	return AdvisoryReview
}

// Independent reports whether the reviewer's isolation from the architecture
// was PROVEN. It says nothing about whether the reviewer is independent of the
// implementer.
func (q Qualification) Independent() bool { return q.Kind() == IndependentReview }

// Attribution is who reviewed relative to who architected. Distinct parties is
// useful to know and is not independence; read Kind for that.
func (q Qualification) Attribution() Attribution {
	if q.attribution.Valid() {
		return q.attribution
	}
	return Unattributed
}

// Architect and Reviewer are who this qualification is about.
func (q Qualification) Architect() ID { return q.architect }
func (q Qualification) Reviewer() ID  { return q.reviewer }

// Mechanism is what established independence, empty when nothing did.
func (q Qualification) Mechanism() Mechanism {
	if q.Independent() {
		return q.mechanism
	}
	return ""
}

// Reason is why, in words a person reading the run record can act on.
func (q Qualification) Reason() string { return q.reason }

// ErrAdversarialObligationUnmet is the refusal a caller must not route around.
var ErrAdversarialObligationUnmet = errors.New("this task requires an independent review and has not had one")

// ObligationUnmet names the review obligation this qualification leaves open,
// or nil when it leaves none.
//
// Read the nil carefully: it is not a statement that the task's review
// obligation is SATISFIED. This function knows one thing — whether the
// reviewer's isolation from the architecture was proven — and a task's
// obligation also requires that the reviewer is not the provider that
// implemented the candidate, which roles.Policy.Check and
// roles.ReviewVerdict.Validate answer separately. A method that could return
// "satisfied" would be read as permission by the first caller in a hurry, so
// this one can only ever withhold.
func (q Qualification) ObligationUnmet(p roles.Policy) error {
	if !p.CrossProviderReview {
		return nil
	}
	if q.Independent() {
		return nil
	}
	return fmt.Errorf("%w: %s. Reason the task requires one: %s",
		ErrAdversarialObligationUnmet, q.reason, p.Reason)
}

// Statement is the line the run record carries, written so it cannot be read as
// more than it is.
func (q Qualification) Statement() string {
	if q.Independent() {
		return "review kind: independent, established by " + string(q.mechanism) + " — " + q.reason +
			"; independence from the implementing provider is a separate check"
	}
	return "review kind: advisory (" + string(q.Attribution()) + ") — " + q.reason +
		"; it carries findings and satisfies no adversarial-review obligation, and it is not admission"
}

// MarshalJSON renders the qualification with independent_review DERIVED from
// the kind rather than stored beside it.
//
// Two fields holding one fact eventually disagree, and the disagreement is
// resolved silently in favour of whichever one the reader happened to consult.
// Here there is one fact and two renderings of it, and the rendering cannot
// contradict the fact because it is computed from it at the moment of writing.
// attribution is rendered alongside precisely so a reader can see that two
// distinct principals did NOT make the review independent.
func (q Qualification) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Kind              ReviewKind  `json:"review_kind"`
		IndependentReview bool        `json:"independent_review"`
		Attribution       Attribution `json:"attribution"`
		Mechanism         Mechanism   `json:"independence_mechanism,omitempty"`
		Architect         ID          `json:"architect_principal,omitempty"`
		Reviewer          ID          `json:"reviewer_principal,omitempty"`
		Reason            string      `json:"reason,omitempty"`
	}{
		Kind:              q.Kind(),
		IndependentReview: q.Independent(),
		Attribution:       q.Attribution(),
		Mechanism:         q.Mechanism(),
		Architect:         q.architect,
		Reviewer:          q.reviewer,
		Reason:            q.reason,
	})
}
