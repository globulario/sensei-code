package sensei

// Typed adapters over Sensei's public structured contracts.
//
// Governance transitions consume the values in this file. A tool result's
// content[].text is presentation evidence only. No gate may depend on
// firstText, on substring matching, or on a reviewer's reading of a verdict:
// the reviewer judges the change, Sensei judges certifiability, and a model
// that can restate a verdict in prose must never be the thing that decides
// whether the verdict was negative.
//
// Every decoder here fails closed. A required result that is missing, empty,
// structurally malformed, or that carries an enum member this build does not
// recognise does not permit progression. Sensei's enums are closed sets, but a
// Sensei newer than this binary can return a member this code has never seen,
// and the only safe reading of an unrecognised verdict is "not certified".
// Recognising the affirmative values and refusing everything else means a new
// negative state added upstream fails safe here on the day it ships, rather
// than being silently treated as a pass until someone notices.
//
// The field names below are Sensei's, taken from its published structured
// output. Sensei Code does not invent alternative semantics for them.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// PreflightStatus is the status field of awareness_preflight.
type PreflightStatus string

const (
	PreflightOK          PreflightStatus = "PREFLIGHT_STATUS_OK"
	PreflightDegraded    PreflightStatus = "PREFLIGHT_STATUS_DEGRADED"
	PreflightEmpty       PreflightStatus = "PREFLIGHT_STATUS_EMPTY"
	PreflightUnspecified PreflightStatus = "PREFLIGHT_STATUS_UNSPECIFIED"
)

// AuditDecision is the canonical verdict of awareness_audit_diff.
type AuditDecision string

const (
	AuditPass         AuditDecision = "pass"
	AuditReview       AuditDecision = "review"
	AuditBlock        AuditDecision = "block"
	AuditCannotVerify AuditDecision = "cannot_verify"
)

// AuditAvailability reports whether the graph and evaluation context were
// complete enough for the audit to mean anything.
type AuditAvailability string

const (
	AuditAvailable             AuditAvailability = "available"
	AuditAvailabilityCannot    AuditAvailability = "cannot_verify"
	AuditAvailabilityUnsupport AuditAvailability = "unsupported"
)

// GraphFreshness is the graph_freshness_state carried on every authority block.
type GraphFreshness string

const (
	GraphCurrent     GraphFreshness = "GRAPH_FRESHNESS_STATE_CURRENT"
	GraphStale       GraphFreshness = "GRAPH_FRESHNESS_STATE_STALE"
	GraphEmpty       GraphFreshness = "GRAPH_FRESHNESS_STATE_EMPTY"
	GraphMadeUp      GraphFreshness = "GRAPH_FRESHNESS_STATE_MADE_UP"
	GraphUnknown     GraphFreshness = "GRAPH_FRESHNESS_STATE_UNKNOWN"
	GraphCheckError  GraphFreshness = "GRAPH_FRESHNESS_STATE_CHECK_ERROR"
	GraphUnspecified GraphFreshness = "GRAPH_FRESHNESS_STATE_UNSPECIFIED"
)

// SeedState is the seed_state carried on every authority block.
type SeedState string

const (
	SeedCurrent     SeedState = "SEED_STATE_CURRENT"
	SeedStale       SeedState = "SEED_STATE_STALE"
	SeedUnstamped   SeedState = "SEED_STATE_UNSTAMPED"
	SeedUnspecified SeedState = "SEED_STATE_UNSPECIFIED"
)

// CompositionState is the composition_state of sensei_workspace_status.
type CompositionState string

const (
	CompositionComplete    CompositionState = "complete"
	CompositionPartial     CompositionState = "partial"
	CompositionUnavailable CompositionState = "unavailable"
)

// ContractError is a refusal to read a required Sensei result as a verdict.
//
// It is deliberately distinct from a transport error. A transport error means
// Sensei was not reached; a ContractError means Sensei was reached and what
// came back cannot be certified. Both stop the run, but only the second one
// means the governed surface itself is wrong, and a caller that wants to say
// so precisely needs to be able to tell them apart.
type ContractError struct {
	Surface string
	Reason  string
}

func (e *ContractError) Error() string {
	return fmt.Sprintf("Sensei %s is not certifiable: %s", e.Surface, e.Reason)
}

// Authority is the authority block Sensei attaches to its governed surfaces.
// It is the same shape on preflight and on metadata.
type Authority struct {
	Authoritative        bool           `json:"authoritative"`
	Verdict              string         `json:"verdict"`
	State                string         `json:"state"`
	GraphFreshnessState  GraphFreshness `json:"graph_freshness_state"`
	GraphFreshnessDetail string         `json:"graph_freshness_detail"`
	SeedState            SeedState      `json:"seed_state"`
	BuildProvenanceState string         `json:"build_provenance_state"`
	GraphBuildCommit     string         `json:"graph_build_commit"`
	SourceRepoCommit     string         `json:"source_repo_commit"`
}

// Certifiable reports whether Sensei vouches for its own answers right now.
//
// This is the single computation the authority router is meant to escalate on:
// it is a property of the graph, not an opinion of a model. All three
// conditions must hold affirmatively. A stale graph that still answers
// confidently is the failure this exists to catch — it produces fluent,
// specific, wrong invariants, which is worse than producing nothing.
func (a Authority) Certifiable() bool {
	return a.Authoritative &&
		a.GraphFreshnessState == GraphCurrent &&
		a.SeedState == SeedCurrent
}

// Diagnostic explains a negative certifiability in one line.
func (a Authority) Diagnostic() string {
	var reasons []string
	if !a.Authoritative {
		verdict := a.Verdict
		if verdict == "" {
			verdict = "not authoritative"
		}
		reasons = append(reasons, "authority "+verdict)
	}
	if a.GraphFreshnessState != GraphCurrent {
		reason := "graph " + humanState(string(a.GraphFreshnessState), "GRAPH_FRESHNESS_STATE_")
		if a.GraphFreshnessDetail != "" {
			reason += " (" + a.GraphFreshnessDetail + ")"
		}
		reasons = append(reasons, reason)
	}
	if a.SeedState != SeedCurrent {
		reasons = append(reasons, "seed "+humanState(string(a.SeedState), "SEED_STATE_"))
	}
	if len(reasons) == 0 {
		return ""
	}
	return strings.Join(reasons, "; ")
}

// humanState turns SCREAMING_ENUM_MEMBER into readable text for a human, and
// says so plainly when the value is missing entirely.
func humanState(value, prefix string) string {
	if strings.TrimSpace(value) == "" {
		return "unreported"
	}
	return strings.ToLower(strings.TrimPrefix(value, prefix))
}

// Invariant is one graph invariant implicated by a preflight.
type Invariant struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Severity string `json:"severity"`
	Status   string `json:"status"`
}

// PreflightDecision is the typed form of awareness_preflight.
type PreflightDecision struct {
	Status           PreflightStatus `json:"status"`
	RiskClass        string          `json:"risk_class"`
	Confidence       string          `json:"confidence"`
	Authority        Authority       `json:"authority"`
	DirectInvariants []Invariant     `json:"direct_invariants"`
	RequiredActions  []string        `json:"required_actions"`
	BlindSpots       []string        `json:"blind_spots"`
}

// Permits is the strict, file-scoped gate: Sensei affirmatively cleared this
// specific set of files. Only PREFLIGHT_STATUS_OK does.
//
// Use this once the files are known. Applied to an unscoped query it is wrong
// in a way that looks right — see PermitsStart.
func (p PreflightDecision) Permits() bool {
	return p.Status == PreflightOK && p.Authority.Certifiable()
}

// PermitsStart is the gate for the beginning of a task, before a plan exists
// and therefore before any file list can be named.
//
// The distinction is not a softening, it is a correction. Sensei answers an
// unscoped preflight with PREFLIGHT_STATUS_EMPTY and UNKNOWN_IMPACT, which is a
// true statement about the question asked: no files were named, so no impact
// was found. Reading that as "Sensei refuses this task" attributes to Sensei a
// verdict it never gave, and blocks every task in the product on the strength
// of the caller's own empty query. Absence of evidence is not safety, but it is
// equally not a refusal, and conflating the two produces a gate that fails
// closed on literally everything — which is indistinguishable from broken.
//
// What is genuinely knowable at task start is whether Sensei can vouch for its
// own answers at all. That is the authority block, and it is required here in
// full. A stated degradation is also a real negative and is refused. The
// file-scoped judgement is not skipped, only deferred: it lands on the diff
// audit at the end of the candidate, where the files actually exist.
func (p PreflightDecision) PermitsStart() bool {
	if !p.Authority.Certifiable() {
		return false
	}
	switch p.Status {
	case PreflightOK, PreflightEmpty:
		return true
	default:
		return false
	}
}

// Diagnostic explains a negative preflight in one line.
func (p PreflightDecision) Diagnostic() string {
	var reasons []string
	if p.Status != PreflightOK {
		reasons = append(reasons, "preflight "+humanState(string(p.Status), "PREFLIGHT_STATUS_"))
	}
	if detail := p.Authority.Diagnostic(); detail != "" {
		reasons = append(reasons, detail)
	}
	return strings.Join(reasons, "; ")
}

// AuditFinding is one violation the diff audit reported.
type AuditFinding struct {
	ID          string `json:"id"`
	File        string `json:"file"`
	Disposition string `json:"disposition"`
	Detail      string `json:"detail"`
	Message     string `json:"message"`
}

// DiffAuditDecision is the typed form of awareness_audit_diff.
type DiffAuditDecision struct {
	Schema          string            `json:"schema"`
	Digest          string            `json:"digest"`
	InputDiffDigest string            `json:"input_diff_digest"`
	Availability    AuditAvailability `json:"availability"`
	Decision        AuditDecision     `json:"decision"`
	ExpectedHead    string            `json:"expected_head"`
	Domain          string            `json:"domain"`
	GraphCommit     string            `json:"graph_commit"`
	Findings        []AuditFinding    `json:"findings"`
	ReasonCodes     []string          `json:"reason_codes"`
	Limitations     []string          `json:"limitations"`
}

// ReviewerMayAccept reports whether a reviewer's acceptance is allowed to
// stand, which is a different question from whether the audit passed.
//
// Sensei's four verdicts mean different things about who decides. pass and
// review both leave room for judgement — review means precisely "a reviewer
// must look at this". block does not: it is Sensei's refusal, and a reviewer
// accepting over it would make the reviewer the effective judge of an
// architectural audit, which is the authority inversion this whole gate
// exists to prevent. cannot_verify is not a pass either; an audit that could
// not be performed is an unanswered question, and accepting on the strength
// of an unanswered question is how unverified changes acquire a receipt.
func (d DiffAuditDecision) ReviewerMayAccept() bool {
	switch d.Decision {
	case AuditPass, AuditReview:
		return d.Availability == AuditAvailable
	default:
		return false
	}
}

// Blocks reports Sensei's outright refusal, as distinct from an audit that
// merely could not be completed.
func (d DiffAuditDecision) Blocks() bool { return d.Decision == AuditBlock }

// Actionable reports whether a worker could plausibly fix what the audit
// objected to.
//
// A blocking finding names something in the candidate: the worker changes the
// code and the next audit differs. An audit that could not run names something
// about the environment -- the graph was unreachable, the snapshot shifted, the
// repository context was missing -- and no edit to the candidate changes that.
//
// The distinction is not pedantry, it is the difference between a review loop
// that converges and one that burns every cycle on identical work. A real run
// produced the same 3078-byte diff four times because an unverifiable audit was
// handed back as though it were a finding to address.
func (d DiffAuditDecision) Actionable() bool {
	switch d.Decision {
	case AuditBlock, AuditReview:
		return true
	case AuditCannotVerify:
		return false
	default:
		return false
	}
}

// Diagnostic explains why acceptance is not available, naming the findings
// that carry the refusal so the worker gets something it can act on.
func (d DiffAuditDecision) Diagnostic() string {
	var parts []string
	switch d.Decision {
	case AuditBlock:
		parts = append(parts, "Sensei audit blocks this candidate")
	case AuditCannotVerify:
		parts = append(parts, "Sensei audit could not verify this candidate")
	case AuditPass, AuditReview:
		if d.Availability != AuditAvailable {
			parts = append(parts, "Sensei audit context is "+string(d.Availability))
		}
	default:
		parts = append(parts, fmt.Sprintf("Sensei audit returned unrecognised decision %q", string(d.Decision)))
	}
	for _, f := range d.Findings {
		if f.Disposition != "block" {
			continue
		}
		detail := f.Detail
		if detail == "" {
			detail = f.Message
		}
		label := strings.TrimSpace(f.File + " " + f.ID)
		if label == "" {
			label = "finding"
		}
		parts = append(parts, strings.TrimSpace(label+": "+detail))
	}
	if len(d.ReasonCodes) != 0 {
		parts = append(parts, "reason codes: "+strings.Join(d.ReasonCodes, ", "))
	}
	// Limitations carry the sentence that actually explains the verdict --
	// which file, which query, which mismatch. Without them a cannot_verify
	// reaches the worker as the bare token "graph_unavailable", which is not
	// something anyone can act on. One real run spent four review cycles
	// producing a byte-identical diff because that was all it had been told.
	if len(d.Limitations) != 0 {
		parts = append(parts, strings.Join(d.Limitations, "; "))
	}
	return strings.Join(parts, " · ")
}

// WorkspaceStatus is the typed form of sensei_workspace_status.
type WorkspaceStatus struct {
	SchemaVersion    string           `json:"schema_version"`
	CompositionState CompositionState `json:"composition_state"`
	CoverageState    string           `json:"coverage_state"`
	Binding          struct {
		RepositoryDomain string `json:"repository_domain"`
		Repository       string `json:"repository"`
	} `json:"binding"`
	RepositoryDomainSource string     `json:"repository_domain_source"`
	GraphAuthority         *Authority `json:"graph_authority"`
	Limitations            []struct {
		Code   string `json:"code"`
		Detail string `json:"detail"`
	} `json:"limitations"`
}

// Permits reports whether the workspace identity was fully composed. A partial
// composition means Sensei could not establish some governed fact about this
// repository, and governing a candidate whose identity is only partly known is
// governing something other than what will be shipped.
func (w WorkspaceStatus) Permits() bool {
	return w.CompositionState == CompositionComplete
}

// Diagnostic explains an incomplete workspace composition.
func (w WorkspaceStatus) Diagnostic() string {
	if w.Permits() {
		return ""
	}
	state := string(w.CompositionState)
	if strings.TrimSpace(state) == "" {
		state = "unreported"
	}
	parts := []string{"workspace composition " + state}
	for _, l := range w.Limitations {
		detail := strings.TrimSpace(l.Code + ": " + l.Detail)
		if detail != ":" && detail != "" {
			parts = append(parts, detail)
		}
	}
	return strings.Join(parts, " · ")
}

// DecodePreflight reads a preflight result, failing closed.
func DecodePreflight(r ToolResult) (PreflightDecision, error) {
	var out PreflightDecision
	if err := decodeStructured("preflight", r, &out); err != nil {
		return PreflightDecision{}, err
	}
	if strings.TrimSpace(string(out.Status)) == "" {
		return PreflightDecision{}, &ContractError{Surface: "preflight", Reason: "result carried no status field"}
	}
	return out, nil
}

// DecodeDiffAudit reads a diff audit result, failing closed.
func DecodeDiffAudit(r ToolResult) (DiffAuditDecision, error) {
	var out DiffAuditDecision
	if err := decodeStructured("diff audit", r, &out); err != nil {
		return DiffAuditDecision{}, err
	}
	if strings.TrimSpace(string(out.Decision)) == "" {
		return DiffAuditDecision{}, &ContractError{Surface: "diff audit", Reason: "result carried no decision field"}
	}
	return out, nil
}

// DecodeWorkspaceStatus reads a workspace status result, failing closed.
func DecodeWorkspaceStatus(r ToolResult) (WorkspaceStatus, error) {
	var out WorkspaceStatus
	if err := decodeStructured("workspace status", r, &out); err != nil {
		return WorkspaceStatus{}, err
	}
	if strings.TrimSpace(string(out.CompositionState)) == "" {
		return WorkspaceStatus{}, &ContractError{Surface: "workspace status", Reason: "result carried no composition_state field"}
	}
	return out, nil
}

// decodeStructured requires a structured payload and rejects anything that
// cannot be read as one.
//
// The empty check is the load-bearing one. An MCP result with prose in
// content[].text and nothing in structuredContent looks entirely healthy to a
// human reading the transcript, and decodes without error into a zero-valued
// struct whose every enum field is "" — which, before this file existed, would
// have been carried forward as a verdict. Requiring the structured payload is
// what makes the zero value unreachable rather than merely unlikely.
func decodeStructured(surface string, r ToolResult, out any) error {
	if r.IsError {
		reason := strings.TrimSpace(refusalReason(r))
		if reason == "" {
			reason = "Sensei reported a tool error"
		}
		return &ContractError{Surface: surface, Reason: reason}
	}
	if len(r.Structured) == 0 {
		return &ContractError{Surface: surface, Reason: "result carried no structured content"}
	}
	raw, err := json.Marshal(r.Structured)
	if err != nil {
		return &ContractError{Surface: surface, Reason: "structured content could not be re-encoded: " + err.Error()}
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return &ContractError{Surface: surface, Reason: "structured content did not match the published contract: " + err.Error()}
	}
	return nil
}

// Discrepancy reports a disagreement between a structured verdict and the
// prose Sensei rendered beside it.
//
// The structured contract always wins; this exists only so a disagreement is
// visible instead of silent. It matters because the prose is what a human
// skims and what a model is handed, so prose that says "pass" beside a
// structured block is a live way for everyone to believe the wrong thing at
// once. Matching is restricted to the closed set of verdict tokens, which is
// why this is a diagnostic rather than a gate: it can only ever observe that
// two known values differ, never decide what should happen next.
func Discrepancy(surface, text string, structured string, known []string) string {
	lowered := strings.ToLower(text)
	var found []string
	for _, token := range known {
		if token == structured {
			continue
		}
		if strings.Contains(lowered, strings.ToLower(token)) {
			found = append(found, token)
		}
	}
	if len(found) == 0 {
		return ""
	}
	sort.Strings(found)
	return fmt.Sprintf("%s text mentions %s while the structured verdict is %q; the structured verdict governs",
		surface, strings.Join(found, ", "), structured)
}

// AuditDecisionTokens is the closed set of audit verdicts, for Discrepancy.
func AuditDecisionTokens() []string {
	return []string{string(AuditPass), string(AuditReview), string(AuditBlock), string(AuditCannotVerify)}
}

// AuthorityOf extracts the authority block from any Sensei result that carries
// one, reporting whether there was one to extract.
//
// Absence of an authority block is not staleness — several surfaces do not
// publish one — so the boolean matters: a caller must be able to tell "Sensei
// said its graph is stale" from "this surface does not say".
func AuthorityOf(r ToolResult) (Authority, bool) {
	if len(r.Structured) == 0 {
		return Authority{}, false
	}
	raw, ok := r.Structured["authority"]
	if !ok {
		// Workspace status publishes the same block under a different key.
		raw, ok = r.Structured["graph_authority"]
		if !ok {
			return Authority{}, false
		}
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return Authority{}, false
	}
	var a Authority
	if err := json.Unmarshal(encoded, &a); err != nil {
		return Authority{}, false
	}
	// A block present but carrying no freshness signal states nothing.
	if a.GraphFreshnessState == "" && a.SeedState == "" && a.Verdict == "" {
		return Authority{}, false
	}
	return a, true
}

// ProvenEmpty reports whether Sensei affirmatively answered "there is nothing
// here", as distinct from not answering.
//
// The difference is the whole point of having both states. "Sensei has no
// invariants for this file" is a finding — it means the region is uncovered and
// a human owns it. "Sensei did not answer" is not a finding at all. Collapsing
// the two lets an unanswered question be reported as a clean bill of health,
// which is the most expensive mistake this system can make, because it is the
// one that looks like good news.
func ProvenEmpty(r ToolResult) (string, bool) {
	if len(r.Structured) == 0 {
		return "", false
	}
	if status, ok := r.Structured["status"].(string); ok && PreflightStatus(status) == PreflightEmpty {
		return "Sensei reports no impact for this scope", true
	}
	if coverage, ok := r.Structured["coverage_state"].(string); ok && coverage == "COVERAGE_STATE_EMPTY" {
		return "Sensei reports no coverage for this scope", true
	}
	return "", false
}
