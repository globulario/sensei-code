// SPDX-License-Identifier: AGPL-3.0-only

// Package evidence records the MINIMAL COMPLETE CAUSAL ENVELOPE of a governed
// call: enough to reconstruct the experiment, and no more.
//
// WHY IT EXISTS. sensei-code#134 preserved a run that stopped at the human
// authority boundary carrying "human_approval_required (blast radius cluster)"
// and NOT the request that produced it. When the upstream repair was ready the
// specimen could not be replayed, and the escalation no longer reproduced even
// on the binary that had produced it. A post-repair "local/none" would have
// looked exactly like proof and established nothing.
//
//	A PRESERVED VERDICT IS NOT A PRESERVED EXPERIMENT.
//
// #137 repaired one call site. An audit then found that ONE OF SIX preflight
// call sites recorded its request, so the class was open. This package is the
// one way to record such a call, so a new site cannot quietly omit the half
// that makes a record replayable.
//
// THE OBJECTIVE IS NOT ENORMOUS LOGS. The envelope answers a fixed set of
// questions and stops. Anything a replay does not need does not belong here.
package evidence

import "strings"

// Envelope pairs the QUESTION with the ANSWER and the world both were true in.
//
// Field names are the JSON keys of the recorded payload; changing one changes a
// durable record's shape, which is a decision rather than a rename.
type Envelope struct {
	// Operation is what was asked -- the tool or RPC name.
	Operation string `json:"operation"`
	// Request is the exact input. Without it the record is a historical
	// observation and not a replayable proof.
	Request map[string]any `json:"request"`
	// Revision is the source revision the subject was read at.
	Revision string `json:"revision,omitempty"`
	// GraphDigest identifies the knowledge generation that answered. A verdict
	// is only reproducible against the graph that produced it.
	GraphDigest string `json:"graph_digest,omitempty"`
	// Tool identifies the binary that asked, so a replay can tell a changed
	// answer from a changed asker.
	Tool string `json:"tool,omitempty"`
}

// Record renders the envelope beside a result.
//
// The result's own fields are carried at the TOP LEVEL and the envelope under a
// reserved key, because every existing reader of these payloads looks for
// result fields such as risk_class. The result map is COPIED: callers decode
// the same structure afterwards, and mutating it here would change what they
// decode.
func (e Envelope) Record(result map[string]any) map[string]any {
	out := make(map[string]any, len(result)+1)
	for k, v := range result {
		out[k] = v
	}
	out["evidence"] = e.payload()
	return out
}

func (e Envelope) payload() map[string]any {
	p := map[string]any{"operation": e.Operation, "request": e.Request}
	if v := strings.TrimSpace(e.Revision); v != "" {
		p["revision"] = v
	}
	if v := strings.TrimSpace(e.GraphDigest); v != "" {
		p["graph_digest"] = v
	}
	if v := strings.TrimSpace(e.Tool); v != "" {
		p["tool"] = v
	}
	return p
}

// HasQuestion reports whether the QUESTION survived. It is the property whose
// absence created #134, and it is necessary rather than sufficient.
func (e Envelope) HasQuestion() bool {
	return strings.TrimSpace(e.Operation) != "" && len(e.Request) > 0
}

// Replayable reports whether this envelope carries enough to RECONSTRUCT the
// call -- question, subject and world.
//
// An earlier version returned true on the question alone, reasoning that a
// missing revision merely "weakens" a replay. That is the softer half of the
// same mistake: replaying against an unknown revision and an unknown graph
// generation does not reproduce the experiment, it runs a NEW one that happens
// to share a question. Missing() already named those fields as absent while
// this method called the record replayable -- two answers from one type.
//
// The distinction is preserved rather than collapsed: HasQuestion is the weaker
// property and is still available, so a caller can say "the question survived,
// the world did not" instead of choosing between true and false.
func (e Envelope) Replayable() bool {
	return e.HasQuestion() && len(e.Missing()) == 0
}

// Missing names what a replay would lack, so a caller can report a partial
// envelope as partial instead of discovering it when the replay is attempted.
func (e Envelope) Missing() []string {
	var out []string
	if strings.TrimSpace(e.Operation) == "" {
		out = append(out, "operation")
	}
	if len(e.Request) == 0 {
		out = append(out, "request")
	}
	if strings.TrimSpace(e.Revision) == "" {
		out = append(out, "revision")
	}
	if strings.TrimSpace(e.GraphDigest) == "" {
		out = append(out, "graph_digest")
	}
	return out
}
