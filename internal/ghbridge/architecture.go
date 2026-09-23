package ghbridge

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/globulario/sensei-code/internal/reviewartifact"
	"github.com/globulario/sensei-code/internal/roles"
)

const (
	architectureRequestMarker  = "[sensei-code:architecture-request]"
	architectureResponseMarker = "[sensei-code:architecture]"
	// architectureRefusalMarker is the ADDITIVE envelope through which a
	// consumer says it refused one exact request. It is not an answer, and the
	// two are separate markers precisely so no reader can mistake one for the
	// other. See THE REFUSAL ENVELOPE below for what it exists to repair.
	architectureRefusalMarker = "[sensei-code:refused]"
)

var architectureRequestID = regexp.MustCompile(`^[0-9A-Za-z_.:-]{1,128}$`)

// ArchitectureRequest asks the remote architect one question whose identity is
// already owned by the workflow. Prompt is presentation and context; Binding is
// the subject. The transport never derives the latter back out of the former.
type ArchitectureRequest struct {
	Binding   roles.ArchitectureBinding
	RequestID string
	Prompt    string
	// MailboxRepository is where the conversation lives, "owner/name".
	// WorkspaceRepository is where the governed evidence lives.
	//
	// Deliberately NOT inside Binding: Binding is the identity a response echoes
	// back, and widening it would fail Answers() for every response that does not
	// repeat them. Routing tells a consumer where to look; it is not testimony.
	//
	// Architecture turns happen to stay in the mailbox repository today, which is
	// exactly why this is worth stating rather than leaving implicit. The base an
	// architecture request names is already a WORKSPACE object -- f62e3379 on
	// 2026-09-12 belonged to globulario/sensei while the mailbox was
	// globulario/sensei-code -- so the conflation was latent here too and escaped
	// notice only because nothing fetched it.
	MailboxRepository   string
	WorkspaceRepository string
}

func (r ArchitectureRequest) Validate() error {
	if !r.Binding.Valid() {
		return fmt.Errorf("architecture request has no complete objective/world binding: %+v", r.Binding)
	}
	if !architectureRequestID.MatchString(strings.TrimSpace(r.RequestID)) {
		return fmt.Errorf("architecture request id is missing or malformed: %q", r.RequestID)
	}
	if strings.TrimSpace(r.Prompt) == "" {
		return errors.New("architecture request carries no prompt")
	}
	return nil
}

func (r ArchitectureRequest) Marker() (string, error) {
	if err := r.Validate(); err != nil {
		return "", err
	}
	b := strings.Builder{}
	b.WriteString(architectureRequestMarker + "\n")
	b.WriteString("kind=architecture\n")
	fmt.Fprintf(&b, "task=%s\n", r.Binding.TaskID)
	fmt.Fprintf(&b, "request=%s\n", r.RequestID)
	fmt.Fprintf(&b, "objective_digest=%s\n", r.Binding.ObjectiveDigest)
	fmt.Fprintf(&b, "base=%s\n", r.Binding.BaseSHA)
	// The graph provenance pair, always together. base is a WORKSPACE object and
	// graph_build_commit is SENSEI GRAPH provenance: they are resolved in
	// different repositories, so emitting the commit alone hands the consumer a
	// routing decision it can only make by guessing. Validate() has already
	// refused a half pair, which is why both are emitted unconditionally here.
	fmt.Fprintf(&b, "graph_repository=%s\n", r.Binding.GraphRepository)
	fmt.Fprintf(&b, "graph_build_commit=%s\n", r.Binding.GraphBuildCommit)
	// Omitted when unknown rather than asserted empty, so a consumer fails closed
	// on a missing binding instead of being sent somewhere by a guess.
	if strings.TrimSpace(r.MailboxRepository) != "" {
		fmt.Fprintf(&b, "mailbox_repository=%s\n", r.MailboxRepository)
	}
	if strings.TrimSpace(r.WorkspaceRepository) != "" {
		fmt.Fprintf(&b, "workspace_repository=%s\n", r.WorkspaceRepository)
	}
	b.WriteString("\n")
	b.WriteString(r.Prompt)
	return b.String(), nil
}

// ArchitectureResponse carries only the architect's answer. The marker says
// which exact question it answers; Body remains the architecture JSON contract
// the workflow already parses, so there is no second decision representation.
type ArchitectureResponse struct {
	Binding   roles.ArchitectureBinding
	RequestID string
	Body      string
	Author    string
	AuthorID  int64
}

func (r ArchitectureResponse) Validate() error {
	if !r.Binding.Valid() {
		return fmt.Errorf("architecture response has no complete objective/world binding: %+v", r.Binding)
	}
	if !architectureRequestID.MatchString(strings.TrimSpace(r.RequestID)) {
		return fmt.Errorf("architecture response request id is missing or malformed: %q", r.RequestID)
	}
	if strings.TrimSpace(r.Body) == "" {
		return errors.New("architecture response carries no answer")
	}
	return nil
}

func (r ArchitectureResponse) Marker() (string, error) {
	if err := r.Validate(); err != nil {
		return "", err
	}
	b := strings.Builder{}
	b.WriteString(architectureResponseMarker + "\n")
	fmt.Fprintf(&b, "task=%s\n", r.Binding.TaskID)
	fmt.Fprintf(&b, "request=%s\n", r.RequestID)
	fmt.Fprintf(&b, "objective_digest=%s\n", r.Binding.ObjectiveDigest)
	fmt.Fprintf(&b, "base=%s\n", r.Binding.BaseSHA)
	fmt.Fprintf(&b, "graph_repository=%s\n", r.Binding.GraphRepository)
	fmt.Fprintf(&b, "graph_build_commit=%s\n", r.Binding.GraphBuildCommit)
	b.WriteString("\n")
	b.WriteString(r.Body)
	return b.String(), nil
}

func (r ArchitectureResponse) Answers(q ArchitectureRequest) bool {
	return r.RequestID == q.RequestID && r.Binding.Same(q.Binding)
}

// firstLineOf names what an envelope was actually followed by, bounded so a
// diagnostic quotes a delimiter rather than reprinting a payload.
func firstLineOf(rest string) string {
	line, _, _ := strings.Cut(rest, "\n")
	if len(line) > 40 {
		line = line[:40]
	}
	return line
}

// parseArchitectureEnvelope is strict about the identity header. Duplicate or
// unknown fields are refused rather than ignored: ambiguity in presentation
// must never decide which objective/world an answer belongs to.
//
// extra names REQUIRED header fields an envelope kind carries in addition to the
// binding, so a new envelope can add its own fields without any existing one
// changing shape. It is variadic for exactly that reason: the request and
// response call sites read byte for byte as they did before it existed.
func parseArchitectureEnvelope(body, marker string, request bool, extra ...string) (roles.ArchitectureBinding, string, string, map[string]string, error) {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	// IDENTIFICATION, then this envelope's grammar (A7). The delimiter used to be
	// part of the identification test -- marker+"\n" as one prefix -- so a body
	// that opened with the marker and a wrong delimiter identified as NOTHING and
	// was handed back to its caller as ordinary content. It is a malformed
	// envelope OF THIS KIND, and separating the two steps is what lets it be
	// reported as one.
	if !reviewartifact.Opens(body, marker) {
		return roles.ArchitectureBinding{}, "", "", nil, errors.New("architecture marker missing")
	}
	rest := body[len(marker):]
	// The same delimiter rule the review and request envelopes use, from the one
	// place it is spelled. This envelope had it first; a second hand-written copy
	// of a rule is how two readers in one grammar come to disagree about where a
	// header may begin. Line endings are already normalized above, so accepting
	// CRLF there changes nothing here.
	if !reviewartifact.DelimitsHeader(rest) {
		return roles.ArchitectureBinding{}, "", "", nil, fmt.Errorf(
			"the %s envelope must be followed by a newline, and this one is followed by %q",
			marker, firstLineOf(rest))
	}
	rest = rest[1:]
	header, payload, ok := strings.Cut(rest, "\n\n")
	if !ok {
		return roles.ArchitectureBinding{}, "", "", nil, errors.New("architecture envelope has no payload boundary")
	}
	fields := map[string]string{}
	for _, line := range strings.Split(header, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok || key == "" || value == "" {
			return roles.ArchitectureBinding{}, "", "", nil, fmt.Errorf("malformed architecture header line %q", line)
		}
		if _, duplicate := fields[key]; duplicate {
			return roles.ArchitectureBinding{}, "", "", nil, fmt.Errorf("duplicate architecture header %q", key)
		}
		fields[key] = value
	}
	allowed := map[string]bool{
		"task": true, "request": true, "objective_digest": true,
		"base": true, "graph_repository": true, "graph_build_commit": true,
	}
	// Required, not optional: a field an envelope kind declares is part of its
	// identity, and the completeness loop below refuses an envelope that omits
	// one. A partially stated refusal must not be readable as a whole one.
	for _, key := range extra {
		allowed[key] = true
	}
	// Routing is accepted but NOT required. A request that predates repository
	// binding must still parse, so an in-flight exchange keeps matching its
	// answer; refusing to read it here would break Answers() for requests
	// already standing on GitHub. Absence is then a MISSING BINDING the consumer
	// refuses on -- a typed refusal at the point of use, never a silent default
	// to whichever repository the mailbox happens to be.
	optional := map[string]bool{}
	if request {
		optional["mailbox_repository"] = true
		optional["workspace_repository"] = true
		for key := range optional {
			allowed[key] = true
		}
	}
	if request {
		allowed["kind"] = true
		if fields["kind"] != "architecture" {
			return roles.ArchitectureBinding{}, "", "", nil, fmt.Errorf("architecture request kind = %q", fields["kind"])
		}
	}
	for key := range fields {
		if !allowed[key] {
			return roles.ArchitectureBinding{}, "", "", nil, fmt.Errorf("unknown architecture header %q", key)
		}
	}
	// The graph provenance pair, refused as a pair and named as one.
	//
	// An envelope that predates graph_repository carries graph_build_commit with
	// no repository that owns it. Refusing it is the CORRECT outcome and is
	// deliberate: the alternative is to resolve the commit in whatever repository
	// happens to be at hand, which is how a Sensei graph commit came to be looked
	// up in the workspace repository that never contained it. The missing half is
	// never inferred, defaulted, or copied from workspace_repository.
	if (fields["graph_repository"] == "") != (fields["graph_build_commit"] == "") {
		return roles.ArchitectureBinding{}, "", "", nil, fmt.Errorf(
			"architecture graph provenance is half stated (graph_repository=%q graph_build_commit=%q); "+
				"neither half travels alone and the absent one is never inferred",
			fields["graph_repository"], fields["graph_build_commit"])
	}
	for key := range allowed {
		if optional[key] {
			continue
		}
		if fields[key] == "" {
			return roles.ArchitectureBinding{}, "", "", nil, fmt.Errorf("architecture header %q is missing", key)
		}
	}
	binding := roles.ArchitectureBinding{
		TaskID:           fields["task"],
		ObjectiveDigest:  fields["objective_digest"],
		BaseSHA:          fields["base"],
		GraphRepository:  fields["graph_repository"],
		GraphBuildCommit: fields["graph_build_commit"],
	}
	if !binding.Valid() {
		return roles.ArchitectureBinding{}, "", "", nil, fmt.Errorf("architecture binding is malformed: %+v", binding)
	}
	id := fields["request"]
	if !architectureRequestID.MatchString(id) {
		return roles.ArchitectureBinding{}, "", "", nil, fmt.Errorf("architecture request id is malformed: %q", id)
	}
	if strings.TrimSpace(payload) == "" {
		return roles.ArchitectureBinding{}, "", "", nil, errors.New("architecture envelope payload is empty")
	}
	return binding, id, payload, fields, nil
}

func ParseArchitectureRequest(body string) (ArchitectureRequest, bool) {
	binding, id, prompt, fields, err := parseArchitectureEnvelope(body, architectureRequestMarker, true)
	if err != nil {
		return ArchitectureRequest{}, false
	}
	return ArchitectureRequest{
		Binding:             binding,
		RequestID:           id,
		Prompt:              prompt,
		MailboxRepository:   fields["mailbox_repository"],
		WorkspaceRepository: fields["workspace_repository"],
	}, true
}

func ParseArchitectureResponse(body string) (ArchitectureResponse, bool) {
	binding, id, answer, _, err := parseArchitectureEnvelope(body, architectureResponseMarker, false)
	if err != nil {
		return ArchitectureResponse{}, false
	}
	return ArchitectureResponse{Binding: binding, RequestID: id, Body: answer}, true
}

// ErrEvidenceHasNoOwningRepository refuses a pinned commit the envelope did not
// route.
//
// There is deliberately no fallback behind it. Trying the workspace repository
// for a graph commit is exactly how 05feaf64d2694e97ac42b6bb93fbb49b9851a1f1 was
// looked up in globulario/sensei-code -- which answered "422 no commit found" --
// while the commit existed in globulario/sensei the whole time. A consumer that
// must guess which repository a SHA belongs to has already lost the binding.
var ErrEvidenceHasNoOwningRepository = errors.New("a pinned commit arrived with no repository that owns it")

// ResolvePinnedEvidence resolves every commit this request pins through the ONE
// repository whose authority owns it, and through no other.
//
// The domains do not overlap and are never tried in turn: workspace_repository
// owns base, graph_repository owns graph_build_commit, and mailbox_repository
// owns the conversation and nothing pinned here. lookup is called exactly once
// per identity, with that identity's owning repository. Attempting a second
// repository and accepting whichever answered would be the same guess wearing a
// retry, so an unrouted identity is refused rather than searched for.
func (r ArchitectureRequest) ResolvePinnedEvidence(lookup func(repository, commit string) error) error {
	if lookup == nil {
		return errors.New("resolving pinned evidence needs a lookup")
	}
	for _, pinned := range []struct{ field, repository, commit string }{
		{"base", r.WorkspaceRepository, r.Binding.BaseSHA},
		{"graph_build_commit", r.Binding.GraphRepository, r.Binding.GraphBuildCommit},
	} {
		repository := strings.TrimSpace(pinned.repository)
		if repository == "" {
			return fmt.Errorf("%w: %s=%s", ErrEvidenceHasNoOwningRepository, pinned.field, pinned.commit)
		}
		if err := lookup(repository, pinned.commit); err != nil {
			return fmt.Errorf("resolving %s %s in %s: %w", pinned.field, pinned.commit, repository, err)
		}
	}
	return nil
}

// THE REFUSAL ENVELOPE -- ADDITIVE, AND TERMINAL FOR ONE EXACT REQUEST.
//
// MEASURED 2026-09-24. A governed run published two architecture requests, rang
// the doorbell, waited 30 minutes each and ended "no architecture answer
// answering that request was posted". The consumer had not been silent: it had
// computed an exact diagnostic -- a commit the objective pinned existed on no
// remote -- and had nowhere to put it. Seven prefixes, none of which can express
// a rejection, so the rejection became silence; and silence is indistinguishable
// from an absent consumer, an exhausted quota and a broken doorbell. An hour was
// spent, the cause was misattributed to quota twice, and the true reason
// surfaced by accident through another channel.
//
// This envelope is the place to put it. It is VISIBILITY, not recovery: it makes
// a refused request end immediately and by its true name, and it changes nothing
// about what happens next.

const (
	refusalStageField      = "stage"
	refusalVocabularyField = "stage_vocabulary"
)

// RefusalStageVocabulary versions the closed stage set below.
//
// Versioned because a consumer and this reader must agree on what a stage MEANS,
// not merely on its spelling. A refusal quoting a vocabulary this build does not
// know is refused rather than read as though the member set were the same: the
// alternative is to interpret a future stage by its string and act on a meaning
// nobody stated here.
const RefusalStageVocabulary = "v1"

// RefusalStage is the point in a consumer's own pipeline at which it stopped.
//
// Every member is POST-BINDING, deliberately. A refusal may only be posted once
// an exact request has been authenticated and bound, because the envelope's
// whole claim is the binding it echoes. A consumer failure with no trustworthy
// binding -- a malformed wake, an event it could not attribute -- has no stage
// here and stays a consumer-side diagnostic; the protocol never invents,
// infers, or partially fills a binding in order to report one.
type RefusalStage string

const (
	// RefusalStagePinnedEvidence: evidence this request pins could not be
	// resolved in the repository that owns it. THE MEASURED CASE.
	RefusalStagePinnedEvidence RefusalStage = "pinned-evidence"
	// RefusalStageWorkspace: the governed workspace this request routes could
	// not be established, so nothing pinned in it could be read at all.
	RefusalStageWorkspace RefusalStage = "workspace"
	// RefusalStageAnswerContract: the consumer reached the point of answering
	// and could not produce an answer meeting the contract the request states.
	RefusalStageAnswerContract RefusalStage = "answer-contract"
)

// refusalStages is the closed set, read by MEMBERSHIP.
//
// Membership rather than a list of rejected spellings: an exclusion test admits
// everything nobody thought to exclude, which for a vocabulary that decides
// meaning is failing open.
var refusalStages = map[RefusalStage]bool{
	RefusalStagePinnedEvidence: true,
	RefusalStageWorkspace:      true,
	RefusalStageAnswerContract: true,
}

// Known reports membership in the closed vocabulary named by
// RefusalStageVocabulary.
func (s RefusalStage) Known() bool { return refusalStages[s] }

// ArchitectureRefusal is a consumer saying, in the protocol's own grammar, that
// it refused ONE exact request and why.
//
// It is not an answer and cannot become one: it carries no plan, no decision, no
// coverage and no grant, and there is no field here an architecture result could
// be read out of. Binding is the COMPLETE request binding, echoed so a reader
// can tell this refusal apart from a refusal of something else.
type ArchitectureRefusal struct {
	Binding   roles.ArchitectureBinding
	RequestID string
	Stage     RefusalStage
	// Reason is the consumer's own text, verbatim, never a paraphrase. It is the
	// payload rather than a header field so multi-line diagnostics survive
	// exactly as the consumer wrote them.
	Reason string
	// Author, AuthorID and Comment are transport facts about how this refusal
	// was observed, filled in by the reader. They are never part of the binding.
	Author   string
	AuthorID int64
	Comment  int64
}

func (f ArchitectureRefusal) Validate() error {
	if !f.Binding.Valid() {
		return fmt.Errorf("architecture refusal has no complete objective/world binding: %+v", f.Binding)
	}
	if !architectureRequestID.MatchString(strings.TrimSpace(f.RequestID)) {
		return fmt.Errorf("architecture refusal request id is missing or malformed: %q", f.RequestID)
	}
	if !f.Stage.Known() {
		return fmt.Errorf("architecture refusal stage %q is not a member of the closed stage vocabulary %s",
			f.Stage, RefusalStageVocabulary)
	}
	if strings.TrimSpace(f.Reason) == "" {
		return errors.New("architecture refusal carries no reason")
	}
	return nil
}

// Marker renders the refusal in canonical order: the request's own binding
// order, then this envelope's own fields.
//
// The binding order matches ArchitectureRequest.Marker exactly so the echo is
// readable as an echo. The graph provenance pair is emitted together and never
// half stated -- Validate has already refused a binding carrying one half --
// for the same reason it is inseparable in a request: graph_build_commit is
// Sensei graph provenance and base is a workspace object, so a commit emitted
// without the repository that owns it hands its reader a guess.
func (f ArchitectureRefusal) Marker() (string, error) {
	if err := f.Validate(); err != nil {
		return "", err
	}
	b := strings.Builder{}
	b.WriteString(architectureRefusalMarker + "\n")
	fmt.Fprintf(&b, "task=%s\n", f.Binding.TaskID)
	fmt.Fprintf(&b, "request=%s\n", f.RequestID)
	fmt.Fprintf(&b, "objective_digest=%s\n", f.Binding.ObjectiveDigest)
	fmt.Fprintf(&b, "base=%s\n", f.Binding.BaseSHA)
	fmt.Fprintf(&b, "graph_repository=%s\n", f.Binding.GraphRepository)
	fmt.Fprintf(&b, "graph_build_commit=%s\n", f.Binding.GraphBuildCommit)
	fmt.Fprintf(&b, "%s=%s\n", refusalVocabularyField, RefusalStageVocabulary)
	fmt.Fprintf(&b, "%s=%s\n", refusalStageField, f.Stage)
	b.WriteString("\n")
	b.WriteString(f.Reason)
	return b.String(), nil
}

// mismatch names the FIRST identity on which this refusal differs from an open
// request, or "" when it binds to it exactly.
//
// One predicate with two readers: Refuses decides settlement from it and a
// rejected refusal states its diagnostic from it, so what settles a wait and
// what a reader is told cannot drift apart.
func (f ArchitectureRefusal) mismatch(q ArchitectureRequest) string {
	switch {
	case f.RequestID != q.RequestID:
		return fmt.Sprintf("request is %q and the open request is %q", f.RequestID, q.RequestID)
	case f.Binding.TaskID != q.Binding.TaskID:
		return fmt.Sprintf("task is %q and the open request is bound to %q", f.Binding.TaskID, q.Binding.TaskID)
	case f.Binding.ObjectiveDigest != q.Binding.ObjectiveDigest:
		return fmt.Sprintf("objective_digest is %q and the open request is bound to %q",
			f.Binding.ObjectiveDigest, q.Binding.ObjectiveDigest)
	case f.Binding.BaseSHA != q.Binding.BaseSHA:
		return fmt.Sprintf("base is %q and the open request is bound to %q", f.Binding.BaseSHA, q.Binding.BaseSHA)
	case f.Binding.GraphRepository != q.Binding.GraphRepository:
		return fmt.Sprintf("graph_repository is %q and the open request is bound to %q",
			f.Binding.GraphRepository, q.Binding.GraphRepository)
	case f.Binding.GraphBuildCommit != q.Binding.GraphBuildCommit:
		return fmt.Sprintf("graph_build_commit is %q and the open request is bound to %q",
			f.Binding.GraphBuildCommit, q.Binding.GraphBuildCommit)
	}
	// The backstop, which is not redundant with the cases above. Those name
	// fields so a rejection can say WHICH one differs, and they can only name
	// the fields that existed when they were written. Equality over the whole
	// binding is the predicate: an identity added to ArchitectureBinding later
	// fails closed here instead of being quietly left out of the match.
	if !f.Binding.Same(q.Binding) {
		return fmt.Sprintf("binding %+v differs from the open request's %+v", f.Binding, q.Binding)
	}
	return ""
}

// Refuses reports whether this refusal is bound to THAT EXACT request.
//
// Full binding equality, with no subset fallback. Matching on task alone, or on
// request id alone, would let a refusal of one objective/world end a wait for
// another -- and a refusal is terminal, so a loose match terminates the wrong
// exchange with a reason that was never about it.
func (f ArchitectureRefusal) Refuses(q ArchitectureRequest) bool { return f.mismatch(q) == "" }

// ErrNotAnArchitectureRefusal reports a body that does not even CLAIM to be one.
var ErrNotAnArchitectureRefusal = errors.New("not a sensei-code architecture refusal")

// ArchitectureRefusalShaped reports whether a comment CLAIMS to be a refusal.
//
// The distinction that keeps this repair from recreating the defect one layer
// up. A body that claims to be a refusal and cannot be read is a REJECTED
// refusal an operator must be shown; a body that never claimed to be one is
// just another comment. Collapsing the two turns a malformed rejection back
// into silence, which is the exact failure this envelope removes.
//
// CLAIMING is position zero and the exact marker, nothing more. It previously
// required marker+"\n", so a bare refusal marker, or one followed by any other
// delimiter, began with the additive prefix and was still classified as ordinary
// content: it never reached the parser and never reached the rejected list, so
// silence returned at exactly the boundary this envelope exists to remove. The
// delimiter is a rule of the GRAMMAR, enforced by ParseArchitectureRefusal,
// which can then say what was wrong with it.
//
// This is the general positional rule applied here, not a special case for
// refusals: identity comes from the first token, and well-formedness is decided
// afterwards by the grammar that token selected.
func ArchitectureRefusalShaped(body string) bool {
	return reviewartifact.Opens(strings.ReplaceAll(body, "\r\n", "\n"), architectureRefusalMarker)
}

// ParseArchitectureRefusal reads a refusal strictly, and says why when it will
// not.
//
// Returns an error rather than a bool because every rejection here has to be
// reportable: a refusal-shaped comment that is discarded without a stated
// reason is silence wearing a different mask.
func ParseArchitectureRefusal(body string) (ArchitectureRefusal, error) {
	if !ArchitectureRefusalShaped(body) {
		return ArchitectureRefusal{}, ErrNotAnArchitectureRefusal
	}
	binding, id, reason, fields, err := parseArchitectureEnvelope(body, architectureRefusalMarker, false,
		refusalVocabularyField, refusalStageField)
	if err != nil {
		return ArchitectureRefusal{}, err
	}
	if fields[refusalVocabularyField] != RefusalStageVocabulary {
		return ArchitectureRefusal{}, fmt.Errorf(
			"refusal stage vocabulary is %q and this build reads %q; a stage means only what the "+
				"vocabulary that defines it says it means",
			fields[refusalVocabularyField], RefusalStageVocabulary)
	}
	stage := RefusalStage(fields[refusalStageField])
	if !stage.Known() {
		return ArchitectureRefusal{}, fmt.Errorf(
			"refusal stage %q is not a member of the closed stage vocabulary %s",
			stage, RefusalStageVocabulary)
	}
	refusal := ArchitectureRefusal{Binding: binding, RequestID: id, Stage: stage, Reason: reason}
	if verr := refusal.Validate(); verr != nil {
		return ArchitectureRefusal{}, verr
	}
	return refusal, nil
}
