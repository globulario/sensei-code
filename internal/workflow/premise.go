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
			// An open receipt continues its episode. The old test also required
			// Outcome == unresolved, which excluded a receipt no round had
			// answered yet (Outcome "") -- one more way to reach a fresh
			// receipt, and therefore a fresh budget, for the same question.
			// open() already excludes established and refuted, which are the
			// two outcomes that genuinely END an episode.
			if !r.open() {
				continue
			}
			if continuesClosureEpisode(r, routing.Gap) == episodeUnrelated {
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

// episodeContinuation says WHY a replan belongs to an open closure episode, or
// that it does not.
//
// Named cases rather than one boolean, because the rule has to be explicit and
// each branch separately testable. In particular a degenerate replan is NOT
// treated as "a scope that overlaps everything" -- that would silently make
// unrelated gaps share an episode the moment one of them named no files.
type episodeContinuation int

const (
	// episodeUnrelated: a different question. It gets its own receipt and its
	// own budget, which is the discrimination sensei-code#97 established.
	episodeUnrelated episodeContinuation = iota
	// episodeSameScope: the replan named the same files.
	episodeSameScope
	// episodeOverlapping: the replan moved, narrowed or widened, and still
	// concerns files the episode opened over.
	episodeOverlapping
	// episodeDegenerate: the replan named NO files during an active episode.
	//
	// Observed live at 14:32:21 on task-1789272620293170079: coverage collapsed
	// to "0 anchor(s) over 0 planned file(s)". A plan that names nothing has not
	// answered the question and has not become a different question, so it stays
	// bound to the episode it is failing to close. It must never buy a round by
	// evaporating the work surface.
	episodeDegenerate
)

// continuesClosureEpisode decides whether a routing's gap continues the episode
// this receipt opened.
//
// THE LAW: the retried actor may change its plan; it may not thereby change the
// closure episode's identity. premise.go already states this about
// PremiseResolution -- "authored by the architect but keyed by an engine-issued
// ID, so what the model chooses is the outcome, not the identity" -- and the
// lookup is where it was lost: selection recomputed the identity from the
// architect's LATEST scope, so a replan selected a different receipt and
// spendClosure, correctly keyed on that receipt's ID, handed out a fresh budget.
//
// Measured consequence on task-1789272620293170079 (2026-09-13): one
// coverage-unexamined gap, scope 7 -> 5 -> 7 -> 0 -> 7 -> 6, six closure rounds
// under closureBudget = 1.
//
// The comparison is against the receipt's STORED OPENING GAP, which is immutable
// once issued, rather than against the current plan. Kind, World and Subject keep
// discriminating exactly as before; only the scope test is relaxed from equality
// to episode membership.
func continuesClosureEpisode(r *premiseReceipt, gap GapIdentity) episodeContinuation {
	if r.Gap.Kind != gap.Kind || r.Gap.World != gap.World {
		return episodeUnrelated
	}
	// Subject unchanged from the previous rule: an equal subject continues, and
	// a routing that names none continues whatever it landed in. Two premises
	// about one file remain two questions.
	if gap.Subject != r.Gap.Subject && gap.Subject != "" {
		return episodeUnrelated
	}
	opening, replanned := normalizeEpisodeScope(r.Gap.Scope), normalizeEpisodeScope(gap.Scope)
	switch {
	case len(replanned) == 0:
		// Explicitly its own case. A degenerate replan during an OPEN episode
		// stays in it; it is not overlap and must not be described as overlap.
		return episodeDegenerate
	case len(opening) == 0:
		// The episode opened over no files, so nothing constrains membership by
		// path. Only Kind/World/Subject can discriminate, and they already did.
		return episodeSameScope
	case scopesEqual(opening, replanned):
		return episodeSameScope
	case scopesOverlap(opening, replanned):
		return episodeOverlapping
	}
	return episodeUnrelated
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
