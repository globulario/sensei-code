package roles

import (
	"fmt"
	"strings"
)

// A packet is the context one role receives. There are several types rather
// than one because the roles are asymmetric, and a single packet handed to
// everybody is the same question asked three times wearing different hats.
//
// What each type leaves out is as load-bearing as what it carries.
// IndependentReviewPacket has no field for the worker's reasoning, and that is
// not an oversight to be corrected later: a reviewer told why the author
// believes the change is right is reviewing the argument, and the argument is
// the most persuasive and least reliable evidence in the run. It gets the
// governing facts and the artifact, and forms its own view of both.

// ArchitectPacket is the conversation the human is actually having, plus what
// Sensei can vouch for right now.
type ArchitectPacket struct {
	Provenance Provenance `json:"provenance"`
	// Conversation is the dialogue so far. The architect is the one role that
	// legitimately holds it: continuity here is the product, not a leak.
	Conversation       string `json:"conversation,omitempty"`
	Task               string `json:"task"`
	WorkspaceAuthority string `json:"workspace_authority,omitempty"`
	Preflight          string `json:"preflight,omitempty"`
}

// WorkerHandoffPacket is what survives a change of implementer.
//
// It carries state, not transcript. The next worker does not need to know what
// the last one said; it needs to know what is true — which task, which base,
// what the contract bounds, what a human has already settled, what the
// candidate currently holds, and which findings nobody has answered. A
// paragraph is a poor container for those, and a worker that has to re-derive
// them re-derives them slightly differently, which is exactly how the second
// worker undoes the first one's fix.
type WorkerHandoffPacket struct {
	Provenance     Provenance `json:"provenance"`
	PreviousWorker string     `json:"previous_worker,omitempty"`
	// State is the rendered task position: identity, contract, settled
	// decisions and current evidence.
	State string `json:"state"`
	// OpenFindings are review findings the previous worker did not answer. They
	// enter the next worker's first cycle as unanswered feedback, because that
	// is what they are.
	OpenFindings  []Finding `json:"open_findings,omitempty"`
	CyclesUsed    int       `json:"cycles_used,omitempty"`
	CyclesAllowed int       `json:"cycles_allowed,omitempty"`
}

// Continuity reports whether this handoff actually preserves the task, rather
// than restarting it under the same name. A handoff that names a different task
// or a different base is a new candidate wearing the old one's identity.
func (p WorkerHandoffPacket) Continuity(b Binding) error {
	if strings.TrimSpace(p.State) == "" {
		return fmt.Errorf("handoff carries no task state, so the next worker would start over")
	}
	// The digest is deliberately not compared. The whole purpose of a handoff is
	// that the next worker changes the candidate, so binding one to a revision
	// would make every handoff stale on arrival.
	return Binding{TaskID: b.TaskID, BaseSHA: b.BaseSHA}.Verify(p.Provenance)
}

// Render is the handoff as the next worker reads it.
func (p WorkerHandoffPacket) Render() string {
	var b strings.Builder
	b.WriteString(p.State)
	if len(p.OpenFindings) != 0 {
		b.WriteString("\n\nFINDINGS NOBODY HAS ANSWERED YET\n")
		for _, f := range p.OpenFindings {
			b.WriteString("  - " + f.Line() + "\n")
		}
	}
	if p.CyclesAllowed > 0 {
		b.WriteString(fmt.Sprintf("\nBUDGET: %d of %d review cycles were used before this handoff.\n", p.CyclesUsed, p.CyclesAllowed))
	}
	return strings.TrimRight(b.String(), "\n")
}

// IndependentReviewPacket is the candidate and the facts that govern it.
//
// There is no worker-transcript field, and adding one would defeat the reason
// the reviewer runs in its own session at all.
type IndependentReviewPacket struct {
	Provenance Provenance `json:"provenance"`
	Task       string     `json:"task"`
	// Plan is the bound the candidate must stay inside. It is the architect's
	// output, not the worker's: a reviewer must know what was authorised in
	// order to notice work that exceeds it.
	Plan string `json:"plan,omitempty"`
	// Conversation is what the human asked for. It explains the subject and
	// never lowers the standard of proof.
	Conversation       string `json:"conversation,omitempty"`
	ArchitectIntent    string `json:"architect_intent,omitempty"`
	WorkspaceAuthority string `json:"workspace_authority,omitempty"`
	Preflight          string `json:"preflight,omitempty"`
	// Audit is Sensei's verdict on the diff, and Validation is the record of
	// checks the broker executed. Both are evidence rather than claims: they
	// were produced by running things, not by an agent reporting that it ran
	// things.
	Audit      string `json:"audit,omitempty"`
	Validation string `json:"validation,omitempty"`
	Diff       string `json:"diff"`
	// Report is the artifact of a read-only plan: findings, not a change.
	//
	// It exists because such a plan has nothing in Diff, and an empty Diff read
	// as "nothing to object to" would make every inspection self-accepting. A
	// packet carrying a Report is asking whether the findings are supported, and
	// that is a different question from whether a change is safe -- but it is
	// still a question a second agent has to answer, which is the whole reason
	// the reviewer is independent.
	Report string `json:"report,omitempty"`
}

// Inspection reports whether this packet is about findings rather than a change.
func (p IndependentReviewPacket) Inspection() bool {
	return strings.TrimSpace(p.Report) != "" && strings.TrimSpace(p.Diff) == ""
}
