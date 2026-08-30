// Package legacy reconstructs a receipt from the event stream older runs left
// behind. It is a COMPATIBILITY ADAPTER, and it is deliberately not reachable
// from the core package.
//
// The governed loop imports internal/runreceipt and never this package.
// Documentation did not stop a parser from being tested against invented
// specimens, so the boundary is a package rather than a comment: the
// dependency graph itself now refuses the accidental resurrection of the
// reconstruct-afterwards architecture.
//
// What this produces is an APPROXIMATION. Where the historical stream never
// measured something, the field is UNKNOWN with a reason. It must never fill a
// gap by inference -- an earlier draft of this adapter set GovernorCommit from
// the same field it set BaseCommit from, which is one measured fact becoming
// two claims, the exact pattern this whole chain has been repairing.
//
// Totality is the contract: no syntactically valid JSON line may crash
// extraction. Every read of a nested value is classified. The defect that
// voided C5 -- payload.provenance arriving as a string where an object was
// assumed -- is a MALFORMED classification here, not a panic.
package legacy

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/globulario/sensei-code/internal/runreceipt"
)

// maxLine is generous: one governed event can carry a whole candidate diff. A
// longer line is recorded as a diagnostic rather than silently dropped.
const maxLine = 16 << 20

// object reads a nested object without assuming it is one.
//
// This function exists because assuming it was one voided an experiment.
func object(m map[string]any, key string) (map[string]any, runreceipt.Knownness, string) {
	raw, ok := m[key]
	if !ok {
		return nil, runreceipt.Unknown, "absent"
	}
	if raw == nil {
		return nil, runreceipt.Unknown, "null"
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil, runreceipt.Malformed, fmt.Sprintf("%s is %T, not an object", key, raw)
	}
	return obj, runreceipt.Known, ""
}

// text reads a string without assuming it is one, and stamps the source path
// the value came from so the record says how it was measured.
func text(m map[string]any, key, source string) runreceipt.Value {
	raw, ok := m[key]
	if !ok {
		return runreceipt.UnknownValue("absent")
	}
	switch v := raw.(type) {
	case nil:
		return runreceipt.UnknownValue("null")
	case string:
		return runreceipt.MeasuredValue(v, source)
	default:
		return runreceipt.MalformedValue(fmt.Sprintf("%s is %T, not a string", key, raw))
	}
}

// nested reads a string one level down, classifying every way that can fail.
func nested(m map[string]any, objKey, key, source string) runreceipt.Value {
	obj, state, detail := object(m, objKey)
	switch state {
	case runreceipt.Unknown:
		return runreceipt.UnknownValue(objKey + " " + detail)
	case runreceipt.Malformed:
		return runreceipt.MalformedValue(detail)
	}
	return text(obj, key, source)
}

// FromEvents builds an approximate receipt from a governed run's JSONL stream.
//
// It never returns an error and never panics on any input: what it cannot model
// becomes a diagnostic and an explicitly Unknown, Malformed or Unsupported
// value. A reader that crashes on unexpected input converts an observation into
// an outage, and the observation is the thing worth keeping.
func FromEvents(r io.Reader) runreceipt.Receipt {
	rec := runreceipt.Receipt{Schema: runreceipt.SchemaVersion, Outcome: runreceipt.OutcomeUnknown}
	// The historical stream cannot distinguish "this run produced no candidate"
	// from "the events do not say", so the adapter says UNKNOWN rather than
	// choosing. A governor emitting its own receipt knows which it is.
	rec.CandidateState = runreceipt.CandidateUnknown
	// The historical stream does not record whether a plan governed the run in
	// a way this adapter can read, and UNKNOWN is the answer rather than a
	// convenient NONE. Leaving it at Go's zero value would have been a third
	// instance of the raw-zero pattern, inside the package that exists to
	// remove it.
	rec.PlanState = runreceipt.PlanUnknown
	// v3 facts the historical stream never carried. Stated, not defaulted.
	rec.ReviewedTree = runreceipt.UnknownValue("the event stream does not name the tree a verdict was bound to")
	rec.CandidateCommitDiffDigest = runreceipt.UnknownValue("no candidate identity was minted, so nothing was rendered from one")
	rec.CandidateDigestRelation = runreceipt.RelationUnknown
	rec.DeferredQuestion = runreceipt.UnknownValue("the event stream does not record a deferred authority question")
	rec.ExecutionBudget = runreceipt.UnknownValue("the event stream does not record an execution budget")

	// Facts the historical stream never measured. Each says so, and why. A
	// governor emitting its own receipt supplies these; an adapter cannot.
	rec.GovernorCommit = runreceipt.UnknownValue(
		"the event stream does not identify the governor commit; only the governor can state it, and it is NOT the base commit")
	rec.GovernorBinarySHA256 = runreceipt.UnknownValue("not present in the event stream; the governor must emit it")
	rec.ServingProducer = runreceipt.UnknownValue("not present in the event stream; the governor must emit it")
	rec.ReviewerExecutable = runreceipt.UnknownValue("not present in the event stream; the governor must emit it")
	rec.CandidateCommit = runreceipt.UnknownValue("the event stream names no candidate commit")
	rec.CandidateTree = runreceipt.UnknownValue("the event stream names no candidate tree")
	rec.CandidateFirstParent = runreceipt.UnknownValue("the event stream names no candidate first parent")

	rec.BaseCommit = runreceipt.UnknownValue("no event carried it")
	rec.PlanDigest = runreceipt.UnknownValue("no event carried it")
	rec.GraphDigest = runreceipt.UnknownValue("no event carried it")
	rec.CandidateDigest = runreceipt.UnknownValue("no candidate digest observed")
	rec.ReviewerProvider = runreceipt.UnknownValue("no reviewer was assigned")
	rec.ReviewVerdict = runreceipt.UnknownValue("no bounded verdict")
	rec.ReviewedDigest = runreceipt.UnknownValue("no bounded verdict")
	rec.Terminal = runreceipt.UnknownValue("no terminal event observed")

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), maxLine)
	line, unparsed := 0, 0
	unknownKinds := map[string]int{}
	var pending *runreceipt.Attempt

	flush := func() {
		if pending != nil {
			rec.Attempts = append(rec.Attempts, *pending)
			pending = nil
		}
	}

	for sc.Scan() {
		line++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" {
			continue
		}
		if !strings.HasPrefix(raw, "{") {
			unparsed++
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(raw), &ev); err != nil {
			unparsed++
			continue
		}
		kind, _ := ev["kind"].(string)
		payload, pstate, pdetail := object(ev, "payload")
		if pstate != runreceipt.Known {
			payload = map[string]any{}
			if pstate == runreceipt.Malformed {
				rec.Diagnostics = append(rec.Diagnostics, fmt.Sprintf("line %d (%s): %s", line, kind, pdetail))
			}
		}

		switch kind {
		case "sensei.result":
			if binding, state, _ := object(payload, "binding"); state == runreceipt.Known {
				if v := text(binding, "revision", "event:sensei.result.payload.binding.revision"); v.State == runreceipt.Known && rec.BaseCommit.State != runreceipt.Known {
					rec.BaseCommit = v
				}
			}
			if auth, state, _ := object(payload, "graph_authority"); state == runreceipt.Known {
				v := text(auth, "live_store_graph_digest_sha256", "event:sensei.result.payload.graph_authority.live_store_graph_digest_sha256")
				if v.State == runreceipt.Known && rec.GraphDigest.State != runreceipt.Known {
					rec.GraphDigest = v
				}
			}
		case "plan.proposed":
			if v := text(payload, "plan_digest", "event:plan.proposed.payload.plan_digest"); v.State == runreceipt.Known {
				rec.PlanDigest = v
			}
		case "agent.role.assigned":
			if role := text(payload, "role", "event:agent.role.assigned.payload.role"); role.State == runreceipt.Known && role.Text != "reviewer" {
				break
			}
			flush()
			provider := text(payload, "provider", "event:agent.role.assigned.payload.provider")
			pending = &runreceipt.Attempt{
				Provider: provider,
				// The historical stream does not state whether a superseded
				// attempt failed or was abandoned, and the adapter does not
				// infer. Only a delivered verdict is measurable from it.
				Delivery: runreceipt.UnknownValue("the event stream does not record whether this attempt delivered"),
				Verdict:  runreceipt.UnknownValue("this attempt produced no verdict"),
				Digest:   runreceipt.UnknownValue("this attempt produced no verdict"),
			}
			if provider.State == runreceipt.Known {
				rec.ReviewerProvider = provider
			}
			if v := text(payload, "candidate", "event:agent.role.assigned.payload.candidate"); v.State == runreceipt.Known {
				rec.CandidateDigest = v
				rec.CandidateState = runreceipt.CandidatePresent
			}
		case "review.completed":
			verdict := text(payload, "decision", "event:review.completed.payload.decision")
			digest := nested(payload, "provenance", "candidate_digest", "event:review.completed.payload.provenance.candidate_digest")
			provider := nested(payload, "provenance", "provider", "event:review.completed.payload.provenance.provider")
			if pending == nil {
				pending = &runreceipt.Attempt{Provider: runreceipt.UnknownValue("no role assignment preceded this verdict")}
			}
			pending.Delivery = runreceipt.DeliveryValue(runreceipt.Delivered, "event:review.completed")
			pending.Verdict = verdict
			pending.Digest = digest
			if provider.State == runreceipt.Known {
				pending.Provider = provider
				rec.ReviewerProvider = provider
			}
			rec.ReviewVerdict = verdict
			rec.ReviewedDigest = digest
			flush()
		case "candidate.changed":
			rec.CandidateState = runreceipt.CandidatePresent
		case "workflow.completed", "workflow.failed", "candidate.not_auditable":
			rec.Terminal = runreceipt.MeasuredValue(kind, "event:"+kind)
		case "candidate.resolved":
			rec.CandidateState = runreceipt.CandidatePresent
			if evidence, state, _ := object(payload, "evidence"); state == runreceipt.Known {
				if v := text(evidence, "base_sha", "event:candidate.resolved.payload.evidence.base_sha"); v.State == runreceipt.Known {
					rec.BaseCommit = v
				}
			}
		default:
			if kind == "" {
				unknownKinds["(no kind)"]++
			} else if !modelled(kind) {
				unknownKinds[kind]++
			}
		}
	}
	flush()
	if err := sc.Err(); err != nil {
		rec.Diagnostics = append(rec.Diagnostics, "stream truncated: "+err.Error())
	}
	if unparsed > 0 {
		rec.Diagnostics = append(rec.Diagnostics, fmt.Sprintf("%d line(s) were not JSON objects", unparsed))
	}
	for k, n := range unknownKinds {
		rec.Diagnostics = append(rec.Diagnostics, fmt.Sprintf("kind %s not modelled by %s (%d event(s))", k, runreceipt.SchemaVersion, n))
	}
	rec.Outcome = OutcomeFrom(rec)
	return rec
}

// modelled reports whether a kind is one this adapter deliberately ignores
// rather than one it has never heard of. Silence about a kind we chose not to
// read is different from silence about a kind we did not know existed.
func modelled(kind string) bool {
	switch kind {
	case "output", "status", "task.created", "mode.selected", "context.consulted",
		"candidate.audited", "validation.run", "routine.classified",
		"review.started", "review.finding", "change.reported", "decision.recorded",
		"agent.started", "agent.finished", "inspection.reported", "handoff.created",
		"authority.required", "authority.resolved", "review.contradiction",
		"architect.reconciliation", "candidate.artifact_excluded":
		return true
	}
	return false
}

// OutcomeFrom reads the run axis, and only the run axis. It never consults
// completeness: a record missing a required field still has an outcome, and
// conflating the two is the collapse the schema exists to prevent.
func OutcomeFrom(r runreceipt.Receipt) runreceipt.Outcome {
	if r.ReviewVerdict.State == runreceipt.Known {
		if strings.EqualFold(r.ReviewVerdict.Text, "accept") {
			return runreceipt.OutcomeAccepted
		}
		return runreceipt.OutcomeRefused
	}
	if r.Terminal.State == runreceipt.Known {
		if r.Terminal.Text == "workflow.completed" {
			return runreceipt.OutcomeUnreviewed
		}
		return runreceipt.OutcomeFailed
	}
	return runreceipt.OutcomeUnknown
}
