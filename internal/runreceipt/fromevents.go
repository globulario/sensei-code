package runreceipt

// Building a receipt from an event stream is a MIGRATION PATH, not the
// destination. The governor holds every one of these facts at the moment it
// acts; emitting them directly is the point of the schema. Until that wiring
// exists, the same typed, total reader can be built from the stream a run
// already writes -- which means the reader can be tested today against the real
// logs C4 and C5 left behind, rather than against specimens written from an
// author's model of the data.
//
// Totality is the contract: no syntactically valid JSON line may crash
// extraction. Every read of a nested value goes through a classifier that
// returns Known, Unknown, Malformed or Unsupported. The C5 defect --
// payload.provenance arriving as a string where an object was assumed -- is a
// MALFORMED classification here, not a panic.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// maxLine is generous: a single governed event can carry a whole candidate
// diff. A line beyond this is recorded as a diagnostic rather than dropped.
const maxLine = 16 << 20

// object reads a nested object without assuming it is one.
//
// This function exists because assuming it was one voided an experiment.
func object(m map[string]any, key string) (map[string]any, Knownness, string) {
	raw, ok := m[key]
	if !ok {
		return nil, Unknown, "absent"
	}
	if raw == nil {
		return nil, Unknown, "null"
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil, Malformed, fmt.Sprintf("%s is %T, not an object", key, raw)
	}
	return obj, Known, ""
}

// text reads a string without assuming it is one. A number or boolean is
// rendered rather than refused: it was reported, and saying what arrived is
// more useful than saying nothing did.
func text(m map[string]any, key string) Value {
	raw, ok := m[key]
	if !ok {
		return UnknownValue("absent")
	}
	switch v := raw.(type) {
	case nil:
		return UnknownValue("null")
	case string:
		return KnownValue(v)
	case float64, bool:
		return MalformedValue(fmt.Sprintf("%s is %T, not a string: %v", key, raw, raw))
	default:
		return MalformedValue(fmt.Sprintf("%s is %T, not a string", key, raw))
	}
}

// nested reads a string one level down, classifying every way that can fail.
func nested(m map[string]any, objKey, key string) Value {
	obj, state, detail := object(m, objKey)
	switch state {
	case Unknown:
		return UnknownValue(objKey + " " + detail)
	case Malformed:
		return MalformedValue(detail)
	}
	return text(obj, key)
}

// FromEvents builds a receipt from a governed run's JSONL event stream.
//
// It never returns an error and never panics on any input: what it cannot model
// becomes a Diagnostic and an explicitly Unknown, Malformed or Unsupported
// value. A reader that crashes on unexpected input converts an observation into
// an outage, and the observation is the thing we were trying to keep.
func FromEvents(r io.Reader) Receipt {
	rec := Receipt{Schema: SchemaVersion, Outcome: OutcomeUnknown}
	rec.GovernorCommit = UnknownValue("no event carried it")
	rec.GovernorBinarySHA256 = UnknownValue("not present in the event stream; the governor must emit it")
	rec.ServingProducer = UnknownValue("not present in the event stream; the governor must emit it")
	rec.BaseCommit = UnknownValue("no event carried it")
	rec.PlanDigest = UnknownValue("no event carried it")
	rec.GraphDigest = UnknownValue("no event carried it")
	rec.CandidateCommit = UnknownValue("the event stream names no candidate commit")
	rec.CandidateTree = UnknownValue("the event stream names no candidate tree")
	rec.CandidateFirstParent = UnknownValue("the event stream names no candidate first parent")
	rec.CandidateDigest = UnknownValue("no candidate digest observed")
	rec.ReviewerProvider = UnknownValue("no reviewer was assigned")
	rec.ReviewerExecutable = UnknownValue("not present in the event stream; the governor must emit it")
	rec.ReviewVerdict = UnknownValue("no bounded verdict")
	rec.ReviewedDigest = UnknownValue("no bounded verdict")
	rec.Terminal = UnknownValue("no terminal event observed")

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), maxLine)
	line := 0
	unparsed, unknownKinds := 0, map[string]int{}
	var pending *Attempt

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
		if pstate != Known {
			payload = map[string]any{}
			if pstate == Malformed {
				rec.Diagnostics = append(rec.Diagnostics, fmt.Sprintf("line %d (%s): %s", line, kind, pdetail))
			}
		}

		switch kind {
		case "sensei.result":
			binding, state, _ := object(payload, "binding")
			if state == Known {
				if v := text(binding, "revision"); v.State == Known && rec.BaseCommit.State != Known {
					rec.BaseCommit = v
					rec.GovernorCommit = v
				}
			}
			auth, state, _ := object(payload, "graph_authority")
			if state == Known {
				if v := text(auth, "live_store_graph_digest_sha256"); v.State == Known && rec.GraphDigest.State != Known {
					rec.GraphDigest = v
				}
			}
		case "plan.proposed":
			if v := text(payload, "plan_digest"); v.State == Known {
				rec.PlanDigest = v
			}
		case "agent.role.assigned":
			if role := text(payload, "role"); role.State == Known && role.Text != "reviewer" {
				break
			}
			flush()
			pending = &Attempt{
				Provider: text(payload, "provider"),
				Verdict:  UnknownValue("this attempt produced no verdict"),
				Digest:   UnknownValue("this attempt produced no verdict"),
			}
			if pending.Provider.State == Known {
				rec.ReviewerProvider = pending.Provider
			}
			if v := text(payload, "candidate"); v.State == Known {
				rec.CandidateDigest = v
			}
		case "review.completed":
			verdict := text(payload, "decision")
			digest := nested(payload, "provenance", "candidate_digest")
			provider := nested(payload, "provenance", "provider")
			if pending == nil {
				pending = &Attempt{Provider: UnknownValue("no role assignment preceded this verdict")}
			}
			pending.Delivered = true
			pending.Verdict = verdict
			pending.Digest = digest
			if provider.State == Known {
				pending.Provider = provider
				rec.ReviewerProvider = provider
			}
			rec.ReviewVerdict = verdict
			rec.ReviewedDigest = digest
			flush()
		case "workflow.completed", "workflow.failed", "candidate.not_auditable":
			rec.Terminal = KnownValue(kind)
		case "candidate.resolved":
			if ev, state, _ := object(payload, "evidence"); state == Known {
				if v := text(ev, "base_sha"); v.State == Known {
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
		rec.Diagnostics = append(rec.Diagnostics, fmt.Sprintf("kind %s not modelled by %s (%d event(s))", k, SchemaVersion, n))
	}
	rec.Outcome = outcomeFrom(rec)
	return rec
}

// modelled reports whether a kind is one this schema version deliberately
// ignores rather than one it has never heard of. Silence about a kind we chose
// not to read is different from silence about a kind we did not know existed.
func modelled(kind string) bool {
	switch kind {
	case "output", "status", "task.created", "mode.selected", "context.consulted",
		"candidate.changed", "candidate.audited", "validation.run", "routine.classified",
		"review.started", "review.finding", "change.reported", "decision.recorded",
		"agent.started", "agent.finished", "inspection.reported", "handoff.created",
		"authority.required", "authority.resolved", "review.contradiction",
		"architect.reconciliation", "candidate.artifact_excluded":
		return true
	}
	return false
}

// outcomeFrom reads the run axis, and only the run axis. It never consults
// completeness: a record that is missing a required field still has an outcome,
// and conflating the two is the collapse this package exists to prevent.
func outcomeFrom(r Receipt) Outcome {
	if r.ReviewVerdict.State == Known {
		if strings.EqualFold(r.ReviewVerdict.Text, "accept") {
			return OutcomeAccepted
		}
		return OutcomeRefused
	}
	if r.Terminal.State == Known {
		if r.Terminal.Text == "workflow.completed" {
			return OutcomeUnreviewed
		}
		return OutcomeFailed
	}
	return OutcomeUnknown
}
