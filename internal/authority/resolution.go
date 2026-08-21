package authority

// Durable Level-3 resolutions.
//
// When a human answers an authority question, two different things happen and
// they must not be conflated. The answer is immediately authoritative for this
// run — the human said it, and that settles what happens next. Whether it
// becomes project knowledge that makes the same question certifiable next time
// is a separate question, and it is Sensei's to answer, not this program's.
//
// So a resolution is recorded with its provenance and then submitted to
// Sensei's own review queue as a typed proposal. It is never written into the
// graph here, and there is deliberately no local authority store that other
// code consults as though it were canon. A Sensei Code file asserting what the
// project believes would be a second source of truth with none of the
// validation, review or promotion that makes the first one worth trusting —
// and the failure mode is silent, because such a file always agrees with
// whoever wrote it.
//
// The distinction shows up in the naming: Applied means the run continued,
// Durable means Sensei has it. Only the second is a claim about the future, and
// it is never inferred — it requires an accepted status and a candidate path
// back from Sensei.

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// PersistenceState is what became of the attempt to give a resolution to
// Sensei.
type PersistenceState string

const (
	// Proposed means Sensei validated the entry and wrote it to its review
	// queue. It is durable evidence, and it is not yet canonical: a human or CI
	// step promotes it into the graph.
	Proposed PersistenceState = "proposed to Sensei review queue"
	// Unsupported means this Sensei cannot accept proposals at all, typically
	// because the server was started without the propose capability.
	Unsupported PersistenceState = "unsupported by this Sensei"
	// Rejected means Sensei read the proposal and refused it, usually because
	// it failed the contract-first validation.
	Rejected PersistenceState = "rejected by Sensei"
	// Failed means the attempt itself did not complete.
	Failed PersistenceState = "failed"
)

// Resolution is one Level-3 human decision, bound to the state it was made
// against and to what became of it.
type Resolution struct {
	TaskID    string    `json:"task_id"`
	SessionID string    `json:"session_id"`
	Domain    string    `json:"domain,omitempty"`
	BaseSHA   string    `json:"base_sha,omitempty"`
	DecidedAt time.Time `json:"decided_at"`

	// Question is what the human was actually asked.
	Question string `json:"question"`
	// Condition is the certifiability condition that caused the escalation. It
	// is the part worth keeping: the answer only means something in the context
	// of the question the graph could not settle.
	Condition string `json:"condition"`
	// OptionID and OptionLabel are what they chose.
	OptionID    string `json:"option_id"`
	OptionLabel string `json:"option_label"`
	// Scope is the set of files the plan the human saw would touch.
	//
	// Without it an answer is identified by the condition alone, and conditions
	// are properties of a region rather than of a plan -- "Sensei requires
	// approval for this change class: human_approval_required (blast radius
	// security)" says nothing about what is being changed. A yes given for one
	// plan then authorized every later plan in the same task that reached the
	// same gate, including one touching entirely different files.
	Scope []string `json:"scope,omitempty"`
	// Outcome is what the choice does, carried from the option rather than read
	// back out of its label.
	Outcome Outcome `json:"outcome"`

	// State, CandidatePath, NodeIDs and Detail record what Sensei did with it.
	State         PersistenceState `json:"state"`
	CandidatePath string           `json:"candidate_path,omitempty"`
	NodeIDs       []string         `json:"node_ids,omitempty"`
	Detail        string           `json:"detail,omitempty"`
}

// Durable reports whether Sensei actually holds this resolution.
//
// It requires both an accepted state and the path Sensei says it wrote, because
// "accepted" alone is a status and a path is an artifact. A claim of durable
// project knowledge is exactly the kind of claim that must rest on the artifact.
func (r Resolution) Durable() bool {
	return r.State == Proposed && strings.TrimSpace(r.CandidatePath) != ""
}

// Summary is the line the human reads, and it never overstates what happened.
//
// The wording matters more than it looks. "Recorded" would imply the project
// has learned something; until a promotion step runs, it has not. Saying so
// plainly is the difference between a system that is honest about its memory
// and one that claims learned autonomy it does not have.
func (r Resolution) Summary() string {
	applied := fmt.Sprintf("resolution applied to this run: %s", r.OptionLabel)
	switch {
	case r.Durable():
		return applied + fmt.Sprintf("\nproject-governance persistence: proposed to Sensei for review (%s) — not canonical until promoted", r.CandidatePath)
	case r.State == Unsupported:
		return applied + "\nproject-governance persistence: unsupported / pending upstream contract — this answer will be asked again"
	case r.State == Rejected:
		return applied + "\nproject-governance persistence: Sensei rejected the proposal (" + r.Detail + ") — this answer will be asked again"
	default:
		detail := r.Detail
		if detail == "" {
			detail = "no reason reported"
		}
		return applied + "\nproject-governance persistence: failed (" + detail + ") — this answer will be asked again"
	}
}

// Caller is the narrow Sensei surface persistence needs.
type Caller interface {
	CallTool(name string, args map[string]any) (ToolResult, error)
}

// ToolResult mirrors the MCP result shape without importing the client, so this
// package stays testable and free of a dependency cycle.
type ToolResult struct {
	Structured map[string]any
	Text       string
	IsError    bool
}

// ErrNoQuestion refuses to propose a resolution with nothing to resolve.
var ErrNoQuestion = errors.New("a resolution needs the question it answered")

// Persist submits the resolution to Sensei's review queue and returns it with
// the outcome filled in.
//
// The proposal is deliberately kind=contract_unknown with a proposed_contract.
// That is the honest typing: a Level-3 escalation happens precisely because the
// graph had no governing contract for the question, so what the human supplied
// is a proposed contract awaiting review — not an invariant this program is
// entitled to assert on the project's behalf.
//
// Every failure path leaves a state that reads as not-durable. There is no
// branch here that can produce Durable() without Sensei having said so.
func Persist(caller Caller, r Resolution) Resolution {
	if strings.TrimSpace(r.Question) == "" && strings.TrimSpace(r.Condition) == "" {
		r.State = Failed
		r.Detail = ErrNoQuestion.Error()
		return r
	}
	if caller == nil {
		r.State = Unsupported
		r.Detail = "no Sensei client available"
		return r
	}

	args := map[string]any{
		"kind":  "contract_unknown",
		"title": title(r),
		"proposed_contract": fmt.Sprintf(
			"When %s, the human authority for this repository decided: %s.",
			strings.TrimSpace(firstNonEmpty(r.Condition, r.Question)), strings.TrimSpace(r.OptionLabel)),
		"description": fmt.Sprintf(
			"Level-3 authority resolution.\nQuestion: %s\nCertifiability condition: %s\nChosen: %s (option %s)\nTask: %s\nDecided at: %s",
			r.Question, r.Condition, r.OptionLabel, r.OptionID, r.TaskID, r.DecidedAt.UTC().Format(time.RFC3339)),
		"evidence": []string{
			"sensei-code task " + r.TaskID,
			"session " + r.SessionID,
			"certifiability condition: " + r.Condition,
		},
	}
	if r.Domain != "" {
		args["domain"] = r.Domain
	}
	if r.BaseSHA != "" {
		args["evidence"] = append(args["evidence"].([]string), "base commit "+r.BaseSHA)
	}

	result, err := caller.CallTool("awareness_propose", args)
	if err != nil {
		r.State = classifyFailure(err)
		r.Detail = err.Error()
		return r
	}
	return apply(r, result)
}

// apply reads Sensei's answer. An answer it cannot read is not an acceptance.
func apply(r Resolution, result ToolResult) Resolution {
	if result.IsError {
		r.State = Rejected
		r.Detail = firstNonEmpty(result.Text, "Sensei reported a tool error")
		return r
	}
	if len(result.Structured) == 0 {
		r.State = Failed
		r.Detail = "Sensei returned no structured propose result, so acceptance cannot be verified"
		return r
	}
	encoded, err := json.Marshal(result.Structured)
	if err != nil {
		r.State = Failed
		r.Detail = "propose result could not be re-encoded: " + err.Error()
		return r
	}
	var payload struct {
		Status           string   `json:"status"`
		Accepted         bool     `json:"accepted"`
		CandidatePath    string   `json:"candidate_path"`
		NodeIDs          []string `json:"node_ids"`
		ValidationErrors []string `json:"validation_errors"`
		Note             string   `json:"note"`
	}
	if err := json.Unmarshal(encoded, &payload); err != nil {
		r.State = Failed
		r.Detail = "propose result did not match the published contract: " + err.Error()
		return r
	}

	r.NodeIDs = payload.NodeIDs
	switch {
	case payload.Accepted && strings.TrimSpace(payload.CandidatePath) != "":
		r.State = Proposed
		r.CandidatePath = payload.CandidatePath
		r.Detail = payload.Note
	case payload.Accepted:
		// Accepted without an artifact. Reported as not durable, because the
		// artifact is the thing a later reader can actually check.
		r.State = Failed
		r.Detail = "Sensei accepted the proposal but named no candidate path, so the write cannot be verified"
	case len(payload.ValidationErrors) != 0:
		r.State = Rejected
		r.Detail = strings.Join(payload.ValidationErrors, "; ")
	default:
		r.State = Rejected
		r.Detail = firstNonEmpty(payload.Note, payload.Status, "Sensei did not accept the proposal")
	}
	return r
}

// classifyFailure separates "this Sensei cannot do this" from "the attempt
// broke", so the human is told which of the two they are looking at.
func classifyFailure(err error) PersistenceState {
	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, "unavailable"),
		strings.Contains(text, "not enabled"),
		strings.Contains(text, "unknown tool"),
		strings.Contains(text, "propose disabled"):
		return Unsupported
	default:
		return Failed
	}
}

// title names one decision, not one kind of decision.
//
// It used to be a pure function of the condition, and the condition is a
// recurring sentence -- "Sensei reported blind spots in the planned region:
// anchor with severity=critical, file path under high-risk directory" fires on
// every run that touches a protected file. Sensei derives the proposal's
// filename from this title, so two different answers to the same recurring
// question produced the same slug and the second replaced the first on disk.
//
// That is not a cosmetic collision. It was observed overwriting a decision of
// "preserve current human-owned intent and require another design" with a later
// "authorize the architectural change" -- the opposite answer, to a question
// asked about different work, with nothing left saying the first had ever been
// given. A record of human authority that keeps only the most recent answer
// cannot support the one claim it exists to make.
//
// The task is what makes a decision unique: it is minted per run, it is already
// recorded in the description and the evidence, and it is what distinguishes
// two answers to the same standing question.
func title(r Resolution) string {
	base := strings.TrimSpace(r.Condition)
	if base == "" {
		base = strings.TrimSpace(r.Question)
	}
	if len(base) > 90 {
		base = base[:90]
	}
	// A decision with no task cannot be told apart from the next one, so the
	// condition alone is used and the collision remains possible. That is
	// preferable to inventing an identity: a fabricated discriminator would
	// make two records look distinct without either being traceable.
	if task := strings.TrimSpace(r.TaskID); task != "" {
		return "Human authority resolution (" + task + "): " + base
	}
	return "Human authority resolution: " + base
}

// ScopeKey identifies the plan an answer was given about, order-insensitively.
//
// An empty scope is its own key rather than a wildcard: a decision recorded
// without one cannot be shown to be about the current plan, and treating
// "unknown" as "matches anything" is how the reuse this field exists to stop
// would return.
func ScopeKey(scope []string) string {
	if len(scope) == 0 {
		return "(no scope recorded)"
	}
	seen := make(map[string]bool, len(scope))
	normalized := make([]string, 0, len(scope))
	for _, f := range scope {
		f = strings.TrimSpace(f)
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		normalized = append(normalized, f)
	}
	if len(normalized) == 0 {
		return "(no scope recorded)"
	}
	sort.Strings(normalized)
	return strings.Join(normalized, "\x00")
}

// Key is the identity of one answered question: what was asked, about what.
func (r Resolution) Key() string {
	return strings.TrimSpace(r.Condition) + "\x00" + ScopeKey(r.Scope)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
