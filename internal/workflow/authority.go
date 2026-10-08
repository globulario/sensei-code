package workflow

// The authority router.
//
// Whether a plan may proceed on architectural authority alone is a property of
// the graph, not a feeling the architect reports. Before this file, the model
// returned "proceed" or "escalate" and the workflow simply obeyed, which gets
// both directions wrong: a confident model proceeds through a region the graph
// cannot cover, and a cautious model interrupts a human over a question Sensei
// could have answered outright. Neither error is visible at the time, because
// in both cases the model sounds exactly as sure as it always does.
//
// So routing takes no model text as input. It reads Sensei's structured
// evidence and the architect's explicit factual claims, and every human
// interruption it produces names the exact certifiability condition that caused
// it — an interruption a human cannot trace back to a condition is one they
// learn to dismiss.

import (
	"context"
	"fmt"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/globulario/sensei-code/internal/authority"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/sensei"
)

// Route is the outcome of the authority router.
type Route string

const (
	// RouteArchitectural means Sensei can certify the region and the plan may
	// proceed without interrupting anyone.
	RouteArchitectural Route = "architectural-authority-granted"
	// RouteHuman means a human owns this decision. The condition says why.
	RouteHuman Route = "human-authority-required"
	// RouteCloseGap means the plan cannot be granted YET because relevant
	// knowledge is incomplete, the incompleteness is bounded, and closing it
	// does not cross a human-owned consequence boundary.
	//
	// It is not permission to proceed, and it is not permission to experiment.
	// It is an instruction to go and establish what is already knowable, after
	// which governance runs again from the top. Retrieval silence still confers
	// nothing: the route grants no authority over the planned change, it names
	// work that must happen before the question can even be asked properly.
	//
	// Only after established knowledge has been retrieved, and only if it
	// leaves more than one viable technical alternative, does the
	// DesignQuestion lane become relevant. This route does not enter it.
	RouteCloseGap Route = "bounded-knowledge-gap"
	// RouteObserve means the action changes nothing, so no architectural
	// authority is needed and none is granted.
	//
	// This exists because a governed audit of an uncovered region used to be
	// impossible. "Check these doc comments against the code" needs no coverage
	// -- reading a file establishes nothing and breaks nothing -- but the router
	// saw a region the graph could not cover and sent the question to a human,
	// which meant the system could not investigate a subsystem until it already
	// knew enough to modify it. Observation and authorization were the same
	// question, and they are not.
	//
	// What makes this safe is not trust. It is that the stage is structural and
	// the absence of a change is VERIFIED before the run may report anything.
	RouteObserve Route = "observation-no-authority-needed"
	// RouteCannotEstablish means Sensei could not vouch for its own answers, so
	// the question is not "who decides" but "the governance surface is broken".
	// Escalating this to a human as a design question would be asking them to
	// adjudicate something nobody has evidence about.
	RouteCannotEstablish Route = "cannot-establish-authority"
)

// Claim is a factual premise the architect asserts its plan rests on.
//
// Source is the architect's own account of where the premise came from. It is
// not trusted as evidence — an "inference" is routed to a human precisely
// because the model is reporting that nothing verified it.
type Claim struct {
	Statement string `json:"statement"`
	About     string `json:"about"`
	Source    string `json:"source"` // graph | repository | inference
	// Gap references the engine-issued premise receipt this claim continues,
	// when a closure round could not settle it. It is a reference to an
	// identity the engine owns, not an identity the model authored.
	Gap string `json:"gap,omitempty"`
	// Authority states, in structure, that this premise is about exactly one
	// plan-local authority requirement the plan itself declares. Absent on
	// every other premise. It is a statement of what the premise is ABOUT, so
	// that a recorded grant can answer it exactly; it grants nothing.
	Authority *AuthorityPremise `json:"authority,omitempty"`
}

// AuthorityPremise is the structured subject of a premise about plan-local
// authority: one declared requirement, by kind and path, and what the premise
// asserts about it. Every field is read by exact membership; a value outside
// its closed set makes the premise an ordinary one.
type AuthorityPremise struct {
	// Requirement is premiseProspectiveCreate (a declared prospective_surfaces
	// entry) or premiseTestEdit (a declared test_edits entry).
	Requirement string `json:"requirement"`
	// Path is the declared entry's path, exactly as declared.
	Path string `json:"path"`
	// State is what the premise asserts: premiseAuthorityUnestablished.
	State string `json:"state"`
}

// The closed vocabularies of AuthorityPremise.
const (
	premiseProspectiveCreate      = "prospective_create"
	premiseTestEdit               = "test_edit"
	premiseAuthorityUnestablished = "unestablished"
)

// Routing is the router's decision plus the reason a human can act on.
// RefusalBasis says what a stop RESTS ON. It never decides whether the stop
// happens.
//
// Two refusals arrive in the same shape -- a route, a condition -- and mean
// opposite things. "A human owns this decision" is a statement about VALUE:
// somebody must weigh a consequence, and no amount of evidence removes that.
// "This router has no reading for that signal" is a statement about KNOWLEDGE:
// nothing was weighed, something could not be seen, and evidence closes it.
//
// Both landed on RouteHuman, so both cost a person's attention. On 2026-09-07
// the second kind fired on a LOW_RISK region Sensei had already classified
// APPROVAL_GATE_NONE, and separately a coverage rule about unindexed test files
// was read for hours as a principled refusal because it was worded like one.
// The route said WHO decides and never WHY, and the difference is the whole
// question of whether a refusal is protecting something or reporting a limit.
//
// This is descriptive. It is deliberately not consulted by any routing
// decision, and a test pins that: a basis that could change a route would be a
// classifier that widens the router, which is the thing this must not become.
type RefusalBasis int

const (
	// BasisUnclassified is the zero value and is READ AS protecting a value.
	//
	// Deliberately first, so an unlabelled stop costs a person's attention
	// rather than being filed as a closable gap. Mislabelling a value as a
	// knowledge limit would route a human-owned decision to a derivation, and
	// silence is not evidence that nothing was being protected.
	BasisUnclassified RefusalBasis = iota

	// BasisProtectsValue: somebody must weigh a consequence. Evidence does not
	// dissolve it, and the stop is the system working.
	BasisProtectsValue

	// BasisLacksKnowledge: nothing was weighed because something could not be
	// seen. Closable by establishing evidence, and a stop of this kind that
	// reaches a person is a cost with no protection attached.
	BasisLacksKnowledge
)

func (b RefusalBasis) String() string {
	switch b {
	case BasisProtectsValue:
		return "protects-value"
	case BasisLacksKnowledge:
		return "lacks-knowledge"
	default:
		return "unclassified"
	}
}

// ProtectsValue reads the basis, defaulting an unclassified stop to the
// protective reading.
func (r Routing) ProtectsValue() bool { return r.Basis != BasisLacksKnowledge }

type Routing struct {
	Route Route
	// Condition is the exact certifiability condition that produced the route.
	// It is the explanation a person reads and the evidence a record keeps; it
	// is not the gap's identity.
	Condition string
	// Gap classifies and locates a bounded knowledge gap, set when the route
	// is RouteCloseGap. It is metadata for the premise receipt (premise.go),
	// which is the identity the closure budget is spent against: class and
	// location are not the question -- two premises about one file share
	// them, and one premise re-stated as a symbol does not (sensei-code#97).
	Gap GapIdentity
	// ClaimGap is the receipt the routing claim referenced, if any.
	ClaimGap string
	// Basis says what this stop rests on. Descriptive; no route reads it.
	Basis RefusalBasis
	// Closes names what would close a BasisLacksKnowledge stop, so a limit
	// arrives with its remedy instead of only its symptom. Empty otherwise.
	Closes string
	// Blast and Gate are Sensei's structured change-risk verdict, carried
	// forward rather than consumed here.
	//
	// The router only asks one question of them — may this proceed without a
	// person — and answering it discards how expensive the change is. That
	// second fact decides something else: how adversarially the candidate must
	// be judged. A local change reviewed by whoever is free and a system-wide
	// one reviewed by the provider that wrote it are not the same risk, and
	// without these fields the second is indistinguishable from the first by the
	// time the reviewer is assigned.
	Blast string
	Gate  string
}

// GapIdentity identifies a bounded knowledge gap independently of how the
// architect worded it.
//
// It is derived from the governed question, never authored by the model: the
// kind is the router's own classification of why the route closed, the subject
// is the planned file the premise concerns (resolved by path against the
// plan, or the whole plan region when no planned path is named), the scope is
// the plan's files, and the world is the pinned base. Two paraphrases of one
// premise about one file share an identity; two premises about two files do
// not. Normalising the prose would be a claim that wording is identity; this
// is the opposite claim.
//
// A coverage gap is one EPISODE from the moment it opens: its ledger key is
// fixed then, and a same-identity re-evaluation that settles some of its
// members narrows Scope in place without minting another key (DF-30, ruling
// 177). Opening carries the form the episode opened as on every later form of
// the gap this process hands around -- the routing, the question, the answer --
// so each returns to the one AuthorityResolution the gap opened as. It is
// structure, never an opaque key: the key is derived from it, and a binding
// whose opening does not describe the same kind, subject and world
// (validBinding) is refused rather than trusted. Nil until a gap is narrowed.
//
// Opening and Semantics are LIVE episode state and are never written to a
// durable record (json "-"). Objective 59a owns the live episode only: no
// canonical encoding of an episode exists yet, so a recorded coverage gap
// carries no episode a resume could authenticate, and restoration fails closed
// on it (unauthenticatedCoverageRestore) instead of trusting a payload nobody
// can verify. Durable episode reconstruction is objective 59b's.
type GapIdentity struct {
	Kind    string
	Subject string
	Scope   []string
	World   string
	// Question tells apart two coverage episodes over one kind, subject,
	// scope and world that ask different qualified questions: the qualified
	// requirement a coverage episode asks, and empty for an unqualified one
	// (canonicalQuestion). It is part of the key, so each question is its own
	// canonical ledger entry -- its own disposition, budget and answer -- and
	// neither consumes the other's.
	Question Requirement `json:",omitempty"`
	Opening  *GapOpening `json:"-"`
	// Semantics is the question a coverage episode asks, fixed when the
	// episode opens and carried unchanged on every later form of it (DF-30,
	// rulings 176 and 181). Nil on every other kind. A coverage episode
	// without a valid one is never settled, narrowed or re-rendered.
	Semantics *CoverageSemantics `json:"-"`
	// Ambiguous marks a scope-less coverage observation more than one live
	// episode is compatible with (bindEpisode). It continues none of them and
	// opens none: it has no ledger entry, no receipt and no budget, binds no
	// answer, and is never put to a person (validBinding). It fails closed.
	Ambiguous bool `json:"-"`
}

// CoverageSemantics is the semantic payload one coverage episode owns: the
// derivation requirement its members are decided against, and the evidence
// its condition quotes. renderCoverageGap produces it from the preflight the
// episode opened under; every later re-evaluation of that episode reads it
// back, so a later preflight -- unqualified where the opening one was
// qualified, or quoting other diagnostics -- can neither widen which
// derivation settles the episode nor rewrite what its condition says. It is
// immutable for the episode; no canonical replacement rule exists.
type CoverageSemantics struct {
	// Requirement is gapRequirement over Spots.
	Requirement Requirement
	// Spots are the coverage blind spots the requirement was read from, and
	// what a coverage-blind-spot condition quotes.
	Spots []string
	// Diagnostic is the coverage diagnostic a coverage-absent condition
	// quotes. Empty for every other kind.
	Diagnostic string
}

// coverageSemantics is the payload a coverage episode of kind opens with,
// read from the preflight's coverage blind spots and coverage diagnostic.
func coverageSemantics(kind string, spots []string, diagnostic string) CoverageSemantics {
	s := CoverageSemantics{Requirement: gapRequirement(spots), Spots: append([]string(nil), spots...)}
	if kind == gapCoverageAbsent {
		s.Diagnostic = diagnostic
	}
	return s
}

// validFor reports whether s is a payload coverageSemantics writes for kind:
// every blind spot it quotes is one readBlindSpots classifies as coverage --
// the only spots any producer reads its payload from -- its requirement is the
// one those spots name, a coverage-blind-spot quotes at least one of them, a
// coverage-absent quotes a nonblank coverage diagnostic (which
// sensei.Coverage.Diagnostic never leaves blank), and no other kind carries a
// diagnostic. Anything else is a payload no producer writes.
func (s CoverageSemantics) validFor(kind string) bool {
	if !isCoverageGapKind(kind) || s.Requirement != gapRequirement(s.Spots) {
		return false
	}
	for _, spot := range s.Spots {
		if classifyBlindSpot(spot) != blindSpotCoverage {
			return false
		}
	}
	switch kind {
	case gapCoverageAbsent:
		return strings.TrimSpace(s.Diagnostic) != ""
	case gapCoverageBlindSpot:
		return len(s.Spots) != 0 && s.Diagnostic == ""
	}
	return s.Diagnostic == ""
}

// canonicalQuestion is gap under the canonical identity of the question it
// asks: a coverage gap that is no later form of an episode (Opening nil) and
// carries a payload naming a qualified requirement is that requirement's
// question (GapIdentity.Question) -- the FIRST episode over an identity
// included -- so two distinct qualified requirements never share a ledger
// entry, an answer, a disposition, a receipt or a budget. An unqualified
// report asks no question of its own: it keeps Question empty and continues
// whatever compatible episode the discriminator finds (continuation).
func canonicalQuestion(gap GapIdentity) GapIdentity {
	if !isCoverageGapKind(gap.Kind) || gap.Opening != nil || gap.Semantics == nil {
		return gap
	}
	if req := gap.Semantics.Requirement; req != RequirementUnqualified {
		gap.Question = req
	}
	return gap
}

// episodeContinuation says WHY an observed gap belongs to a live episode, or
// that it does not.
//
// Named cases rather than one boolean, because the rule has to be explicit and
// each branch separately testable. In particular a degenerate observation is
// NOT treated as "a scope that overlaps everything" -- that would silently make
// unrelated gaps share an episode the moment one of them named no files.
type episodeContinuation int

const (
	// episodeUnrelated: a different question. It gets its own identity, its
	// own receipt and its own budget, which is the discrimination
	// sensei-code#97 established.
	episodeUnrelated episodeContinuation = iota
	// episodeSameScope: the observation names the files the episode opened
	// over or currently holds, or the episode holds none to compare against.
	episodeSameScope
	// episodeOverlapping: the observation moved, narrowed or widened, and
	// still concerns files the episode has held.
	episodeOverlapping
	// episodeDegenerate: the observation names NO files.
	//
	// Observed live at 14:32:21 on task-1789272620293170079: coverage
	// collapsed to "0 anchor(s) over 0 planned file(s)". A plan that names
	// nothing has not answered the question and has not become a different
	// question, so it stays bound to the episode it is failing to close -- but
	// only when exactly one live episode is compatible with it (bindEpisode).
	// It must never buy a round by evaporating the work surface.
	episodeDegenerate
)

// episodeLineage is everything the one continuation discriminator reads of a
// live episode: its kind, pinned world, subject and immutable qualified
// requirement, the scope it opened over, its current scope, and every member
// it has ever held. The resolution ledger (bindEpisode) and the closure-budget
// ledger (premiseReceiptFor) both decide continuation through it, so they
// cannot disagree about which episode a later observation belongs to.
type episodeLineage struct {
	Kind, Subject, World string
	Question             Requirement
	Opening, Current     []string
	Members              []string
}

// lineage is the episode r as the discriminator reads it.
func (r *AuthorityResolution) lineage() episodeLineage {
	o := r.opening()
	opened := r.opened
	if opened == nil {
		opened = o.Scope
	}
	return episodeLineage{Kind: o.Kind, Subject: o.Subject, World: o.World, Question: o.Question,
		Opening: opened, Current: r.Gap.Scope, Members: r.members()}
}

// continuation decides whether gap continues the episode l describes.
//
// THE LAW: the retried actor may change its plan; it may not thereby change
// the episode's identity. Kind, world and the qualified requirement
// discriminate exactly; a gap naming a subject must name the episode's; and
// the scope test is episode membership, not equality: the same members,
// fewer, more, moved onto one the episode once held, or none at all.
//
// Measured consequence of comparing against the LATEST scope instead, on
// task-1789272620293170079 (2026-09-13): one coverage-unexamined gap, scope
// 7 -> 5 -> 7 -> 0 -> 7 -> 6, six closure rounds under closureBudget = 1.
func (l episodeLineage) continuation(gap GapIdentity) episodeContinuation {
	if l.Kind != gap.Kind || l.World != gap.World {
		return episodeUnrelated
	}
	// An equal subject continues, and a gap that names none continues
	// whatever it landed in. Two premises about one file remain two questions.
	if gap.Subject != l.Subject && gap.Subject != "" {
		return episodeUnrelated
	}
	// A distinct qualified requirement is a distinct question; an unqualified
	// observation asks none of its own.
	if gap.Question != "" && gap.Question != l.Question {
		return episodeUnrelated
	}
	reported := normalizeEpisodeScope(gap.Scope)
	members := normalizeEpisodeScope(l.Members)
	switch {
	case len(reported) == 0:
		return episodeDegenerate
	case len(members) == 0:
		// The episode has never held a file, so nothing constrains
		// membership by path; the fields above already discriminated.
		return episodeSameScope
	case scopesEqual(reported, normalizeEpisodeScope(l.Opening)) || scopesEqual(reported, normalizeEpisodeScope(l.Current)):
		return episodeSameScope
	case scopesOverlap(members, reported):
		return episodeOverlapping
	}
	return episodeUnrelated
}

// sameSemantics reports whether two payloads are the same, absence included.
func sameSemantics(a, b *CoverageSemantics) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Requirement == b.Requirement && a.Diagnostic == b.Diagnostic && slices.Equal(a.Spots, b.Spots)
}

// episodeSemantics is the payload g's episode decides and renders by, and
// whether it has a valid one: a coverage gap carrying one coverageSemantics
// writes for its kind. A missing, malformed or kind-inconsistent payload is
// not one, and an episode without one is never settled, narrowed or
// re-rendered.
//
// A later form of the episode carries the payload twice -- its own, and the
// one its opening binds -- and has one only when the two are the same: the
// payload a form asks by is the one the episode opened with, never one the
// form substitutes.
func (g GapIdentity) episodeSemantics() (CoverageSemantics, bool) {
	if g.Semantics == nil || !g.Semantics.validFor(g.Kind) {
		return CoverageSemantics{}, false
	}
	if g.Opening != nil && !sameSemantics(g.Opening.Semantics, g.Semantics) {
		return CoverageSemantics{}, false
	}
	return *g.Semantics, true
}

// GapOpening is the immutable form a coverage episode opened as, with the
// payload it opened with bound into it, so every later form proves which
// question it continues: its own payload must be this one (validBinding).
type GapOpening struct {
	Kind      string
	Subject   string
	Scope     []string
	World     string
	Question  Requirement
	Semantics *CoverageSemantics
}

// identity is the opening as the gap identity it opened as. The payload is
// not part of the key; the qualified requirement it asks is (Question).
func (o GapOpening) identity() GapIdentity {
	return GapIdentity{Kind: o.Kind, Subject: o.Subject, Scope: append([]string(nil), o.Scope...), World: o.World, Question: o.Question}
}

// members is every planned file the episode r has held: its current scope,
// then the scope it opened over and every scope it has held since. A member an
// earlier re-evaluation settled is still the episode's member -- settlement is
// a fact about the Action that settled it, not about the file -- so it is
// decided again on every re-evaluation (reconcileCoverageGaps).
func (r *AuthorityResolution) members() []string {
	var out []string
	seen := map[string]bool{}
	add := func(files []string) {
		for _, f := range files {
			if c := cleanPlannedPath(f); c != "." && !seen[c] {
				seen[c] = true
				out = append(out, c)
			}
		}
	}
	add(r.Gap.Scope)
	add(r.opened)
	if r.Gap.Opening != nil {
		add(r.Gap.Opening.Scope)
	}
	add(r.held)
	return out
}

// hold records the episode's current scope among the scopes it has held,
// before its current form is replaced.
func (r *AuthorityResolution) hold() {
	r.held = normalizeEpisodeScope(append(append([]string(nil), r.held...), r.Gap.Scope...))
}

// Key is the ledger key the closure budget is spent against: the episode the
// gap belongs to, which is the key of the form it opened in.
func (g GapIdentity) Key() string {
	if g.Opening != nil {
		return g.Opening.identity().Key()
	}
	scope := append([]string(nil), g.Scope...)
	sort.Strings(scope)
	key := g.Kind + "|" + g.Subject + "|" + strings.Join(scope, ",") + "|" + g.World
	if g.Question != "" {
		key += "|" + string(g.Question)
	}
	return key
}

// validBinding reports whether g's episode binding is one the engine itself
// writes: none at all, or an opening of a coverage gap of the SAME kind,
// subject and world as g, over a non-empty scope in Key's canonical order,
// carrying exactly g's payload, that is not g's own form (episodeOf writes no
// opening for the form the episode opened as). The opening scope may be empty:
// an episode that opened over no files is continued by a later populated form.
// Any other binding would attach a question or answer to an identity it is
// not about, so it is refused -- as is an ambiguous observation, which is
// bound to no episode at all.
func (g GapIdentity) validBinding() bool {
	if g.Ambiguous {
		return false
	}
	o := g.Opening
	if o == nil {
		return true
	}
	if !isCoverageGapKind(g.Kind) || o.Kind != g.Kind || o.Subject != g.Subject || o.World != g.World || o.Question != g.Question ||
		!scopesEqual(o.Scope, normalizeEpisodeScope(o.Scope)) ||
		!sameSemantics(o.Semantics, g.Semantics) || (o.Semantics != nil && !o.Semantics.validFor(o.Kind)) {
		return false
	}
	own := g
	own.Opening = nil
	return own.Key() != g.Key()
}

// episodeEntry is the ledger entry gap is a form of: the one under its
// canonical key (canonicalQuestion), so a gap asking a distinct qualified
// question is never read as another question's episode -- its answer,
// disposition and condition are not that gap's. An ambiguous observation is a
// form of no entry.
func (tr *taskResolutions) episodeEntry(gap GapIdentity) (*AuthorityResolution, bool) {
	if gap.Ambiguous {
		return nil, false
	}
	r, ok := tr.byKey[canonicalQuestion(gap).Key()]
	return r, ok
}

// Identified reports whether the router classified this gap at all.
func (g GapIdentity) Identified() bool { return strings.TrimSpace(g.Kind) != "" }

// AuthorityResolution is P9: the one durable answer to "is gap G settled for
// task T at world W".
//
// Before it, two deciders answered that question and could disagree. The
// proceed route read a modifying plan and reported the gap; the escalate route
// read a differently shaped plan over the same work and certified it, and the
// round ceiling was the only thing that ended the loop between them
// (task-1790513596091085059, 2026-09-27). Coverage observations may establish
// that a gap EXISTS; only an explicit authority answer may SETTLE it, and every
// routing site -- proceed, escalate, resume -- consumes this record rather than
// deciding for itself.
//
// Two facts are kept apart on purpose:
//
//   - Observed: whether the latest routing that re-evaluated this identity's
//     scope reported it. A plan that stops reporting a gap makes it inactive
//     for that observation. That is not settlement: the identity is kept, and
//     a later RouteCloseGap observation of it opens it again.
//   - Settled: an explicit authority answer given about exactly this identity.
//     Terminal and monotonic. The first settlement stands; replay, resume and a
//     second routing surface cannot reopen or override it.
type AuthorityResolution struct {
	Gap GapIdentity
	// Routing is the latest observation of this identity, so a consumer that
	// must ask about it asks the question the router actually reached.
	Routing  Routing
	Observed bool
	Settled  bool
	Outcome  authority.Outcome
	// Disposition is the typed result of this coverage identity's latest
	// same-identity re-evaluation (reconcileCoverageGaps), nil until one ran.
	// Gap, Routing and Disposition are the episode's CURRENT state: a narrowing
	// rewrites them here, under the key the episode opened with, and never
	// opens a second entry for the members that remain.
	Disposition *GapDisposition
	// opened is the scope the identity opened over, when this process saw it
	// open: immutable, and what a later routing's coverage gap is compared
	// against to decide whether it continues this episode (bindEpisode).
	opened []string
	// held is every scope the episode has held since it opened, so a member a
	// re-evaluation settled is decided again by the next one (members).
	held []string
	// restored marks a coverage identity read back from the durable record
	// rather than opened by this process. It carries no episode this process
	// can authenticate (GapIdentity), so it is preserved unresolved: never
	// settled, narrowed or re-rendered, and never put to a person
	// (unauthenticatedCoverageRestore, RULING-181).
	restored bool
}

// Open reports an identity that is currently observed and nobody has settled.
func (r AuthorityResolution) Open() bool { return r.Observed && !r.Settled }

// taskResolutions is one task's P9 state. hydrated records that the durable
// session record has been read into it (the engine does that; this router file
// reads no store), so a restarted process reconstructs the same open and
// settled identities the interrupted one held.
type taskResolutions struct {
	hydrated bool
	byKey    map[string]*AuthorityResolution
	order    []string
	// settlements are the explicit answers given about each identity, in the
	// order given, each owned by the plan attempt it was asked about.
	settlements map[string][]gapAnswer
	// answerScope is which of the task's recorded answers a routing may
	// consume: see answerScope.
	answerScope answerScope
	// observedBy is, per identity, the plan attempt whose routing last raised
	// it: the attempt a question about it is about (questionOwner).
	observedBy map[string]string
}

// observe records that attempt's routing raised gap.
//
// Every identity is about the attempt currently routing it. A coverage
// episode keeps its GapIdentity and its opening semantics through every
// later observation, but its live question is asked by -- and its answer
// owned by -- the plan attempt whose routing observed it last, which is the
// attempt gapSettlement reads it for. Ownership by the attempt that OPENED the
// episode recorded an answer no live boundary could consume (DF-30A review f1).
func (tr *taskResolutions) observe(gap GapIdentity, attempt string) {
	if attempt == "" {
		return
	}
	if tr.observedBy == nil {
		tr.observedBy = map[string]string{}
	}
	tr.observedBy[gap.Key()] = attempt
}

// gapAnswer is one explicit settlement of a gap identity.
type gapAnswer struct {
	owner   string
	epoch   int
	outcome authority.Outcome
}

// answerScope is the plan-attempt ownership rule for recorded human answers --
// gap settlements and consequence resolutions alike.
//
// An answer is owned by the plan attempt the question was asked about, and is
// recorded with its PlanAttemptID. It may be consumed ONLY by a routing of that
// exact attempt -- the same canonical identity, wherever it recurs -- and only
// when this task durably started it. With no attempt being routed, the subject
// is the operative attempt the record names.
//
// No answer crosses to any OTHER attempt: not to the plan the architect wrote
// in reply to it, not to a replacement after an operative transition, and not
// to a plan that differs from its owner only in a declared effect. Each attempt
// is routed through its own authority decision; an answer about one plan never
// authorizes another merely because its condition, gap or paths read the same.
// An answer naming an attempt this task never durably started authorizes
// nothing.
//
// A legacy answer, recorded before answers named their attempt, keeps its
// compatibility reading: it is consumed within the resolution it was given in
// and by the attempt that resolution made operative -- never across a
// transition to another attempt.
type answerScope struct {
	// epoch counts the task's operative transitions recorded so far.
	epoch int
	// closedBy is the attempt each transition made operative, by the epoch it
	// closed; operative is the latest of them.
	closedBy  map[int]string
	operative string
	// started are the attempts this task durably started.
	started map[string]bool
}

// admits reports whether an answer owned by owner, given in epoch, may be
// consumed by a routing of the attempt subject ("" for none in progress).
func (s answerScope) admits(owner string, epoch int, subject string) bool {
	if subject == "" {
		subject = s.operative
	}
	if owner != "" {
		return s.started[owner] && owner == subject
	}
	// The legacy compatibility reading.
	return epoch == s.epoch || (subject != "" && s.closedBy[epoch] == subject)
}

// transition records that attempt became operative, closing the current
// resolution.
func (s *answerScope) transition(attempt string) {
	if s.closedBy == nil {
		s.closedBy = map[int]string{}
	}
	s.closedBy[s.epoch] = attempt
	s.epoch++
	s.operative = attempt
}

// resolvedAuthority is the AuthorityResolved payload for an answer given about
// a certifiability condition: the existing resolution, unchanged and inline,
// plus the identity of the gap it settles, when it is about one, and the plan
// attempt it was asked about. A reader that decodes an authority.Resolution
// sees exactly the fields it always did.
type resolvedAuthority struct {
	authority.Resolution
	Gap *GapIdentity `json:"gap_identity,omitempty"`
	// PlanAttemptID is the attempt whose question this answers. Absent on
	// records written before answers named their attempt (see answerScope).
	PlanAttemptID string `json:"plan_attempt_id,omitempty"`
}

// authorityGapKey carries the gap a human question is about through the one
// rendezvous every question uses, whose signature is shared with callers that
// have no gap at all.
type authorityGapKey struct{}

func withAuthorityGap(ctx context.Context, gap GapIdentity) context.Context {
	if !gap.Identified() {
		return ctx
	}
	return context.WithValue(ctx, authorityGapKey{}, gap)
}

func authorityGapFrom(ctx context.Context) (GapIdentity, bool) {
	gap, ok := ctx.Value(authorityGapKey{}).(GapIdentity)
	return gap, ok && gap.Identified()
}

func (tr *taskResolutions) entry(gap GapIdentity) *AuthorityResolution {
	key := gap.Key()
	r, ok := tr.byKey[key]
	if !ok {
		r = &AuthorityResolution{Gap: gap}
		r.opened = normalizeEpisodeScope(gap.Scope)
		if gap.Opening != nil {
			// A later form of the episode: what it opened over is its
			// opening.
			r.opened = normalizeEpisodeScope(gap.Opening.Scope)
		}
		tr.byKey[key] = r
		tr.order = append(tr.order, key)
	}
	return r
}

// settle is the one settlement transition. Only an outcome that settles counts
// (a revise answer asks for another design and leaves the gap standing). Each
// settlement keeps the attempt that owns it; which one stands for a routing is
// decided when it is read (view).
func (tr *taskResolutions) settle(gap GapIdentity, outcome authority.Outcome, owner string, epoch int) {
	if !outcome.Settles() {
		return
	}
	tr.entry(gap)
	if tr.settlements == nil {
		tr.settlements = map[string][]gapAnswer{}
	}
	tr.settlements[gap.Key()] = append(tr.settlements[gap.Key()], gapAnswer{owner: owner, epoch: epoch, outcome: outcome})
}

// view is the resolution of key as a routing of the attempt pending sees it:
// settled by the FIRST settlement that routing may consume, and by no other.
// The first admissible settlement stands; replay and resume cannot override it.
func (tr *taskResolutions) view(key, pending string) AuthorityResolution {
	r := *tr.byKey[key]
	r.Settled, r.Outcome = false, ""
	for _, a := range tr.settlements[key] {
		if tr.answerScope.admits(a.owner, a.epoch, pending) {
			r.Settled, r.Outcome = true, a.outcome
			break
		}
	}
	return r
}

// gapSubject resolves what a premise is about to a planned file, by path.
//
// The longest planned path contained in the text wins, so "gosumcheck/main.go"
// does not shadow "gosumcheck/main_test.go". Text naming no planned path
// resolves to "" -- the plan region as a whole -- which is deliberately not a
// fresh subject per wording: an unlocated premise about this plan is one gap.
func gapSubject(about string, planned []string) string {
	about = strings.TrimSpace(about)
	best := ""
	for _, p := range planned {
		p = strings.TrimSpace(p)
		if p == "" || !strings.Contains(about, p) {
			continue
		}
		if len(p) > len(best) {
			best = p
		}
	}
	return best
}

// RequiresHuman reports whether this routing interrupts a person.
func (r Routing) RequiresHuman() bool { return r.Route == RouteHuman }

// Granted reports whether the plan may proceed on architectural authority.
func (r Routing) Granted() bool { return r.Route == RouteArchitectural }

// ClosesGap reports whether the route names bounded epistemic work rather than
// an owner for the decision.
func (r Routing) ClosesGap() bool { return r.Route == RouteCloseGap }

// Observes reports whether this action may proceed as a read-only observation.
func (r Routing) Observes() bool { return r.Route == RouteObserve }

// routeAuthority decides who owns this plan.
//
// scoped is a preflight scoped to the files the plan intends to touch, which is
// the first point in the workflow where a file list exists at all. That is why
// this is a second preflight rather than a reuse of the start gate's: the start
// gate could only ask whether Sensei was healthy, while this one can ask
// whether Sensei covers the specific region about to be edited.
//
// The architect's own decision string is deliberately not a parameter. A model
// may ask for more investigation, and that request is worth honouring as
// investigation, but it cannot by itself manufacture a human interruption when
// Sensei can certify the question.
// There is deliberately no action-less form. Consequence assessment is a
// property of the proposed ACTION, so a routing decision without one is a
// question nobody asked properly -- and the wrapper that used to supply
// Action{} made "unspecified" reach a grant, because nothing on the path
// consulted the stage unless a blind spot happened to send it there.
func routeAuthorityForAction(scoped sensei.PreflightDecision, claims []Claim, action Action) Routing {
	// The risk reading is attached to whatever route the decision reaches. It is
	// not the route's justification; it is a fact about the change that outlives
	// this decision, and a caller that has to re-derive it will re-derive it
	// from a preflight taken at a different moment.
	r := decideRouteForAction(scoped, claims, action)
	r.Blast = scoped.ChangeRisk.Blast()
	r.Gate = scoped.ChangeRisk.Gate()
	return r
}

func decideRouteForAction(scoped sensei.PreflightDecision, claims []Claim, action Action) Routing {
	// The order below is the whole design, and it is not the order the
	// conditions were written in.
	//
	//   1. can Sensei vouch for itself at all
	//   2. is the surface answering
	//   3. does an EXPLICIT approval gate already own this
	//   4. what are THIS ACTION's consequences
	//   5. only then, is the blocker epistemic
	//
	// Consequence must outrank every epistemic question, or closing a knowledge
	// gap becomes a way to walk past an approval gate: the router would notice
	// the missing coverage first, send the agent off to establish evidence, and
	// never reach the verdict that said a human owns this change class. That
	// bug was live in the first draft of this file and is pinned by
	// TestAnApprovalGateIsNotClosableByEvidence.
	//
	// Step 4 is a step. It used to be a branch: AssessConsequences was reached
	// only when the blind-spot list had already been narrowed to consequence
	// signals, so whether an action's consequences were assessed AT ALL was
	// decided by metadata about the graph's coverage. Ordinary candidate edits
	// looked correct anyway, because their stage happens to produce the route
	// the fall-through produced -- a right answer resting on the wrong
	// dependency, and a publish action arriving with no blind spots was granted
	// without anything ever reading its stage. Blind spots may INFORM the
	// assessment; they must not own the edge that makes it exist.

	// Sensei vouching for itself comes first. Every judgement below reads a
	// field of this same result, so if the graph is stale or unauthoritative
	// then the coverage and risk answers are not evidence either — they are a
	// stale graph's opinions, delivered with exactly the same confidence.
	// An action that changes nothing is routed before Sensei is asked to vouch
	// for anything, because none of what follows is about it.
	//
	// Deliberately ahead of the certifiability check: whether the graph is
	// stale, unauthoritative or silent has no bearing on whether a process may
	// READ a file. Placing it after would make an unusable graph prevent the
	// very investigation that might explain why the graph is unusable -- which
	// is precisely the trap where a system cannot learn about itself until it
	// already knows enough to change itself.
	//
	// Nothing here grants authority. It records that none was required.
	if action.Stage == StageObserve {
		return Routing{Route: RouteObserve,
			Condition: "the action reads and reports; no file is written and nothing is admitted, " +
				"so there is no architectural authority to grant"}
	}

	if !scoped.Authority.Certifiable() {
		return Routing{Route: RouteCannotEstablish, Basis: BasisLacksKnowledge,
			Closes:    "a certifiable graph generation; nothing was weighed because the instrument could not vouch for itself",
			Condition: scoped.Authority.Diagnostic()}
	}

	// The status decides whether there is an ANSWER to read. It does not decide
	// what the answer says about coverage.
	spots := readBlindSpots(scoped.BlindSpots)
	switch scoped.Status {
	case sensei.PreflightOK, sensei.PreflightEmpty:
		// Both are answers. What they establish is read below, from the
		// coverage evidence, not from which of the two words came back.
	case sensei.PreflightDegraded:
		// DEGRADED is two different findings sharing one word, and only one of
		// them is "the surface is unreliable".
		//
		// Found by attempting the first autonomous self-repair. A repair had to
		// touch internal/workflow/authority_test.go, which answers DEGRADED
		// with exactly these blind spots:
		//
		//	high_risk_path_no_direct_anchors: file is under a high-risk
		//	  directory but no awareness anchors apply
		//	this is NOT proof of safety — the graph has no facts about this file
		//
		// Both are COVERAGE markers -- readBlindSpots already classifies them
		// that way, and the whole blind-spot vocabulary was split on exactly
		// this distinction. The graph is not broken here; it is uninformed
		// about a risky file. Routing that to CannotEstablish reported an
		// epistemic gap as a broken instrument, and CannotEstablish is a hard
		// stop: no closure round is ever attempted, so the one condition a
		// closure round exists to fix could never be fixed.
		//
		// Across the 135-file specimen every DEGRADED file is coverage-shaped,
		// so this is the common case rather than an edge one.
		//
		// It still fails closed on anything else. A DEGRADED answer carrying an
		// unrecognised blind spot, a consequence-shaped one, or NO blind spots
		// at all tells us nothing about WHY, and an instrument that will not
		// say why it is degraded is not one to reason from.
		if !spots.degradedIsCoverageShaped() {
			return Routing{
				Route: RouteCannotEstablish,
				Condition: "preflight degraded and the reason is not a coverage gap: " +
					degradedReason(scoped.BlindSpots),
			}
		}
		// Fall through: this is missing knowledge, and the coverage logic below
		// decides it from the coverage evidence like any other answer.
	default:
		return Routing{
			Route:     RouteCannotEstablish,
			Condition: "preflight " + strings.ToLower(strings.TrimPrefix(string(scoped.Status), "PREFLIGHT_STATUS_")),
		}
	}

	// Coverage is computed from coverage evidence.
	//
	// This read `PreflightEmpty -> coverageAbsent = true`, converting a summary
	// status into a different and stronger proposition: "graph coverage is
	// absent for the planned files". Those are equivalent only if the preflight
	// contract guarantees the equivalence, and it does not. Live counterexample,
	// pinned in TestAnEmptyStatusIsNotACoverageVerdict:
	//
	//	internal/workflow/authority.go
	//	  status   PREFLIGHT_STATUS_EMPTY
	//	  coverage sufficient=true indexed_file_count=1
	//
	// Coverage.Proven() is TRUE there while the router declared the region
	// uncovered and sent the run off to close a gap that was not open. The
	// status was accurate; the stronger reading of it was false.
	//
	// Reading Coverage directly also stops a future status value from silently
	// changing routing: a new enum member alters what is ANSWERABLE, never what
	// is COVERED.
	// DerivedCoverage is applied further down, where it already was.
	coverageAbsent := !scoped.Coverage.Proven()

	// Consequence authority. Both of these escalate rather than permit, so
	// reading them on an EMPTY preflight is safe in the one direction that
	// matters: an absent classification can stop a change here, and can never
	// clear one.
	//
	// An EXPLICIT approval verdict outranks everything, including a coverage
	// gap. Guarded by Classified(), because Gate() renders an unclassified
	// verdict as "unclassified" — which is not "none" and would otherwise
	// escalate here as though a verdict had been reached.
	if scoped.ChangeRisk.Classified() {
		if gate := scoped.ChangeRisk.Gate(); gate != "none" {
			return Routing{
				Route: RouteHuman,
				Basis: BasisProtectsValue,
				Condition: "Sensei requires approval for this change class: " + gate +
					" (blast radius " + scoped.ChangeRisk.Blast() + ")",
			}
		}
	}

	// Consequence assessment. Established for every action that can reach a
	// grant, from the action itself, before any epistemic question is asked.
	//
	// The stage it reads is engine-owned and structural -- fixed by which
	// entrypoint the task came through -- so provider text can neither choose
	// nor clear it. A plan MAY escalate itself by declaring an outward step,
	// which is the one direction a claim is allowed to move an assessment.
	//
	// Nothing here grants. Bounded means "the technical lane may continue",
	// and everything below still applies to it -- an explicit gate has already
	// run above precisely so that a bounded assessment cannot clear one.
	consequences := AssessConsequences(action)
	switch consequences.Result {
	case ConsequenceUnacceptable:
		return Routing{
			Route:     RouteHuman,
			Basis:     BasisProtectsValue,
			Condition: "this action's consequences are not bounded: " + consequences.Boundary + consequenceSignalSuffix(spots),
		}
	case ConsequenceBounded:
		// Continue. The boundary is recorded on the routing below.
	default:
		// An unclassified stage fails closed as ignorance rather than as risk.
		// Reporting it to a human as an authority question would ask them to
		// adjudicate something nobody has evidence about.
		return Routing{
			Route:     RouteCannotEstablish,
			Condition: "this action's consequences could not be established: " + consequences.Boundary + consequenceSignalSuffix(spots),
		}
	}

	// Epistemic incompleteness. Nothing below grants; each names work that must
	// happen before the question can be asked properly.
	//
	// This precedes the unclassified-gate check on purpose. When the graph
	// covers nothing, it has nothing to classify either: the missing verdict
	// and the missing coverage are one absence, and reporting it as "nobody
	// judged what this change costs" dresses an epistemic hole as a consequence
	// verdict. That is the same conflation this file exists to undo.
	// A machine-derived fact may close a coverage gap, but only where a
	// derivation succeeded in THIS world over THESE files. The caller
	// revalidated to obtain the list; nothing here reads a stored record.
	//
	// It closes the gap rather than granting anything: the consequence checks
	// above have already run, and the premise checks below still apply.
	//
	// Truthfulness is necessary and not sufficient. The relation below asks
	// whether the derivation RESOLVES this gap, not merely whether it is true
	// over the same files -- subject overlap alone let a wide irrelevant truth
	// manufacture coverage. See relevance.go.
	// A planned file the graph never examined is uncovered whatever the region
	// says. The scoped answer is one verdict for all the planned files, and it
	// is proven the moment one of them carries anchors -- live, over
	// [engine.go, a file that does not exist]: sufficient=true,
	// direct_anchor_count=3, file_count=2, indexed_file_count=1. Read at the
	// region, the second file inherited the first one's coverage, and a plan
	// could carry any ungrounded file into an anchored region and launder the
	// region's authority onto it (M25 §1: authority is not inherited from a
	// neighbour). The per-file fact is engine-owned (Action.Unexamined); a
	// file under an operational grant is not asked to be examined. The gap
	// closes the way every coverage gap closes -- a recognised derivation
	// over every architectural file -- and its identity is the unexamined
	// files, so the closure budget is spent on them and not on the region.
	if gap, open := unexaminedCoverageGap(action, spots); open {
		return gap
	}
	if coverageAbsent {
		// Files holding an operational grant are not asked to be covered:
		// they are authorised to be edited, which is a different thing, and
		// the question put to the derivations is about the rest.
		//
		// Decided per member by the one typed owner (regionCoverageGap): a
		// confirmed-absent create settles only by its recorded prospective
		// unit, a present file only by a derivation over it. Examination
		// settles nothing here -- this gap is about the region, not about
		// whether a present file was examined.
		//
		// A plan with no architectural member has a zero-member disposition,
		// which closes the coverage concern: a coverage gap over its other
		// files would ask the region's question of files that do not answer
		// to it. Closing it admits nothing -- those files stay with their own
		// evidence owners (regionArtifactGap), and the consequences are
		// judged below.
		//
		// Sending an open one to a human asks them to supply coverage Sensei
		// lacks -- a technical answer -- and answering leaves the graph exactly
		// as empty as before, so the next task over the same region asks again.
		if r, open := regionCoverageGap(gapCoverageAbsent, scoped, action, spots); open {
			return r
		}
	}

	// An unclassified gate on a preflight that DOES hold coverage is a
	// different animal: the graph has anchors here and still reached no
	// verdict, so nobody has judged what the change costs. Not a default to
	// proceed on, and not obviously bounded work either — across 135 probed
	// files it never fired once, and reclassifying a branch with no
	// observations behind it would be guessing.
	if !scoped.ChangeRisk.Classified() {
		return Routing{
			Route:     RouteHuman,
			Basis:     BasisLacksKnowledge,
			Closes:    "a change-risk classification for this region; the router has no verdict to read, not a verdict it disagrees with",
			Condition: "Sensei classified no approval gate for the planned region",
		}
	}

	// Blind spots are read, not counted. See blindspot.go for the measured
	// vocabulary and why the two kinds are opposites.
	if len(scoped.BlindSpots) != 0 {
		spots := readBlindSpots(scoped.BlindSpots)
		switch {
		case len(spots.Unrecognised) != 0:
			// Fail closed. A blind spot nobody has classified must not become
			// bounded work by default, or every future addition to Sensei's
			// vocabulary becomes silent autonomy.
			return Routing{
				Route:  RouteHuman,
				Basis:  BasisLacksKnowledge,
				Closes: "a reading for that blind-spot phrasing in blindspot.go; until one exists the router cannot tell whether the signal is ignorance or risk, so it costs a person's attention without protecting anything",
				Condition: "Sensei reported a blind spot this router has no reading for: " +
					strings.Join(spots.Unrecognised, ", "),
			}
		case len(spots.Coverage) != 0:
			// The same question the coverage-absent branch asks, asked here
			// too: does a derivation over these files RESOLVE this gap?
			//
			// It was not asked here. The first cold-start run to reach link 5
			// -- a true lock discipline, DERIVED inside the governed run, one
			// anchor over the one planned file -- still routed to bounded work,
			// because this branch never looked. Two branches for the same
			// family of gap, "the graph does not vouch for this region", and
			// only one of them wired to the channel built to close it.
			//
			// Same relevance gate, same fail-closed rule: an unrecognised
			// family resolves nothing, and an unqualified gap is satisfied only
			// by a family this consumer has named as an answer. When it closes,
			// the coverage signals are spent and the remaining consequence
			// signals -- if any -- are judged by the default arm exactly as
			// they would be on a covered region.
			//
			// And the same subtraction as that branch: a planned file under an
			// operational grant is not asked to be covered. B3's N1b found
			// this branch asking the derivation to cover a granted test file
			// -- one anchor over the covered source plus one grant over the
			// test read as "1 anchor over 2 files" and routed cold -- the
			// two-branches-one-wired defect this comment already describes,
			// recurring one seam over (M2.2 had been wired into the other
			// branch only). The identity below is what keeps its closure
			// budget honest.
			//
			// A plan that names files is decided per member, as in the
			// coverage-absent branch: a zero-member disposition closes the
			// coverage signals, and its non-architectural files stay with
			// their own evidence owners (regionArtifactGap). Only an action
			// that names no file at all keeps the region's question whole.
			if r, open := regionCoverageGap(gapCoverageBlindSpot, scoped, action, spots); open {
				return r
			}
			spots.Coverage = nil
			fallthrough
		default:
			// Only consequence signals remain: severity, path class, namespace.
			// These describe knowledge the graph HAS, not knowledge it lacks,
			// and on 22 of the 26 measured OK files they escalate a region the
			// risk channel had already classified APPROVAL_GATE_NONE.
			//
			// Deferring them to that gate would make those 22 grantable. That
			// is a policy choice about who may edit high-risk paths unattended
			// — a consequence and value question — and it is NOT taken here.
			// The route is unchanged; only the condition is corrected, so the
			// escalation stops describing strong knowledge as a blind spot.
			//
			// Declared as dq.consequence_blind_spot_authority rather than
			// decided in passing. Widening a router to improve a coverage
			// number is the failure this line of work exists to avoid.
			//
			// Nothing is decided here any more. A consequence signal says LOOK
			// HARDER HERE; it does not say who decides, and what decides is
			// whether THIS action's consequences are bounded -- which was
			// established above, for every action, and would have returned
			// already had it been anything but bounded. The signals were
			// carried into that decision's condition, so an escalation still
			// names them.
		}
	}

	// Provenance is read from a closed vocabulary, and only "graph" and
	// "repository" are evidence.
	//
	// An inferred premise is the architect telling us, in its own words, that
	// this part of the plan rests on something nothing checked. That is a
	// verification task, not a decision a human owns: what is being asked for
	// is evidence, and the architect is the one who can go and get it.
	//
	// Anything else is read the same way, and that is the point. Claim.Source
	// is whatever the model typed: decodeModelJSON validates no field, so a
	// blank source, a misspelling, or a word this router has no reading for
	// arrives looking exactly like a checked premise. Recognising only
	// "inference" made every one of those grantable -- an unchecked premise
	// acquired architectural authority by being labelled with something nobody
	// defined. The same fail-closed rule as an unrecognised blind spot above,
	// for the same reason: a later addition to the vocabulary must not become
	// silent autonomy by arriving before the router has a reading for it.
	for _, c := range claims {
		source := strings.ToLower(strings.TrimSpace(c.Source))
		if source == "graph" || source == "repository" {
			continue
		}
		statement := strings.TrimSpace(c.Statement)
		if statement == "" {
			statement = "(unstated)"
		}
		about := strings.TrimSpace(c.About)
		if about != "" {
			about = " about " + about
		}
		if source == "inference" {
			return Routing{
				Route:     RouteCloseGap,
				Condition: "the plan rests on an unverified premise" + about + ": " + statement,
				Gap:       GapIdentity{Kind: "unverified-premise", Subject: gapSubject(c.About, action.Files), Scope: action.Files},
				ClaimGap:  c.Gap,
			}
		}
		// The provenance is named, because the work this route asks for is not
		// the same work: an inference needs evidence gathered, while this needs
		// the premise re-stated with a source that can be checked at all.
		return Routing{
			Route: RouteCloseGap, Basis: BasisLacksKnowledge,
			Condition: "the plan rests on an unverified premise" + about +
				" (" + declaredSource(c.Source) + "): " + statement,
			Gap:      GapIdentity{Kind: "unrecognised-premise-source", Subject: gapSubject(c.About, action.Files), Scope: action.Files},
			ClaimGap: c.Gap,
		}
	}

	return Routing{Route: RouteArchitectural}
}

// declaredSource renders what the architect actually wrote in a claim's source
// field, so a routing caused by unreadable provenance says which provenance.
//
// A missing source and a misspelled one are different mistakes and are reported
// as different mistakes: the first is a claim that never stated where it came
// from, the second is a claim that stated something this router cannot read.
func declaredSource(source string) string {
	declared := strings.TrimSpace(source)
	if declared == "" {
		return "no source was declared"
	}
	return "unrecognised source " + strconv.Quote(declared)
}

// escalationCondition renders a routing for a human, so a Level-3 interruption
// always arrives with the condition that caused it attached.
func escalationCondition(r Routing) string {
	if r.Condition == "" {
		return string(r.Route)
	}
	return fmt.Sprintf("%s: %s", r.Route, r.Condition)
}

// consequenceSignalSuffix names the graph's consequence signals for a
// consequence decision that escalates or refuses.
//
// The signals are context for the decision, never its cause: the assessment
// reads the action, and reaches the same result whether or not the graph
// happened to publish a blind spot about the region. Rendering them here keeps
// the message the old blind-spot branch produced without restoring the
// dependency that produced it.
func consequenceSignalSuffix(spots blindSpotReading) string {
	if len(spots.Consequence) == 0 {
		return ""
	}
	return " (consequence signals in the planned region: " + strings.Join(spots.Consequence, ", ") + ")"
}

// unexaminedCoverageGap is the coverage gap the unexamined planned files open,
// and whether it is open. Asked by the router after the consequence checks, and
// asked AGAIN by the engine once a human has authorised a consequence -- the
// gate is answered first, and the answer is about the consequence, not about
// coverage, so a file the graph never examined is not admitted by it.
//
// The gap is decided over ITS OWN members, the unexamined architectural files,
// and nothing else (DF-30). It once asked a recognised derivation to cover
// every architectural file, so a planned file the graph had examined -- already
// positively governed -- was required to acquire a derived anchor because some
// other file was unexamined, and a granted create was told to close by graph
// examination of a file that does not exist (objective 49, run 7). Each member
// is now settled only by what can settle it: its own recorded prospective unit
// when it is a confirmed-absent create, a derivation over it when it is not.
func unexaminedCoverageGap(action Action, spots blindSpotReading) (Routing, bool) {
	unexamined := action.unexaminedArchitecturalFiles()
	// PRODUCTION SOURCE BLOCKS FIRST, because it is the stronger claim: a file a
	// derivation could cover and does not is a different and heavier absence than a test
	// whose neighbour is missing from the plan.
	//
	// Both no-production-gap exits fall through to the test question. An earlier draft
	// only handled the empty case, and a plan whose production gap was closed by a
	// derivation would then have carried an ungranted test out silently -- the same
	// silence this slice exists to end, one branch over.
	if len(unexamined) != 0 {
		sem := coverageSemantics(gapCoverageUnexamined, spots.Coverage, "")
		if disposition := disposeCoverageGap(gapCoverageUnexamined, unexamined, action, sem.Requirement); disposition.Open() {
			return renderCoverageGap(disposition, sem), true
		}
	}
	if r, open := ungrantedTestGovernanceGap(action); open {
		return r, true
	}
	if r, open := documentGovernanceGap(action); open {
		return r, true
	}
	return unsupportedArtifactGap(action)
}

// regionCoverageGap is the coverage gap a region answer of kind
// (coverage-absent or coverage-blind-spot) opens over this action, and whether
// it is open: the one rendering of a region gap, used by the router and by the
// post-authorization continuation alike (coverageAfterAuthorization), so a
// region question is decided by one disposition whichever route asks it.
//
// A plan that names files is decided per member by disposeCoverageGap; a
// zero-member disposition closes the coverage concern and leaves the plan's
// other files with their own evidence owners (regionArtifactGap). Only an
// action that names no file at all keeps the region's question whole.
func regionCoverageGap(kind string, scoped sensei.PreflightDecision, action Action, spots blindSpotReading) (Routing, bool) {
	sem := coverageSemantics(kind, spots.Coverage, scoped.Coverage.Diagnostic())
	if len(action.Files) == 0 {
		if kind == gapCoverageAbsent {
			// The condition states the evidence rather than the status,
			// because the two came apart: a preflight can answer EMPTY while
			// publishing sufficient coverage, and it can answer OK while
			// proving none.
			return Routing{Route: RouteCloseGap, Basis: BasisLacksKnowledge,
				Condition: "graph coverage is absent for the planned files: " + scoped.Coverage.Diagnostic(),
				Gap:       canonicalQuestion(GapIdentity{Kind: gapCoverageAbsent, Scope: action.Files, Semantics: &sem})}, true
		}
		return Routing{Route: RouteCloseGap,
			Condition: "Sensei reported missing coverage in the planned region: " + strings.Join(spots.Coverage, ", "),
			Gap:       canonicalQuestion(GapIdentity{Kind: gapCoverageBlindSpot, Semantics: &sem})}, true
	}
	if disposition := disposeCoverageGap(kind, action.architecturalFiles(), action, sem.Requirement); disposition.Open() {
		return renderCoverageGap(disposition, sem), true
	}
	return regionArtifactGap(action)
}

// coverageAfterAuthorization is every coverage question the router would have
// asked had the consequence a human just authorised not stopped it first: the
// unexamined planned files, then the region's coverage-absent and
// coverage-blind-spot questions, each decided by the same typed disposition
// the router uses (DF-30A review f1). The answer satisfies only the
// consequence it was about; it settles no coverage, so a gated region gap
// whose present members were merely examined stays open after it exactly as
// it would without the gate.
func coverageAfterAuthorization(scoped sensei.PreflightDecision, action Action) (Routing, bool) {
	spots := readBlindSpots(scoped.BlindSpots)
	if gap, open := unexaminedCoverageGap(action, spots); open {
		return gap, true
	}
	if !scoped.Coverage.Proven() {
		if gap, open := regionCoverageGap(gapCoverageAbsent, scoped, action, spots); open {
			return gap, true
		}
	}
	if len(spots.Coverage) != 0 {
		return regionCoverageGap(gapCoverageBlindSpot, scoped, action, spots)
	}
	return Routing{}, false
}

// ungrantedTestGovernanceGap is the typed knowledge limit for a test artifact whose
// test-governance relation could not be established.
//
// It reuses the existing vocabulary -- GapIdentity + BasisLacksKnowledge -- rather than
// introducing a parallel terminal model. What is new is only the KIND, because the
// distinction that matters is which evidence is missing:
//
//	missing source evidence           a derivation could cover the file and none does
//	missing test-governance evidence  no covered production file in this test's own
//	                                  directory and package, so
//	                                  EXISTING_TEST_EDIT_ADMISSIBLE cannot hold
//
// Collapsing them sent W3 to rebuild a graph that could never supply the second.
func ungrantedTestGovernanceGap(action Action) (Routing, bool) {
	tests := action.ungrantedTestArtifacts()
	if len(tests) == 0 {
		return Routing{}, false
	}
	return Routing{Route: RouteCloseGap, Basis: BasisLacksKnowledge,
		Condition: "test-governance evidence is absent for planned test file(s): no covered production file in the same directory and package establishes an existing-test edit grant: " + strings.Join(tests, ", "),
		Gap:       GapIdentity{Kind: gapTestGovernanceUnestablished, Scope: tests}}, true
}

// regionArtifactGap is the artifact-specific question a region coverage gap
// leaves behind when the plan has NO architectural member, so the gap's
// disposition is zero-member and closes. Before that disposition existed the
// region gap itself held such a plan; closing it must not turn into silent
// admission of what it held. A plan with architectural members is unchanged:
// its gap was always decided over those members alone. The region's
// coverage signal is the graph saying it does not vouch for these files, so a
// planned test that holds no operational grant cannot be read as one the graph
// already governs: unexaminedCoverageGap asks that only of a test a per-file
// probe found unexamined, and tests are never probed. It is put to its own
// owner, test governance, rather than to a coverage question it cannot answer
// or to silence. Documents and unsupported artifacts were already put to their
// owners by unexaminedCoverageGap, which runs first on every route.
//
// A test a valid prospective unit settles -- confirmed absent, as every
// member of its unit is -- holds the governance that unit's admission gave it.
func regionArtifactGap(action Action) (Routing, bool) {
	if len(action.architecturalFiles()) != 0 {
		return Routing{}, false
	}
	granted, absent := map[string]bool{}, map[string]bool{}
	for _, f := range action.OperationalAuthority {
		granted[path.Clean(strings.TrimSpace(f))] = true
	}
	for _, f := range action.Absent {
		absent[path.Clean(strings.TrimSpace(f))] = true
	}
	var tests []string
	for _, f := range action.Files {
		c := path.Clean(strings.TrimSpace(f))
		if classifyArtifact(c) == classTestGo && !granted[c] && !(absent[c] && action.settledByProspective(c, absent)) {
			tests = append(tests, c)
		}
	}
	if len(tests) == 0 {
		return Routing{}, false
	}
	return ungrantedTestGovernanceGap(Action{Files: tests, Unexamined: tests})
}

// documentGovernanceGap is the typed knowledge limit for a document whose governance could
// not be established.
//
// The distinction it preserves is the Coverage type's own doctrine: the graph having
// looked and found no protecting invariant means ORDINARY DOCUMENTATION, and the graph
// never having looked means nothing at all. Both render as an empty invariant list, and
// only the first is evidence — so only the second raises this.
func documentGovernanceGap(action Action) (Routing, bool) {
	docs := action.ungovernedDocumentArtifacts()
	if len(docs) == 0 {
		return Routing{}, false
	}
	return Routing{Route: RouteCloseGap, Basis: BasisLacksKnowledge,
		Condition: "architecture-document evidence is absent for planned document(s): no governed invariant is known to protect them, and nothing established that they are ordinary documentation: " + strings.Join(docs, ", "),
		Gap:       GapIdentity{Kind: gapDocumentGovernanceUnestablished, Scope: docs}}, true
}

// unsupportedArtifactGap is the explicit knowledge limit for an artifact kind no evidence
// class covers. It exists so "we have no way to prove anything about this" is a stated
// verdict rather than a silent pass.
func unsupportedArtifactGap(action Action) (Routing, bool) {
	files := action.unsupportedArtifacts()
	if len(files) == 0 {
		return Routing{}, false
	}
	return Routing{Route: RouteCloseGap, Basis: BasisLacksKnowledge,
		Condition: "no evidence class governs planned artifact(s) of this kind: " + strings.Join(files, ", "),
		Gap:       GapIdentity{Kind: gapUnsupportedArtifact, Scope: files}}, true
}

const (
	gapDocumentGovernanceUnestablished = "document-governance-unestablished"
	gapUnsupportedArtifact             = "unsupported-artifact-kind"
)

// gapTestGovernanceUnestablished is the kind name, shared by the remedy and the closure
// owner so the three cannot drift.
const gapTestGovernanceUnestablished = "test-governance-unestablished"

// remedyForGap names the concrete action that would close a typed gap.
//
// Dispatch rather than one remedy, because the remedies are not interchangeable: no graph
// operation can create a test-edit grant, so offering `sensei import --refresh` for a
// test-governance gap would be a true-sounding instruction that cannot work -- the same
// defect as the gap conflation, one layer out.
func remedyForGap(gap GapIdentity, root, domain string) string {
	switch gap.Kind {
	case gapDocumentGovernanceUnestablished:
		return "no graph refresh can establish this, and no Go source property can stand in " +
			"for it: a governed architecture document is proven by the INVARIANT that protects " +
			"it (docs/awareness/invariants.yaml, protects.files).\n" +
			"  unestablished for: " + strings.Join(gap.Scope, ", ") + "\n" +
			"  close it by consulting the graph for these paths, or -- if the repository's " +
			"architecture rules say a document in this role should be governed -- by proposing " +
			"the invariant that protects it, which is a knowledge-admission act and stays " +
			"human-authorized."
	case gapUnsupportedArtifact:
		return "no evidence class governs this artifact kind, so nothing can prove it here.\n" +
			"  artifact(s): " + strings.Join(gap.Scope, ", ") + "\n" +
			"  close it by removing them from the plan, or by defining the evidence class that " +
			"governs them -- not by asking a Go derivation to read them."
	}
	if gap.Kind == gapTestGovernanceUnestablished {
		return "no graph operation can establish this: a test artifact is governed by an " +
			"existing-test edit grant, which requires a planned production file in the SAME " +
			"directory and package that a derived anchor covers at this world.\n" +
			"  missing for: " + strings.Join(gap.Scope, ", ") + "\n" +
			"  close it by including that production file in the plan and giving it derived " +
			"coverage, or by planning the test edit as its own governed change."
	}
	return knowledgeLimitRemedy(root, domain, gap.Scope)
}

// degradedReason renders why a degraded preflight could not be read as a
// coverage gap, so the refusal names its own evidence.
func degradedReason(spots []string) string {
	if len(spots) == 0 {
		return "the preflight reported no blind spots, so nothing says what is degraded"
	}
	return strings.Join(spots, "; ")
}

// gapClosureOwner says who is CAPABLE of closing a bounded gap class.
//
// The law: a bounded knowledge gap may only be assigned to a closure actor
// capable of changing the evidence whose absence caused the gap. Routing a gap
// to an actor that cannot change that evidence produces exactly what was
// measured on task-1789272620293170079 -- rounds that cannot succeed, followed
// by a question the human cannot answer either.
type gapClosureOwner int

const (
	// closureOwnerReasoning: the architect can settle it by thinking, reading
	// what the graph already holds, or narrowing the plan. An unverified premise
	// is this: the evidence exists and the question is what it implies.
	closureOwnerReasoning gapClosureOwner = iota
	// closureOwnerOutOfBand: closing it requires changing the graph's coverage,
	// which no operation reachable from a governed run can do. Reported with its
	// remedy; never asked of the architect twice and never asked of a human.
	closureOwnerOutOfBand
)

// closureOwnerFor types a gap class by who could close it.
//
// Deliberately narrow: only the class actually measured as unclosable is typed
// out-of-band. An unrecognised kind keeps the pre-existing reasoning owner, so
// this repair cannot widen the set of gaps that stop a run.
func closureOwnerFor(kind string) gapClosureOwner {
	switch strings.TrimSpace(kind) {
	case gapCoverageUnexamined, gapTestGovernanceUnestablished,
		gapDocumentGovernanceUnestablished, gapUnsupportedArtifact:
		// Neither is closable by reasoning: one needs a derivation to run, the other a
		// production neighbour in the plan. A round spent thinking closes neither.
		return closureOwnerOutOfBand
	}
	return closureOwnerReasoning
}

// knowledgeLimitRemedy is the minimum concrete operator action that would close
// a coverage limit, in the shape the CLI actually accepts.
//
// `sensei import --refresh` takes a CHECKOUT PATH and a --domain; it does not
// take a file list. It also "never auto-promotes: extractors write
// candidates/intents for you to review and promote yourself", and "never touches
// a store unless --store-url is given" -- so the remedy names the reload
// explicitly rather than implying the served graph updates by itself.
//
// The files are named as WHAT IS MISSING, not as arguments. A remedy that passed
// them to --refresh would not run.
func knowledgeLimitRemedy(root, domain string, missing []string) string {
	cmd := "sensei import --refresh " + root
	// A domain, not a revision. The first live run of this remedy printed
	// `--domain f304cfec6f43...` because the caller passed GapIdentity.World,
	// which is the world the gap was measured in -- a commit sha. The command
	// would not have run. A bare hex string is never a domain, so it is refused
	// here rather than emitted as an argument that looks plausible.
	if d := strings.TrimSpace(domain); d != "" && !looksLikeRevision(d) {
		cmd += " --domain " + d
	}
	return "graph examination of " + strings.Join(missing, ", ") +
		"; the supported operator action is `" + cmd +
		"` (add --store-url and --graph-marker-file to reload the served store). " +
		"This is reported, not performed: a governed run must not change the graph it is governed by, " +
		"and extraction writes candidates for review rather than promoting them."
}

// disposeUnclosedGap decides what a bounded knowledge gap becomes when its one
// closure round is spent and the router still reports it.
//
// Two outcomes, chosen by who could close it:
//
//   - reasoning-owned, or coverage that the plan no longer depends on: escalate
//     as before. What the human is asked is not the technical question, it is
//     whether to proceed with the gap open -- a decision they can actually make.
//   - out-of-band with coverage still missing: a knowledge limit. No further
//     round is spent, no human is asked, and the stop carries the remedy.
//
// The second case is the repair. Asking a human to authorise work over absent
// coverage was answerable but useless: authorising does not examine a file, so
// the router reached the identical condition on the next plan and the person was
// asked again -- observed twice on task-1789272620293170079.
// looksLikeRevision reports whether a string is a git object id rather than a
// domain. Used to keep a revision out of --domain, where it would be accepted as
// text and produce a command that cannot work.
func looksLikeRevision(s string) bool {
	if len(s) < 7 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func (e *Engine) disposeUnclosedGap(taskID, domain string, routing Routing, action Action) (Routing, error) {
	// WHAT IS MISSING DEPENDS ON THE GAP'S TYPE. Reading the unexamined architectural
	// files for every out-of-band gap was right while there was one such gap; a
	// test-governance gap is about test artifacts, which are deliberately NOT in that
	// set, so it would have found nothing missing and fallen through to an ordinary
	// human escalation — asking a person to supply evidence no person can supply.
	missing := action.unexaminedArchitecturalFiles()
	switch routing.Gap.Kind {
	case gapTestGovernanceUnestablished, gapDocumentGovernanceUnestablished, gapUnsupportedArtifact:
		missing = routing.Gap.Scope
	case gapCoverageUnexamined, gapCoverageAbsent, gapCoverageBlindSpot:
		// The gap's own typed disposition: its Scope IS the unresolved set the
		// one owner computed (renderCoverageGap), so a member settled by its
		// prospective unit or a derivation is never reported missing here.
		// Recomputing the set from Action.Unexamined reported a granted create
		// as unexamined and prescribed graph examination of a file that does
		// not exist (objective 49, run 7). Only the members this plan still
		// depends on remain a limit.
		//
		// All three coverage kinds take this one disposition (ruling 80). A
		// region gap is closed only by a derivation over its present members
		// or by prospective authority over its absent creates -- neither of
		// which a human's answer supplies -- so an unresolved region member is
		// a knowledge limit like an unexamined one, never converted into a
		// question about proceeding with it open.
		//
		// Read from the episode's ledger entry -- its current Gap, rendered
		// condition and typed disposition, as the episode's latest
		// same-identity re-evaluation left them (reconcileCoverageGaps) --
		// and consumed verbatim. Which members the plan still depends on was
		// decided THERE, together with scope and condition; nothing here
		// re-derives membership from the action, so no unresolved member can
		// be dropped by a second rule.
		current, registered := e.currentResolution(taskID, routing.Gap)
		routing.Gap = current.Gap
		if current.Routing.ClosesGap() {
			routing.Condition = current.Routing.Condition
		}
		missing = append([]string(nil), current.Gap.Scope...)
		if current.Disposition != nil {
			missing = append([]string(nil), current.Disposition.Unresolved...)
		}
		if !registered {
			// No routing registered this gap, so no typed re-evaluation ever
			// decided which members the plan still depends on and there is no
			// stored disposition to consume. Every production route registers
			// a gap before disposing of it (registerRouting); this reading
			// exists only for a gap handed here without one, and it can only
			// shrink what is reported, never settle a member.
			planned := map[string]bool{}
			for _, f := range action.Files {
				planned[path.Clean(strings.TrimSpace(f))] = true
			}
			kept := missing[:0]
			for _, f := range missing {
				if planned[path.Clean(strings.TrimSpace(f))] {
					kept = append(kept, f)
				}
			}
			missing = kept
		}
	}
	// Coverage the plan no longer depends on is not a limit: the architect
	// narrowed onto examined material, which is the legitimate escape, and the
	// remaining stop is an ordinary escalation.
	if (closureOwnerFor(routing.Gap.Kind) == closureOwnerOutOfBand || isCoverageGapKind(routing.Gap.Kind)) && len(missing) > 0 {
		routing.Basis = BasisLacksKnowledge
		routing.Closes = remedyForCoverage(routing.Gap, action, e.Repo.Root, domain)
		// Its account is recorded by the invocation that disposed of the gap
		// (reportKnowledgeLimit): this owner decides, and names no invocation.
		return routing, &knowledgeLimitError{Condition: routing.Condition, Missing: missing, Closes: routing.Closes}
	}
	routing.Route = RouteHuman
	routing.Condition = "a bounded knowledge gap was not closed by investigation: " + routing.Condition
	return routing, nil
}

// disposeExhaustedGap is the exhausted-gap disposal the post-authorization
// route takes once the gap its answer left open has spent its closure budget.
// It is disposeUnclosedGap -- the one typed owner of what an unclosed gap
// becomes -- over the gap and the facts it was decided on, so this route can
// neither convert a coverage gap of any of the three kinds to a human question
// by hand nor recompute what it lacks from anything but the gap's own
// disposition.
func (e *Engine) disposeExhaustedGap(taskID, domain string, gap Routing, action Action) (Routing, error) {
	return e.disposeUnclosedGap(taskID, domain, gap, action)
}

// reportKnowledgeLimit records, on behalf of the invocation ctx belongs to,
// the account of a knowledge limit a disposal (disposeUnclosedGap) returned
// for routing; any other err records nothing.
func (e *Engine) reportKnowledgeLimit(ctx context.Context, taskID string, routing Routing, err error) {
	limit, ok := err.(*knowledgeLimitError)
	if !ok || limit == nil {
		return
	}
	e.emitIn(ctx, event.New(e.SessionID, taskID, event.SourceSensei, event.Status,
		"knowledge-limited: no actor reachable from a governed run can establish what "+
			strings.Join(limit.Missing, ", ")+" lacks; this is not a decision a human can supply. "+
			"closes: "+limit.Closes, routing))
}

// knowledgeLimitError reports a bounded knowledge gap that NO actor reachable
// from this engine can close.
//
// It is not a failure of the work and not a question for a person. The graph has
// not examined some planned file, and the only operations that change that --
// `sensei import` / `bootstrap` / `build` / `rebuild` -- are CLI stages outside a
// governed run. The engine's own awareness surface is read-only with respect to
// coverage: audit_diff, edit_check and preflight read, and investigate and
// candidates are documented "read-only and candidate-only; never promotes
// knowledge".
//
// So the honest report is the limit plus its remedy, which is what Closes
// carries. Asking a human instead was the observed defect: authorizing does not
// make a file examined, so the router reaches the same condition on the next
// plan and the person is asked again.
type knowledgeLimitError struct {
	// Condition is the router's own wording, kept verbatim.
	Condition string
	// Missing are the planned files the graph has not examined.
	Missing []string
	// Closes is the remedy, in the shape Routing.Closes carries it.
	Closes string
}

func (e *knowledgeLimitError) Error() string {
	return "this task cannot be governed further without knowledge the graph does not hold: " + e.Condition +
		"\nunexamined: " + strings.Join(e.Missing, ", ") +
		"\ncloses: " + e.Closes
}

// premisesUnderRecordedAuthority separates the plan's premises into those the
// router reads and those a RECORDED grant already answers.
//
// Grants this engine recorded for the plan attempt being routed -- at its
// task, its pinned world and its PlanAttemptID -- are established governance
// facts. A premise asserting that exactly such a grant is unestablished is
// contradicted by the record: the record governs routing, and the premise is
// returned as an architect-plan inconsistency rather than opening a bounded
// knowledge gap the record has already closed.
//
// The predicate is structural and exact, never a reading of prose. A premise
// is contradicted only when ALL of these hold:
//
//   - it carries an AuthorityPremise whose State is "unestablished" and whose
//     Requirement is one of the two kinds, by exact membership;
//   - it is about exactly that path (About is the Path), so it says nothing
//     else that could be lost with it;
//   - the plan declares that requirement: the same path among its
//     prospective_surfaces, or among its test_edits;
//   - the grant record of that kind written for THIS attempt, at THIS
//     attempt's world, holds a grant for that path.
//
// Every other premise -- prose about grants included, a premise naming two
// requirements, one whose grant was recorded for another attempt or world, or
// was never recorded -- is kept exactly as it was, so no independent gap is
// lost and a genuinely missing grant still opens its gap.
func (e *Engine) premisesUnderRecordedAuthority(taskID string, d architectureDecision) (kept, contradicted []Claim) {
	attempt := e.pendingPlanAttempt(taskID)
	if attempt.ID == "" {
		return d.Claims, nil
	}
	// The records written for exactly this attempt; nothing recorded for
	// another attempt is looked at.
	prospective, edits := e.recordedGrants(taskID, attempt.ID)
	recorded := func(m AuthorityPremise) bool {
		switch m.Requirement {
		case premiseProspectiveCreate:
			declared := false
			for _, s := range d.ProspectiveSurfaces {
				declared = declared || s.Path == m.Path
			}
			if !declared || prospective.World != attempt.World {
				return false
			}
			for _, g := range prospective.Grants {
				if g.Surface.Path == m.Path {
					return true
				}
			}
		case premiseTestEdit:
			declared := false
			for _, t := range d.TestEdits {
				declared = declared || t.Path == m.Path
			}
			if !declared {
				return false
			}
			for _, g := range edits.Grants {
				if g.Path == m.Path && g.World == attempt.World {
					return true
				}
			}
		}
		return false
	}
	for _, c := range d.Claims {
		m := c.Authority
		if m == nil || m.State != premiseAuthorityUnestablished || m.Path == "" ||
			strings.TrimSpace(c.About) != m.Path || !recorded(*m) {
			kept = append(kept, c)
			continue
		}
		contradicted = append(contradicted, c)
	}
	return kept, contradicted
}

// The three coverage-gap kinds the one typed disposition owner decides.
const (
	gapCoverageUnexamined = "coverage-unexamined"
	gapCoverageAbsent     = "coverage-absent"
	gapCoverageBlindSpot  = "coverage-blind-spot"
)

// isCoverageGapKind reports membership in the closed set above.
func isCoverageGapKind(kind string) bool {
	switch kind {
	case gapCoverageUnexamined, gapCoverageAbsent, gapCoverageBlindSpot:
		return true
	}
	return false
}

// gapSettlement is the governed mechanism that settled one gap member.
type gapSettlement string

const (
	// settledByExamination: a per-file preflight examined a file confirmed
	// present at the pinned world. Settles coverage-unexamined only.
	settledByExamination gapSettlement = "examination"
	// settledByProspectiveGrant: the file is confirmed absent at the pinned
	// world and a member of a valid recorded prospective authority unit.
	settledByProspectiveGrant gapSettlement = "prospective-grant"
	// settledByDerivation: a recognised derivation that satisfies the gap's
	// requirement covers the file, which is confirmed present.
	settledByDerivation gapSettlement = "derivation"
)

// GapDisposition is the ONE typed answer to "which members of this coverage gap
// remain unresolved" (DF-30, rulings 79, 80 and 176). Every routing site --
// initial and supplied-plan routing, the post-authorization re-evaluation, the
// escalation, the exhausted-gap disposal and the same-identity reconciliation
// of a prior gap -- obtains it from disposeCoverageGap and renders it through
// renderCoverageGap, so typed scope and readable condition cannot diverge.
type GapDisposition struct {
	Kind string
	// Unresolved are the members no applicable mechanism settled, in member
	// order. The gap is open exactly when it is non-empty.
	Unresolved []string
	// Settled names, per settled member, the mechanism that settled it.
	Settled map[string]gapSettlement
	// Withdrawn are members the modifying plan no longer names. Not settled:
	// the plan stopped depending on them, which reconcileCoverageGaps alone
	// decides, and a later routing that names one again reopens the episode.
	Withdrawn []string `json:",omitempty"`
}

// Open reports whether any member remains unresolved.
func (d GapDisposition) Open() bool { return len(d.Unresolved) != 0 }

// Narrowed reports whether any member was settled, by whichever mechanism, or
// withdrawn by the plan: the gap no longer describes every member it was
// decided over.
func (d GapDisposition) Narrowed() bool { return len(d.Settled) != 0 || len(d.Withdrawn) != 0 }

// disposeCoverageGap decides each member of a coverage gap of kind, and only
// with the mechanism that kind admits:
//
//   - examination settles a member only of coverage-unexamined, and only when
//     the file is confirmed PRESENT at the pinned world and its own per-file
//     preflight examined it. A region gap's question is not "was this present
//     file examined", so examination never settles one;
//   - a prospective grant settles a member only when the file is confirmed
//     ABSENT at the pinned world and belongs to a VALID recorded authority
//     unit every member of which -- with any unit it depends on -- the world
//     also confirms absent. Absence alone, a merely declared surface, and a
//     unit only part of which is confirmed absent settle nothing;
//   - a derivation settles a member it covers with a satisfying requirement
//     only when the file is confirmed PRESENT at the pinned world: no
//     derivation can observe a file that does not exist, and a member whose
//     presence is unknown is not thereby known to exist. In a real run every
//     derived anchor is over a confirmed-present file (coverPlannedAtWorld),
//     and prospective grants are never projected into ordinary coverage.
//
// Every other member stays unresolved. Missing presence, probe or grant data
// is never settlement: a member in neither Present nor Absent is settled by no
// mechanism, whatever DerivedCoverage or prospective authority names -- even
// when the pinned world was not read for any member. Members are canonicalised
// as the gap identity is.
func disposeCoverageGap(kind string, members []string, action Action, req Requirement) GapDisposition {
	set := func(files []string) map[string]bool {
		out := make(map[string]bool, len(files))
		for _, f := range files {
			out[path.Clean(strings.TrimSpace(f))] = true
		}
		return out
	}
	present, absent, examined := set(action.Present), set(action.Absent), set(action.Examined)
	d := GapDisposition{Kind: kind, Settled: map[string]gapSettlement{}}
	seen := map[string]bool{}
	for _, m := range members {
		f := path.Clean(strings.TrimSpace(m))
		if f == "." || seen[f] {
			continue
		}
		seen[f] = true
		switch {
		case kind == gapCoverageUnexamined && present[f] && examined[f]:
			d.Settled[f] = settledByExamination
		case absent[f] && action.settledByProspective(f, absent):
			d.Settled[f] = settledByProspectiveGrant
		case present[f]:
			if closed, _ := derivationClosesGap(req, action.DerivedCoverage, []string{f}); closed {
				d.Settled[f] = settledByDerivation
				continue
			}
			d.Unresolved = append(d.Unresolved, f)
		default:
			d.Unresolved = append(d.Unresolved, f)
		}
	}
	return d
}

// renderCoverageGap is the one rendering owner of a coverage gap: the routing,
// its condition and its identity are all built from the SAME disposition, so a
// narrowed gap names exactly its remaining members and no settled member is
// still described as lacking authority. Nothing edits a rendered condition
// afterwards; a re-evaluated gap is re-rendered here.
//
// A region gap's condition quotes the region's evidence, which names no file.
// Once ANY member has been settled -- by prospective authority or by a
// derivation alike (ruling 176) -- that evidence no longer describes the gap
// alone, so the condition names the members that remain. Which mechanism
// settled a member never decides whether the text is truthful. The text is a
// function of the disposition and the episode's payload alone, so every route
// that reaches one disposition of one episode renders one condition. The
// payload is carried on the gap it renders: a gap's question travels with it.
func renderCoverageGap(d GapDisposition, sem CoverageSemantics) Routing {
	unresolved := append([]string(nil), d.Unresolved...)
	gap := canonicalQuestion(GapIdentity{Kind: d.Kind, Scope: unresolved, Semantics: &sem})
	files := strings.Join(unresolved, ", ")
	narrowed := ""
	if d.Narrowed() {
		narrowed = "; unresolved planned file(s): " + files
	}
	switch d.Kind {
	case gapCoverageUnexamined:
		return Routing{Route: RouteCloseGap, Basis: BasisLacksKnowledge,
			Condition: "graph coverage is absent for planned file(s) the graph has not examined: " + files,
			Gap:       gap}
	case gapCoverageAbsent:
		return Routing{Route: RouteCloseGap, Basis: BasisLacksKnowledge,
			Condition: "graph coverage is absent for the planned files: " + sem.Diagnostic + narrowed,
			Gap:       gap}
	default:
		return Routing{Route: RouteCloseGap,
			Condition: "Sensei reported missing coverage in the planned region: " + strings.Join(sem.Spots, ", ") + narrowed,
			Gap:       gap}
	}
}

// remedyForCoverage is remedyForGap for a gap whose members are read against
// the pinned world, in its three states. A planned create the world confirms
// absent cannot be examined and no graph refresh can cover it, so it is never
// told to: its route is a declared prospective surface whose canonical grant is
// recorded for the plan attempt. A member the world confirms present keeps the
// graph-examination remedy. A member whose presence was never established is
// given neither -- either would assume a fact nobody read -- and is told to
// establish it first (presenceRemedy), even when no member's presence was read.
func remedyForCoverage(gap GapIdentity, action Action, root, domain string) string {
	if !isCoverageGapKind(gap.Kind) {
		return remedyForGap(gap, root, domain)
	}
	set := func(files []string) map[string]bool {
		out := make(map[string]bool, len(files))
		for _, f := range files {
			out[path.Clean(strings.TrimSpace(f))] = true
		}
		return out
	}
	present, absent := set(action.Present), set(action.Absent)
	var creates, examinable, unknown []string
	for _, f := range gap.Scope {
		switch c := path.Clean(strings.TrimSpace(f)); {
		case absent[c]:
			creates = append(creates, f)
		case present[c]:
			examinable = append(examinable, f)
		default:
			unknown = append(unknown, f)
		}
	}
	var parts []string
	if len(creates) != 0 {
		parts = append(parts, prospectiveSurfaceRemedy(creates))
	}
	if len(examinable) != 0 {
		parts = append(parts, knowledgeLimitRemedy(root, domain, examinable))
	}
	if len(unknown) != 0 {
		parts = append(parts, presenceRemedy(unknown))
	}
	return strings.Join(parts, "\n")
}

// presenceRemedy is the closure route of a member whose presence at the pinned
// world is unknown: fail closed, and establish the fact before choosing a route.
func presenceRemedy(files []string) string {
	return "pinned-world presence is unknown for planned file(s): " + strings.Join(files, ", ") +
		"; neither a prospective-surface grant nor a graph refresh can be prescribed until the pinned world's tree has " +
		"been read and confirms whether each exists. The gap stays open: re-run routing once the pinned base is readable, " +
		"then close each file by the route its confirmed presence admits, or remove it from the plan."
}

// prospectiveSurfaceRemedy is the closure route of a planned create absent at
// the pinned world that holds no valid recorded prospective grant.
func prospectiveSurfaceRemedy(creates []string) string {
	return "prospective authority for planned create(s) absent at the pinned world: " + strings.Join(creates, ", ") +
		"; a file that does not exist has no coverage to examine, so close it through the prospective-surface route: " +
		"declare each one in prospective_surfaces (path, package, role, covering) so a canonical prospective grant is " +
		"derived and recorded for this plan attempt, or remove it from the plan."
}
