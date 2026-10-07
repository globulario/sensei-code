package workflow

import (
	"fmt"
	"sort"
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
//
// Silence is read the same way. Every receipt this task has issued was asked
// by the closure prompt of the round that issued it (a new receipt always has
// budget, so the round always runs), and this is called on the response to
// that round before any new receipt is issued. A receipt that response did
// not answer -- premise_resolutions omitted, or naming only receipts nobody
// issued -- is therefore unresolved, not unasked. Leaving it blank let the
// same paraphrased premise miss the residue rule and buy a fresh receipt,
// which is the laundering this file exists to stop (review 5046471526).
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
	for _, r := range e.premises[taskID] {
		if r.Outcome == "" {
			r.Outcome = premiseUnresolved
		}
	}
}

// premiseReceiptFor returns the receipt a closing route continues, issuing a
// new one when it continues none. The receipt's ID is what the closure
// budget is spent against.
//
// A coverage gap is one live episode of the task's resolution ledger, and its
// receipt is that episode's: selected by the canonical episode key the
// registered routing carries (bindEpisode bound it through the one
// continuation discriminator, episodeLineage.continuation), so the resolution
// ledger and the closure budget cannot disagree about which episode a replan
// continues. A distinct qualified requirement is a distinct key, so it never
// shares a receipt or a budget; a narrowed, widened, emptied or returned
// scope keeps the key, so it never buys one. A scope-less observation the
// ledger could not bind to one episode (GapIdentity.Ambiguous) continues no
// receipt and is issued none: it gets no budget (spendClosure refuses it).
//
// Any other gap continues an existing receipt when:
//   - the claim that produced it references the receipt by ID (claimRef); or
//   - a receipt is open and unresolved after its round, and this route
//     continues it under the same discriminator: same kind and world, the
//     same subject or no located subject at all, and a scope the receipt's
//     opening shares, or none. An unanswered question about a place does not
//     fund a new question about the same place, and a premise that moved
//     from a path to a symbol is still the premise the round did not settle.
//
// A coverage claimRef continues only the canonical episode its receipt names:
// it cannot fold a distinct qualified requirement into another's receipt.
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
	if routing.Gap.Ambiguous {
		return &premiseReceipt{Gap: routing.Gap, Wordings: []string{routing.Condition}}
	}
	coverage := routing.Gap.Identified() && isCoverageGapKind(routing.Gap.Kind)
	episode := ""
	if coverage {
		episode = canonicalQuestion(routing.Gap).Key()
	}
	receipts := e.premises[taskID]
	if ref := strings.TrimSpace(claimRef); ref != "" {
		for _, r := range receipts {
			if r.ID != ref {
				continue
			}
			if coverage && (!isCoverageGapKind(r.Gap.Kind) || canonicalQuestion(r.Gap).Key() != episode) {
				break
			}
			r.Wordings = appendWording(r.Wordings, routing.Condition)
			return r
		}
	}
	if !routing.Gap.Identified() {
		// A gap the router did not identify keeps the pre-receipt floor: the
		// same condition text continues the same receipt. Without this, an
		// unidentified gap was issued a fresh receipt -- and a fresh budget
		// -- every round: B3's N1b spent every round to the ceiling on one
		// identical condition (a blind-spot coverage gap), where the old
		// wording-keyed budget would have refused the second attempt. #98
		// must never spend MORE budget than the rule it replaced.
		for _, r := range receipts {
			if r.open() && len(r.Wordings) != 0 && r.Wordings[0] == routing.Condition {
				return r
			}
		}
	}
	if routing.Gap.Identified() {
		for _, r := range receipts {
			// An open receipt continues its episode. open() excludes
			// established and refuted, the two outcomes that genuinely END an
			// episode; a receipt no round has answered yet (Outcome "") still
			// continues, or the same question would buy a fresh budget.
			if !r.open() {
				continue
			}
			if coverage {
				if !isCoverageGapKind(r.Gap.Kind) || canonicalQuestion(r.Gap).Key() != episode {
					continue
				}
			} else if (episodeLineage{Kind: r.Gap.Kind, Subject: r.Gap.Subject, World: r.Gap.World, Question: r.Gap.Question,
				Opening: r.Gap.Scope, Current: r.Gap.Scope, Members: r.Gap.Scope}).continuation(routing.Gap) == episodeUnrelated {
				continue
			}
			r.Wordings = appendWording(r.Wordings, routing.Condition)
			return r
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

// normalizeEpisodeScope is the cleaning the episode test and GapIdentity.Key
// must agree on: trimmed, de-duplicated, sorted.
func normalizeEpisodeScope(scope []string) []string {
	seen := make(map[string]bool, len(scope))
	out := make([]string, 0, len(scope))
	for _, f := range scope {
		f = strings.TrimSpace(f)
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

func scopesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// scopesOverlap reports whether the two scopes name any file in common. One
// shared file is enough: a replan still working on part of what the episode
// opened over is still the same episode, however it reshaped the rest.
func scopesOverlap(a, b []string) bool {
	in := make(map[string]bool, len(a))
	for _, f := range a {
		in[f] = true
	}
	for _, f := range b {
		if in[f] {
			return true
		}
	}
	return false
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
