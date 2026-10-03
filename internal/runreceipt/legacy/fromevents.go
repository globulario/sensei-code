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

	"github.com/globulario/sensei-code/internal/event"
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
	// The candidate state and its five identity fields are published by one
	// owner, at the end, from the invocation it settled. Nothing else here
	// writes them.
	cand := newCandidateReconstruction()
	// The historical stream does not record whether a plan governed the run in
	// a way this adapter can read, and UNKNOWN is the answer rather than a
	// convenient NONE. Leaving it at Go's zero value would have been a third
	// instance of the raw-zero pattern, inside the package that exists to
	// remove it.
	rec.PlanState = runreceipt.PlanUnknown
	// v3 facts the historical stream never carried. Stated, not defaulted.
	rec.ReviewedTree = runreceipt.UnknownValue("the event stream does not name the tree a verdict was bound to")
	rec.CandidateDigestRelation = runreceipt.RelationUnknown
	rec.DeferredQuestion = runreceipt.UnknownValue("the event stream does not record a deferred authority question")
	rec.ExecutionBudget = runreceipt.UnknownValue("the event stream does not record an execution budget")
	rec.ExternalBlock = runreceipt.UnknownValue("the event stream does not record an externally blocked role turn")
	rec.NotConverged = runreceipt.UnknownValue("the event stream does not record a non-convergence")
	rec.RestorationRefusal = runreceipt.UnknownValue("the event stream does not record a refused restoration")
	rec.FormatterMutationState = runreceipt.MeasuredValue(string(runreceipt.FormatterUnsaid),
		"the historical event stream does not record whether a formatter rewrote the candidate")

	// Facts the historical stream never measured. Each says so, and why. A
	// governor emitting its own receipt supplies these; an adapter cannot.
	rec.GovernorCommit = runreceipt.UnknownValue(
		"the event stream does not identify the governor commit; only the governor can state it, and it is NOT the base commit")
	rec.GovernorBinarySHA256 = runreceipt.UnknownValue("not present in the event stream; the governor must emit it")
	rec.ServingProducer = runreceipt.UnknownValue("not present in the event stream; the governor must emit it")
	rec.ReviewerExecutable = runreceipt.UnknownValue("not present in the event stream; the governor must emit it")

	rec.BaseCommit = runreceipt.UnknownValue("no event carried it")
	rec.PlanDigest = runreceipt.UnknownValue("no event carried it")
	rec.GraphDigest = runreceipt.UnknownValue("no event carried it")
	rec.ReviewerProvider = runreceipt.UnknownValue("no reviewer was assigned")
	rec.ReviewVerdict = runreceipt.UnknownValue("no bounded verdict")
	rec.ReviewedDigest = runreceipt.UnknownValue("no bounded verdict")
	rec.Terminal = runreceipt.UnknownValue("no terminal event observed")

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), maxLine)
	line, unparsed := 0, 0
	unknownKinds := map[string]int{}
	var pending *runreceipt.Attempt
	// refusals are the canonical refusal records seen so far, pending a
	// PLAN_ADMISSION_REFUSED terminal that binds one by identity.
	var refusals []refusalRecord

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
				cand.observeDigest(v)
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
			cand.observeWork()
		case "workflow.completed", "workflow.failed", "candidate.not_auditable":
			rec.Terminal = runreceipt.MeasuredValue(kind, "event:"+kind)
			cand.settle(&rec)
			// A later terminal ends a later invocation; a refusal that parked
			// an earlier one is not this record's ending.
			rec.PlanAdmissionRefusal = nil
		case planAdmissionRefusedKind:
			rec.Terminal = runreceipt.MeasuredValue(kind, "event:"+kind)
			cand.settle(&rec)
			rec.PlanAdmissionRefusal = planAdmissionRefusal(raw, payload, pstate, pdetail, refusals)
		case "plan.attempt.refused":
			// A refusal returned to the architect does not end the invocation.
			// It is kept as evidence a later terminal may bind to by identity.
			if pstate == runreceipt.Known {
				const source = "event:plan.attempt.refused.payload"
				refusals = append(refusals, readRefusal(payload, payloadDeclaration(raw, payload, source), source))
			}
		case "run.receipt":
			if snap, ok := readReceiptCandidate(&rec, payload, line); ok {
				cand.snapshot(snap)
			}
			if r, ok := receiptRefusal(&rec, payload, line); ok {
				refusals = append(refusals, r)
			}
		case "candidate.resolved":
			cand.observeWork()
			if evidence, state, _ := object(payload, "evidence"); state == runreceipt.Known {
				if v := text(evidence, "base_sha", "event:candidate.resolved.payload.evidence.base_sha"); v.State == runreceipt.Known {
					rec.BaseCommit = v
				}
			}
		default:
			// Every other run ending the canonical vocabulary names is still an
			// invocation boundary. The receipt does not model it as a terminal,
			// but the candidate an earlier invocation established must not
			// survive into a later one across it.
			if _, ok := event.RunTerminality(event.Kind(kind)); ok {
				cand.settle(&rec)
			}
			if kind == "" {
				unknownKinds["(no kind)"]++
			} else if !modelled(kind) {
				unknownKinds[kind]++
			}
		}
	}
	flush()
	cand.publish(&rec)
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
	// A parked plan-admission refusal ends the invocation whatever an earlier
	// cycle's verdict said, as the live receipt records it. Its outcome stands
	// even when the refusal's provenance is incomplete: the terminal event is
	// the measurement, and the missing identities are stated on the fact.
	if r.Terminal.State == runreceipt.Known && r.Terminal.Text == planAdmissionRefusedKind {
		return runreceipt.OutcomePlanAdmissionRefused
	}
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

// planAdmissionRefusedKind is the terminal event of an invocation parked on a
// repeated plan-admission refusal (receipt schema v13).
const planAdmissionRefusedKind = "workflow.plan_admission_refused"

// candidateKeys are the receipt's candidate identity fields, in the order a
// candidateSnapshot holds them.
var candidateKeys = [5]string{"candidate_commit", "candidate_tree", "candidate_first_parent", "candidate_digest", "candidate_commit_diff_digest"}

// candidateInsufficiency is what each identity field says when the stream
// does not establish it, whether no event carried it or the run's receipt left
// it unmeasured. It names the stream's insufficiency, never an absence: a
// receipt's own UNKNOWN wording ("no candidate was created") is the
// specimen-shaped absence a reconstruction must not repeat when nothing
// proves it.
var candidateInsufficiency = [5]string{
	"the event stream does not establish a candidate commit",
	"the event stream does not establish a candidate tree",
	"the event stream does not establish a candidate first parent",
	"the event stream does not establish a candidate digest",
	"the historical event stream cannot establish whether a candidate identity was minted, so no rendering of one can be read from it",
}

// candidateSnapshot is a complete candidate account: a state and every
// identity field, each classified.
type candidateSnapshot struct {
	state runreceipt.CandidateState
	ids   [5]runreceipt.Value
}

// insufficientCandidate is the account of a stream that establishes nothing
// about a candidate.
func insufficientCandidate() candidateSnapshot {
	s := candidateSnapshot{state: runreceipt.CandidateUnknown}
	for i, why := range candidateInsufficiency {
		s.ids[i] = runreceipt.UnknownValue(why)
	}
	return s
}

// candidateReconstruction is the one owner of the candidate state and identity
// a reconstruction publishes. It is scoped to an invocation: a terminal event
// settles what the invocation established and resets its evidence, so nothing
// one invocation observed can reach another's account.
//
// A supported run.receipt is the invocation's canonical account and replaces
// any earlier one whole. candidate.changed, candidate.resolved and a reviewer
// assignment naming a candidate are fallback observations, read only when the
// invocation has no such receipt: transient work does not veto a receipt's
// final NONE, and a receipt's UNKNOWN is not strengthened by it.
type candidateReconstruction struct {
	receipt *candidateSnapshot // the invocation's latest supported receipt
	work    bool               // fallback: an ordinary event measured candidate work
	digest  runreceipt.Value   // fallback: the candidate a reviewer was assigned
	settled *candidateSnapshot // the latest settled invocation
}

func newCandidateReconstruction() *candidateReconstruction {
	c := &candidateReconstruction{}
	c.reset()
	return c
}

func (c *candidateReconstruction) reset() {
	c.receipt, c.work, c.digest = nil, false, runreceipt.Value{}
}

func (c *candidateReconstruction) observeWork() { c.work = true }

func (c *candidateReconstruction) observeDigest(v runreceipt.Value) { c.work, c.digest = true, v }

func (c *candidateReconstruction) snapshot(s candidateSnapshot) { c.receipt = &s }

// settle closes the current invocation and resets its evidence before any
// later event is read. Its account replaces the settled one only when it
// supplied candidate evidence: an invocation that established nothing about a
// candidate does not erase what an earlier one settled.
func (c *candidateReconstruction) settle(rec *runreceipt.Receipt) {
	if c.receipt != nil || c.work {
		s := c.current(rec)
		c.settled = &s
	}
	c.reset()
}

// current is the account the current invocation's evidence establishes.
func (c *candidateReconstruction) current(rec *runreceipt.Receipt) candidateSnapshot {
	if c.receipt != nil {
		s := *c.receipt
		if c.work && (s.state == runreceipt.CandidateNone || s.state == runreceipt.CandidateUnattempted) {
			rec.Diagnostics = append(rec.Diagnostics, fmt.Sprintf(
				"the event stream measured transient candidate work in an invocation whose receipt states candidate_state %s; the receipt's final state stands", s.state))
		}
		return s
	}
	s := insufficientCandidate()
	if c.work {
		s.state = runreceipt.CandidatePresent
	}
	if c.digest.State == runreceipt.Known {
		s.ids[3] = c.digest
	}
	return s
}

// publish writes the settled account into rec. A trailing invocation that
// never reached a terminal settles like any other: only when it carried
// candidate evidence; otherwise the latest settled invocation stands.
func (c *candidateReconstruction) publish(rec *runreceipt.Receipt) {
	c.settle(rec)
	s := insufficientCandidate()
	if c.settled != nil {
		s = *c.settled
	}
	rec.CandidateState = s.state
	rec.CandidateCommit, rec.CandidateTree, rec.CandidateFirstParent = s.ids[0], s.ids[1], s.ids[2]
	rec.CandidateDigest, rec.CandidateCommitDiffDigest = s.ids[3], s.ids[4]
}

// readReceiptCandidate reads a run's own receipt as a complete candidate
// snapshot: every identity field is classified from this receipt alone, and
// its candidate_state -- UNKNOWN included -- is the invocation's canonical
// state. A KNOWN field is carried, a MALFORMED one stays MALFORMED, and an
// UNSUPPORTED one stays UNSUPPORTED, and an UNKNOWN, absent or null one
// states the stream's insufficiency for that field. Only NONE and UNATTEMPTED, absences the receipt positively
// establishes, turn its unmeasured identities into absence.
//
// Nothing is read from a receipt whose schema is not one the receipt package
// defines: such a receipt has no vocabulary its facts can be checked against.
func readReceiptCandidate(rec *runreceipt.Receipt, payload map[string]any, line int) (candidateSnapshot, bool) {
	receipt, state, detail := object(payload, "receipt")
	if state != runreceipt.Known {
		if state == runreceipt.Malformed {
			rec.Diagnostics = append(rec.Diagnostics, fmt.Sprintf("line %d (run.receipt): %s", line, detail))
		}
		return candidateSnapshot{}, false
	}
	schema, _ := receipt["schema"].(string)
	// UNKNOWN belongs to every candidate vocabulary, so this fails only for a
	// schema the receipt package does not define.
	if err := runreceipt.SpeaksItsCandidateVocabulary(schema, runreceipt.CandidateUnknown); err != nil {
		rec.Diagnostics = append(rec.Diagnostics, fmt.Sprintf("line %d (run.receipt): %v; its candidate facts are not read", line, err))
		return candidateSnapshot{}, false
	}
	s := insufficientCandidate()
	for i, key := range candidateKeys {
		s.ids[i] = receiptIdentity(receipt, key, candidateInsufficiency[i])
	}
	if raw, present := receipt["candidate_state"]; present && raw != nil {
		cs, _ := raw.(string)
		c := runreceipt.CandidateState(cs)
		if err := runreceipt.SpeaksItsCandidateVocabulary(schema, c); err != nil {
			rec.Diagnostics = append(rec.Diagnostics, fmt.Sprintf("line %d (run.receipt): %v", line, err))
		} else {
			s.state = c
		}
	}
	var why string
	switch s.state {
	case runreceipt.CandidateNone:
		why = "absent: the run's own receipt states candidate_state NONE, so there is no candidate to identify"
	case runreceipt.CandidateUnattempted:
		why = "absent: the run's own receipt states candidate_state UNATTEMPTED, so no canonical candidate identity was minted"
	}
	for i := range s.ids {
		// An unattempted candidate may still have a reviewed diff digest.
		if why == "" || (s.state == runreceipt.CandidateUnattempted && candidateKeys[i] == "candidate_digest") {
			continue
		}
		if s.ids[i].State == runreceipt.Unknown {
			s.ids[i] = runreceipt.UnknownValue(why)
		}
	}
	return s, true
}

// receiptIdentity classifies one identity field of a run's receipt.
func receiptIdentity(receipt map[string]any, key, insufficient string) runreceipt.Value {
	source := "event:run.receipt.payload.receipt." + key
	v, state, _ := object(receipt, key)
	switch state {
	case runreceipt.Malformed:
		return runreceipt.MalformedValue(source + " is not an object")
	case runreceipt.Unknown:
		return runreceipt.UnknownValue(insufficient)
	}
	s, _ := v["state"].(string)
	switch runreceipt.Knownness(s) {
	case runreceipt.Known:
		if t := text(v, "text", source); t.State == runreceipt.Known || t.State == runreceipt.Malformed {
			return t
		}
		return runreceipt.MalformedValue(source + " states KNOWN but carries no text")
	case runreceipt.Malformed:
		d, _ := v["detail"].(string)
		return runreceipt.MalformedValue(source + " is MALFORMED in the run's receipt: " + d)
	case runreceipt.Unknown:
		return runreceipt.UnknownValue(insufficient)
	case runreceipt.Unsupported:
		// An identity the run could not read is not an unmeasured one, and
		// never becomes an absence a NONE receipt could claim.
		d, _ := v["detail"].(string)
		return runreceipt.Value{State: runreceipt.Unsupported, Detail: source + " is UNSUPPORTED in the run's receipt: " + d}
	}
	return runreceipt.MalformedValue(fmt.Sprintf("%s has state %q, which is not a knownness", source, s))
}

// refusalKeys are the fields schema v13 requires of a plan-admission refusal,
// in the order a missing-field detail names them.
var refusalKeys = []string{"plan_attempt_id", "refusal_id", "refusal_class", "declaration", "reason", "governing_evidence_id"}

// refusalRecord is the refusal fields one event carried, each classified, and
// the event path they were read from.
type refusalRecord struct {
	source string
	values map[string]runreceipt.Value
}

// readRefusal classifies the refusal fields of obj. The declaration is passed
// in already read, because where its exact text lives differs by event. A
// present but blank value is UNKNOWN, as an absent one: MeasuredValue does not
// count an empty string as a measurement.
func readRefusal(obj map[string]any, declaration runreceipt.Value, source string) refusalRecord {
	r := refusalRecord{source: source, values: map[string]runreceipt.Value{}}
	for _, key := range refusalKeys {
		v := declaration
		if key != "declaration" {
			v = text(obj, key, source+"."+key)
		}
		r.values[key] = v
	}
	return r
}

// payloadDeclaration reads an event payload's declaration byte for byte as the
// event carried it, rather than re-rendered from a decoded value.
func payloadDeclaration(line string, payload map[string]any, source string) runreceipt.Value {
	if d, ok := payload["declaration"]; !ok || d == nil {
		return runreceipt.UnknownValue("absent")
	}
	var ev struct {
		Payload struct {
			Declaration json.RawMessage `json:"declaration"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		return runreceipt.MalformedValue("declaration is unreadable: " + err.Error())
	}
	return runreceipt.MeasuredValue(string(ev.Payload.Declaration), source+".declaration")
}

// receiptRefusal reads the refusal fact a run's own receipt stated, and only a
// fact it stated as KNOWN: an UNKNOWN fact is the receipt saying it had none.
// The fact is read only from a receipt whose schema speaks
// PLAN_ADMISSION_REFUSED and whose outcome is PLAN_ADMISSION_REFUSED; any other
// receipt has no authority to state one, and is reported rather than read.
func receiptRefusal(rec *runreceipt.Receipt, payload map[string]any, line int) (refusalRecord, bool) {
	const source = "event:run.receipt.payload.receipt.plan_admission_refusal"
	receipt, state, _ := object(payload, "receipt")
	if state != runreceipt.Known {
		return refusalRecord{}, false
	}
	fact, state, _ := object(receipt, "plan_admission_refusal")
	if state != runreceipt.Known {
		return refusalRecord{}, false
	}
	if s, _ := fact["state"].(string); runreceipt.Knownness(s) != runreceipt.Known {
		return refusalRecord{}, false
	}
	schema, _ := receipt["schema"].(string)
	if err := runreceipt.SpeaksItsVersion(schema, runreceipt.OutcomePlanAdmissionRefused); err != nil {
		rec.Diagnostics = append(rec.Diagnostics, fmt.Sprintf("line %d (run.receipt): %v; its plan_admission_refusal is not read", line, err))
		return refusalRecord{}, false
	}
	if o, _ := receipt["outcome"].(string); runreceipt.Outcome(o) != runreceipt.OutcomePlanAdmissionRefused {
		rec.Diagnostics = append(rec.Diagnostics, fmt.Sprintf("line %d (run.receipt): outcome %q is not %s; its plan_admission_refusal is not read",
			line, o, runreceipt.OutcomePlanAdmissionRefused))
		return refusalRecord{}, false
	}
	return readRefusal(fact, text(fact, "declaration", source+".declaration"), source), true
}

// planAdmissionRefusal reconstructs the refusal a PLAN_ADMISSION_REFUSED
// terminal event ended the invocation on.
//
// The terminal's own fields are the measurement. A field it lacks is completed
// only from an earlier refusal record bound to it by the exact refusal
// identity both carry -- never from a record chosen because it is the only
// one, since a historical terminal without that identity names no record. A
// field the terminal and a bound record both carry must agree, or the stream
// contradicts itself and the fact is MALFORMED. A field a bound record carries
// in a shape v13 does not model makes the fact MALFORMED too: the stream did
// carry that evidence, so it is not reported as absent.
//
// The fact is KNOWN only when every field schema v13 requires is present and
// the receipt's own v13 refusal checks accept it. An absent or blank field
// leaves it UNKNOWN naming exactly that field; a value the receipt rejects
// makes it MALFORMED with the receipt's reason. Nothing absent is invented.
func planAdmissionRefusal(line string, payload map[string]any, pstate runreceipt.Knownness, pdetail string,
	records []refusalRecord) *runreceipt.PlanAdmissionRefusal {
	const source = "event:" + planAdmissionRefusedKind + ".payload"
	if pstate == runreceipt.Malformed {
		return &runreceipt.PlanAdmissionRefusal{State: runreceipt.Malformed, Detail: pdetail}
	}
	term := readRefusal(payload, payloadDeclaration(line, payload, source), source)
	values := term.values
	sources := []string{source}
	var malformed []string
	if id := values["refusal_id"]; id.State == runreceipt.Known {
		for _, rec := range records {
			if rid := rec.values["refusal_id"]; rid.State != runreceipt.Known || rid.Text != id.Text {
				continue
			}
			used := false
			for _, key := range refusalKeys {
				have, from := values[key], rec.values[key]
				switch {
				case from.State == runreceipt.Malformed:
					malformed = append(malformed, fmt.Sprintf("%s in %s, a record of the same refusal %s bound by refusal_id: %s",
						key, rec.source, id.Text, from.Detail))
				case from.State != runreceipt.Known:
				case have.State == runreceipt.Unknown:
					values[key], used = from, true
				case have.State == runreceipt.Known && have.Text != from.Text:
					malformed = append(malformed, fmt.Sprintf("%s differs between %s and %s, records of the same refusal %s",
						key, have.Source, from.Source, id.Text))
				}
			}
			if used {
				sources = append(sources, rec.source+" (bound by refusal_id)")
			}
		}
	}

	p := &runreceipt.PlanAdmissionRefusal{}
	var missing []string
	for _, key := range refusalKeys {
		v := values[key]
		switch v.State {
		case runreceipt.Malformed:
			malformed = append(malformed, v.Detail)
			continue
		case runreceipt.Unknown:
			missing = append(missing, key)
			continue
		}
		switch key {
		case "plan_attempt_id":
			p.PlanAttemptID = v.Text
		case "refusal_id":
			p.RefusalID = v.Text
		case "refusal_class":
			p.Class = runreceipt.PlanAdmissionRefusalClass(v.Text)
		case "declaration":
			p.Declaration = v.Text
		case "reason":
			p.Reason = v.Text
		case "governing_evidence_id":
			p.GoverningEvidenceID = v.Text
		}
	}
	switch {
	case len(malformed) > 0:
		p.State = runreceipt.Malformed
		p.Detail = "the plan-admission refusal events carry fields in a shape schema v13 does not model: " + strings.Join(malformed, "; ")
		return p
	case len(missing) > 0:
		p.State = runreceipt.Unknown
		p.Detail = "the historical plan-admission refusal events do not carry " + strings.Join(missing, ", ") +
			", which schema v13 requires; they are not reconstructed from anything else"
		if values["refusal_id"].State != runreceipt.Known {
			p.Detail += ", and without a refusal_id no earlier refusal record is bound to the terminal"
		}
		return p
	}
	p.State, p.Source = runreceipt.Known, strings.Join(sources, ", completed from ")
	// The receipt owns the v13 refusal vocabulary and its canonical shapes; the
	// adapter asks it rather than keeping a second copy of either.
	probe := runreceipt.Receipt{Schema: runreceipt.SchemaVersion, Outcome: runreceipt.OutcomePlanAdmissionRefused, PlanAdmissionRefusal: p}
	_, issues := probe.Completeness()
	var rejected []string
	for _, m := range issues {
		if strings.HasPrefix(m, "plan_admission_refusal") {
			rejected = append(rejected, m)
		}
	}
	if len(rejected) > 0 {
		p.State, p.Source = runreceipt.Malformed, ""
		p.Detail = "the plan-admission refusal events carry values schema v13 rejects: " + strings.Join(rejected, "; ")
	}
	return p
}
