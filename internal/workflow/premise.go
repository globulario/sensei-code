package workflow

import (
	"fmt"
	"strings"
)

// A premise receipt is the engine's identity for one bounded knowledge gap,
// carried through the closure loop (sensei-code#97).
//
// The router classifies a gap and locates it (GapIdentity), but neither the
// class nor the location is the question: two unrelated premises about one
// file share both, and one premise re-stated as a symbol instead of a path
// shares neither. What identifies the question mechanically is the closure
// round itself. The engine issues a receipt when it spends a round on a
// premise, the round is required to answer that receipt, and whether the
// next plan's premise is the same question is read from that answer and the
// round's lineage -- never from the prose.
type premiseReceipt struct {
	ID       string
	Gap      GapIdentity
	Wordings []string
	// Outcome is what the closure round reported for this receipt:
	// established, refuted, or unresolved. Empty means no round has answered
	// yet. A receipt is open until it is established or refuted.
	Outcome string
}

// PremiseResolution is the closure round's answer to a receipt it was asked
// about. It is authored by the architect but keyed by an engine-issued ID, so
// what the model chooses is the outcome, not the identity.
type PremiseResolution struct {
	Gap      string `json:"gap"`
	Outcome  string `json:"outcome"` // established | refuted | unresolved
	Evidence string `json:"evidence,omitempty"`
}

const (
	premiseEstablished = "established"
	premiseRefuted     = "refuted"
	premiseUnresolved  = "unresolved"
)

func (r *premiseReceipt) open() bool {
	return r.Outcome != premiseEstablished && r.Outcome != premiseRefuted
}

// applyPremiseResolutions records the closure round's answers. An answer
// naming a receipt this task never issued is ignored: the model cannot close
// a question nobody asked. An outcome outside the closed vocabulary is read
// as unresolved, the fail-closed reading.
func (e *Engine) applyPremiseResolutions(taskID string, resolutions []PremiseResolution) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, res := range resolutions {
		for _, r := range e.premises[taskID] {
			if r.ID != strings.TrimSpace(res.Gap) {
				continue
			}
			switch strings.ToLower(strings.TrimSpace(res.Outcome)) {
			case premiseEstablished:
				r.Outcome = premiseEstablished
			case premiseRefuted:
				r.Outcome = premiseRefuted
			default:
				r.Outcome = premiseUnresolved
			}
		}
	}
}

// premiseReceiptFor returns the receipt a closing route continues, issuing a
// new one when it continues none. The receipt's ID is what the closure
// budget is spent against.
//
// A route continues an existing receipt when:
//   - the claim that produced it references the receipt by ID (claimRef); or
//   - a receipt is open and unresolved after its round, and this route is the
//     residue of that round: same kind, scope and world, and the same subject
//     or no located subject at all. An unanswered question about a place does
//     not fund a new question about the same place, and a premise that moved
//     from a path to a symbol is still the premise the round did not settle.
//
// A receipt the round answered -- established or refuted -- is closed, so a
// later premise about the same file is a different question with its own
// round. That is the falsifier this design must pass, and the reason the
// answer is required rather than inferred.
func (e *Engine) premiseReceiptFor(taskID string, routing Routing, claimRef string) *premiseReceipt {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.premises == nil {
		e.premises = map[string][]*premiseReceipt{}
	}
	receipts := e.premises[taskID]
	if ref := strings.TrimSpace(claimRef); ref != "" {
		for _, r := range receipts {
			if r.ID == ref {
				r.Wordings = appendWording(r.Wordings, routing.Condition)
				return r
			}
		}
	}
	if routing.Gap.Identified() {
		for _, r := range receipts {
			if !r.open() || r.Outcome != premiseUnresolved {
				continue
			}
			if r.Gap.Kind != routing.Gap.Kind || r.Gap.World != routing.Gap.World || (GapIdentity{Scope: r.Gap.Scope}).Key() != (GapIdentity{Scope: routing.Gap.Scope}).Key() {
				continue
			}
			if routing.Gap.Subject == r.Gap.Subject || routing.Gap.Subject == "" {
				r.Wordings = appendWording(r.Wordings, routing.Condition)
				return r
			}
		}
	}
	r := &premiseReceipt{
		ID:       fmt.Sprintf("gap-%s-%d", strings.TrimPrefix(taskID, "task-"), len(receipts)+1),
		Gap:      routing.Gap,
		Wordings: []string{routing.Condition},
	}
	e.premises[taskID] = append(receipts, r)
	return r
}

func appendWording(ws []string, w string) []string {
	for _, have := range ws {
		if have == w {
			return ws
		}
	}
	return append(ws, w)
}

// premiseReceiptNote is what the closure prompt says about the receipt: the
// round must answer it, and a claim that continues it says so.
func premiseReceiptNote(id string) string {
	return fmt.Sprintf(`THIS GAP HAS RECEIPT %s. Your revised plan MUST answer it:
  "premise_resolutions": [{"gap":"%s","outcome":"established"|"refuted"|"unresolved","evidence":"file and symbol read, or why it stays open"}]
An answer that is missing is read as "unresolved". A premise you could not settle and
that your plan still rests on carries "gap":"%s" on its claim, however you now word it;
re-wording an unsettled premise does not make it a new question and buys no new round.
A different, unrelated premise carries no "gap" field.`, id, id, id)
}
