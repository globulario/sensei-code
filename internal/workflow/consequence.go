package workflow

// Consequence assessment.
//
// A consequence signal is not a verdict. `anchor with severity=critical`, a
// high-risk path and a security namespace all say the same thing: LOOK HARDER
// HERE. None of them says who must decide.
//
// The measurement that forced this: 22 of the 26 files Sensei covers carry
// APPROVAL_GATE_NONE from the risk channel and a consequence blind spot at the
// same time, and ten recorded authority receipts in this repository all name
// the identical pair. Routing the signal straight to a human made the file the
// authority unit, and the file cannot answer the question, because the same
// change carries different consequences depending on what is being DONE with
// it:
//
//	edit internal/event/bus.go in a candidate worktree, run tests
//	  → nothing outside the worktree changes; discard it and the world is unchanged
//
//	merge it, build a release, publish the image, deploy to a cluster
//	  → materially different, and no property of bus.go distinguishes the two
//
// So the unit is the ACTION. This assesses one, and answers only the question
// it can: are the consequences of THIS action bounded.
//
// # The asymmetry that keeps it honest
//
// An assessment draws on two sources and they are not equal.
//
// The STAGE is structural. The engine knows the architect stage runs in an
// isolated candidate worktree whose diff is audited before anything leaves it,
// because that is what the workflow does — not because a provider said so.
//
// Everything the plan DECLARES is a claim. A declared outward effect can only
// make an assessment worse; the absence of one clears nothing. An agent that
// says "no side effects" has supplied no evidence, and an assessment that
// accepted silence as safety would be the escape hatch this whole design
// refuses, one level up from the retrieval silence it already refuses.

import (
	"path"
	"sort"
	"strings"
)

// ActionStage is what the authority decision actually covers.
//
// Deliberately not "the change" or "the file". Authority granted over an edit
// in a disposable worktree is not authority to merge it, and not authority to
// publish it — those are later actions with their own consequences, and each
// gets its own assessment.
type ActionStage string

const (
	// StageCandidateEdit: edit files inside an isolated candidate worktree and
	// run the repository's own tests. The worktree is discarded on refusal, and
	// the diff is audited before it can go anywhere.
	StageCandidateEdit ActionStage = "candidate-edit"
	// StageObserve: read the repository and report what was found. No file is
	// written, no worktree is created, nothing is admitted.
	//
	// Structural like every other stage: set by the entrypoint the task came
	// through, never from a plan that says it will only read. A plan claiming
	// read-only is a claim, and claims escalate rather than clear.
	StageObserve ActionStage = "observe"
	// StagePublish: anything that leaves the worktree — merge, release, deploy,
	// or mutating state something else can observe.
	StagePublish ActionStage = "publish"
	// StageUnknown is a stage nobody has classified. It is the zero value, so
	// an unset stage cannot be assessed as bounded by accident.
	StageUnknown ActionStage = ""
)

// Action is the proposed operation whose consequences are being assessed.
type Action struct {
	// Stage is set by the engine from what the workflow is actually doing.
	// A provider cannot supply it.
	Stage ActionStage
	Files []string
	// DeclaredSteps and DeclaredConsequences are the plan's own account of what
	// it will do. Claims: they may escalate an assessment, never clear one.
	DeclaredSteps        []string
	DeclaredConsequences string
	// OperationalAuthority are planned files a bounded operational grant
	// authorizes -- today, existing test files beside a covered subject
	// (M2.2). They are subtracted from the ARCHITECTURAL coverage question
	// and never enter DerivedCoverage: an edited test earns permission to be
	// edited, not architectural coverage, and the two are kept apart by type.
	OperationalAuthority []string
	// DerivedCoverage are planned files a machine-derived fact covers in THIS
	// world, established by re-running a derivation rather than by a record
	// existing, each carrying what that derivation is able to ANSWER.
	//
	// It was a bare []string of paths. That list could say a file was covered
	// and could not say covered for WHAT, so subject overlap alone closed a
	// gap: `P is DERIVED` was read as `P resolves gap G`, and any cheap wide
	// truth over the right files manufactured coverage honestly.
	//
	// Computed before routing, because revalidation reads the repository and
	// this package is pure. The caller must obtain it from
	// derived.CoveredFiles over anchors produced in the world being assessed —
	// a list assembled any other way would be the forbidden collapse (recipe
	// present -> coverage) wearing a different type. The Requirement on each
	// entry is computed by the consumer from the anchor's family and is never
	// read off the wire.
	DerivedCoverage []CoverageAnchor
	// DocumentEvidence is, per planned DOCUMENT, the identities of the governed
	// invariants that protect it, as observed PER FILE at plan time.
	//
	// Engine-owned and per-file on purpose. The region preflight's DirectInvariants is
	// one answer for a whole region, and reading it per file is the laundering defect a
	// per-file fact already had to fix once for coverage.
	//
	// The three states are distinct and the distinction is the whole point:
	//
	//	entry present, non-empty  the graph looked and found protection -> architecture
	//	entry present, empty      the graph looked and found none -> ordinary documentation
	//	NO entry                  the graph never looked -> knowledge limit, not "ordinary"
	DocumentEvidence map[string][]string
	// PlannedCreates are the exact planned paths this task DECLARED it will
	// create and that were positively proven ABSENT at the task's pinned base.
	//
	// Engine-owned, exact-path, and derived only from the plan's own CREATE
	// disposition plus a read of the pinned base (see plannedCreates). It is
	// NOT coverage and never becomes any: nothing here enters DerivedCoverage
	// or DocumentEvidence, a create is never covered by its neighbour or its
	// directory, and the only questions it answers are "has the graph examined
	// this file" and "what invariant protects this document" -- both of which
	// demand a pre-existence identity the premise of the task says is absent.
	// Every other planned path, including the existing files a create's
	// CONTENTS are decided from, keeps every requirement it had.
	PlannedCreates []string
	// Unexamined are planned files the graph has no facts about at plan time:
	// a per-file preflight found no anchor and no indexed file for each.
	//
	// Engine-owned, established per file, never read off the scoped answer.
	// The scoped preflight is one answer for the whole region, and its
	// coverage is proven the moment ONE planned file carries anchors --
	// live, over [engine.go, a file that does not exist]: sufficient=true,
	// direct_anchor_count=3, file_count=2, indexed_file_count=1. Read at the
	// region, the second file inherited the first one's coverage, and a plan
	// could carry any ungrounded file into an anchored region and launder the
	// region's authority onto it (M25 §1). A file under an operational grant
	// is not asked to be examined and is ignored here by the router.
	Unexamined []string
}

// unexaminedArchitecturalFiles are the planned files the graph has not
// examined and no operational grant covers: the ones whose coverage the
// scoped answer cannot vouch for. Canonicalised like architecturalFiles, and
// in plan order, so the gap identity built from them is stable.
func (a Action) unexaminedArchitecturalFiles() []string {
	if len(a.Unexamined) == 0 {
		return nil
	}
	unexamined := map[string]bool{}
	for _, f := range a.Unexamined {
		unexamined[path.Clean(strings.TrimSpace(f))] = true
	}
	// A DECLARED FUTURE ARTIFACT IS NOT AN UNEXAMINED EXISTING ONE. The graph
	// has no facts about a path that does not exist at the pinned base, and it
	// cannot acquire any: `sensei import --refresh` examines a repository, and
	// the repository does not contain this file yet. Asking for its examination
	// is asking for the premise of the task to be false, so exactly that one
	// path is subtracted -- and nothing else is.
	planned := a.plannedCreateSet()
	var out []string
	for _, f := range a.architecturalFiles() {
		if unexamined[f] && !planned[f] {
			out = append(out, f)
		}
	}
	return out
}

// plannedCreateSet is PlannedCreates as an exact-path set, canonicalised the
// way every other path in this file is. Exact paths only: no prefix, no
// directory and no pattern, because a create's exemption must not reach one
// character past the path the task bound.
func (a Action) plannedCreateSet() map[string]bool {
	out := map[string]bool{}
	for _, f := range a.PlannedCreates {
		if c := path.Clean(strings.TrimSpace(f)); c != "." {
			out[c] = true
		}
	}
	return out
}

// architecturalFiles are the planned files the coverage question is about:
// every planned file not under an operational grant.
// --- TYPED EVIDENCE: what KIND of proof an artifact can actually give -------------
//
// The planner sees one list of paths. Asking every path for the same kind of proof is
// what produced W3's false gap: cmd/sensei-code/control_test.go was reported as
// production source the graph had not examined, and no graph state could ever have
// changed that, because no derivation family reads test files at all.
//
// So classification comes first, and it uses the repository's existing semantics rather
// than a new taxonomy: a *_test.go IS a test artifact under Go's own rule and under
// testEditGrants, which already governs exactly those files.
type artifactClass string

const (
	// classProductionGo answers to mechanical/source evidence: a derived anchor whose
	// subjects include the file.
	classProductionGo artifactClass = "production-go"
	// classTestGo answers to test-governance evidence: the existing
	// EXISTING_TEST_EDIT_ADMISSIBLE grant, which binds the test to a covered production
	// file in its own directory and package.
	classTestGo artifactClass = "test-go"
	// classDocument answers to ARCHITECTURE evidence: a governed invariant that names the
	// file. The repository already decides this — docs/awareness/invariants.yaml protects
	// a markdown file through protects.files, and high_risk_files.yaml states Sensei
	// automatically protects "files a governed invariant or failure mode directly names".
	// So authority comes from governed knowledge, never from the document's own text.
	classDocument artifactClass = "document"
	// classUnsupported is everything no evidence class covers. It fails closed: an
	// artifact nobody can prove anything about is a knowledge limit, not a pass.
	classUnsupported artifactClass = "unsupported"
)

// classifyArtifact types one planned path.
//
// The suffix rule is Go's own and is what testEditGrants already uses, so this adds no
// second definition of "test file". Deliberately NOT a guess about intent: a file named
// test_helpers.go or testdata_helper.go is production source, because the toolchain
// compiles it into the package and every derivation family reads it.
func classifyArtifact(file string) artifactClass {
	c := path.Clean(strings.TrimSpace(file))
	switch {
	case strings.HasSuffix(c, "_test.go"):
		return classTestGo
	case strings.HasSuffix(c, ".go"):
		return classProductionGo
	case strings.HasSuffix(c, ".md"):
		return classDocument
	default:
		// NOT a silent pass. A Makefile, a shell script or an image is an artifact no
		// evidence class here can prove anything about, and saying so is the honest
		// answer; the previous default (production Go) would have asked a derivation to
		// read a PNG.
		return classUnsupported
	}
}

// testArtifacts are the planned test files that did NOT earn a grant.
//
// The grant predicate's contract says an unestablished case "leaves F ungranted, silently
// to routing". This is where that silence ends: an ungranted test is still governed, by
// its own evidence class, and it never joins the production-coverage question.
func (a Action) ungrantedTestArtifacts() []string {
	granted := map[string]bool{}
	for _, f := range a.OperationalAuthority {
		granted[path.Clean(strings.TrimSpace(f))] = true
	}
	// A TEST ARTIFACT HAS TWO WAYS TO BE GOVERNED, and requiring the grant alone was
	// wrong. Two existing invariant tests proved it:
	//
	//   - a replay of real preflight rows contains cmd/sensei-code/version_test.go with
	//     coverage sufficient and anchors present: the graph HAS facts about that test,
	//     because authored knowledge (a required_test entry) names it. That is test
	//     governance of a different kind, and demanding a grant as well would report a
	//     governed file as ungoverned;
	//   - Action.Unexamined is the engine-owned per-file fact for exactly this: "the
	//     graph has no facts about it at plan time".
	//
	// So the gap is for a test that is neither granted NOR examined. Anything the graph
	// already knows about keeps the governance it has.
	unexamined := map[string]bool{}
	for _, f := range a.Unexamined {
		unexamined[path.Clean(strings.TrimSpace(f))] = true
	}
	var out []string
	for _, f := range a.Files {
		c := path.Clean(strings.TrimSpace(f))
		if classifyArtifact(c) == classTestGo && !granted[c] && unexamined[c] {
			out = append(out, c)
		}
	}
	return out
}

func (a Action) architecturalFiles() []string {
	// Grants are recorded canonical (path.Clean of the trimmed plan
	// spelling); the plan's own spelling is whatever the architect wrote,
	// "./pkg/x_test.go" included. Comparing the two raw would leave a
	// validly granted test in the architectural set and ask the derivations
	// to cover it -- the same file, two representations, one of them
	// handled. Both sides are canonicalised here, and the scope returned is
	// the canonical one, so the gap identity built from it is stable too.
	skip := map[string]bool{}
	for _, f := range a.OperationalAuthority {
		skip[path.Clean(strings.TrimSpace(f))] = true
	}
	var out []string
	for _, f := range a.Files {
		c := path.Clean(strings.TrimSpace(f))
		if skip[c] {
			continue
		}
		// ONLY PRODUCTION GO ANSWERS TO SOURCE COVERAGE. A test artifact never joins the
		// production-coverage question, granted or not.
		// Subtracting only the GRANTED ones left an ungranted test here, where the graph
		// was then asked for source coverage of a file no derivation family reads — the
		// false gap W3 hit. Ungranted tests are routed to their own evidence class by
		// ungrantedTestArtifacts.
		if classifyArtifact(c) != classProductionGo {
			continue
		}
		out = append(out, c)
	}
	return out
}

// ConsequenceResult is the answer, and there are only three.
type ConsequenceResult string

const (
	// ConsequenceBounded: the effects are confined to something disposable, and
	// what confines them is named in Boundary.
	ConsequenceBounded ConsequenceResult = "BOUNDED"
	// ConsequenceUnacceptable: the action reaches outside anything this
	// assessment can undo.
	ConsequenceUnacceptable ConsequenceResult = "UNACCEPTABLE"
	// ConsequenceCannotEstablish: the question was asked and could not be
	// answered. Distinct from UNACCEPTABLE on purpose — "this is dangerous" and
	// "nobody knows" are different findings, and collapsing them would report
	// ignorance as a risk verdict.
	ConsequenceCannotEstablish ConsequenceResult = "CANNOT_ESTABLISH"
)

// ConsequenceAssessment is the result plus what it rests on.
type ConsequenceAssessment struct {
	Result ConsequenceResult
	// Boundary names what actually confines the effects. An assessment that
	// says BOUNDED without naming the boundary is an opinion.
	Boundary string
	// Effects are outward effects found. Present on a bounded assessment too:
	// a later stage over the same files needs to know what they were.
	Effects []string
	// Evidence is what the assessment read.
	Evidence []string
}

// outwardVerbs are declared steps that reach past a worktree.
//
// This list is a claim-reader, not a safety net. It catches a plan that SAYS it
// will publish; it cannot catch one that publishes without saying so. What
// stops that is the stage boundary, which is structural.
//
// Only tokens that mean an outward action wherever they appear in English
// belong here. A token that ALSO names ordinary code must not: the first
// governed run against a foreign repository was refused as "declares an outward
// action: release" because the plan said "acquisition and release logic remains
// unchanged" -- semaphore.Weighted.Release, a method. Same class as "403" inside
// a commit hash and "backend is unreachable" in prose: a closed vocabulary read
// by substring against free text, and it failed in the direction of stopping
// honest work before any knowledge-gap routing could run.
var outwardVerbs = []string{
	"deploy", "publish", "push to", "git push",
	"drop table", "truncate",
	"send email", "post to", "curl -x post",
	"terraform apply", "kubectl apply", "helm install",
	"docker push", "npm publish",
}

// outwardPhrases are outward actions whose key word is ambiguous on its own.
//
// "release" is Weighted.Release and "cut a release"; "notify" is notifyWaiters
// and "notify the team"; "migrate" is a struct field and a schema change;
// "production" is an environment and an adjective. Each is read only in a
// verb-object shape that means the outward thing, and ambiguous prose that
// matches none of these does NOT silently become an outward action -- it is
// left to the stage boundary, which is where an undeclared publish is stopped
// anyway.
var outwardPhrases = []string{
	"cut a release", "create a release", "tag a release", "ship a release",
	"publish a release", "publish release", "publish the release", "release to ",
	"push a release", "release v",
	"to production", "in production", "production deploy", "production environment",
	"send a notification", "notify the team", "notify users", "notify customers",
	"notify subscribers",
	"upload to ", "upload the artifact", "upload artifacts",
	"run the migration", "apply the migration", "run migrations", "apply migrations",
	"migrate the database", "database migration", "schema migration",
}

// --- WHAT THE PLAN ASSERTS, NOT WHICH WORDS IT CONTAINS ---------------------
//
// A closed vocabulary answers "is this word here". The question the assessment
// actually asks is "does this plan say it will DO this", and the two came apart
// the moment a plan wrote down what it would NOT do.
//
// Live, 2026-09-20, task-1789870806342069862:
//
//	publication may open a branch and pull request only, never merge or push to main
//	  -> UNACCEPTABLE: "the plan declares an outward action: push to"
//
// and the task stopped at a human boundary that its own plan had promised never
// to reach. Run directly, "the run must never deploy anything; it only edits
// files in the candidate worktree" refuses the same way, so it is not one verb
// and not one wording.
//
// The inversion is the harm. The more carefully an objective states its own
// limits, the more certainly it escalates -- and a person is then asked to
// authorize a boundary nobody proposed crossing. A guard that punishes the
// safest way to write a plan teaches plans to stop saying what they will not do.
//
// So the vocabulary stays exactly as it is, and what changes is how each
// occurrence is READ.
//
// # Suppression requires positive evidence
//
// The first attempt at this carried a negation FORWARD until something
// recognisable stopped it. That default is the wrong way round. Every
// construction nobody had thought of yet fell toward suppression, so plain
// declarations -- "with no additional review push to main", "no approval gate
// remains for the push to main", "there is no support for a push to main" --
// were read as bounded, and the authority router never got to interrupt a run
// that had just said it would publish. Adding stop-conditions cannot repair
// that, because the list of them is precisely what is incomplete.
//
// So the default is inverted. An occurrence is ASSERTED unless one of three
// relations can be POSITIVELY shown to hold between a negation and that
// occurrence. Anything unrecognised is asserted, which fails toward the person
// rather than past them.
//
//	NEGATED PREDICATE   a negator, or a verb that negates its own complement,
//	                    stands before the operation with nothing in between but
//	                    the material of one predicate: auxiliaries, the
//	                    infinitive "to", determiners, adverbials, a permission
//	                    verb and its object, or a disjunct the same negation
//	                    governs.
//	                      "never merge or push to main"
//	                      "never allow it to deploy"
//	                      "refuses to push to main"
//	NEGATIVE SUBJECT    the clause opens with "no"/"none"/"neither" and an
//	                    auxiliary marks the predicate that subject heads.
//	                      "no part of this run will push to main"
//	POSTPOSED           the operation stands as the clause's own subject and the
//	                    predicate made of it is negated.
//	                      "push to main is never allowed"
//
// Parity, never presence: "the run cannot avoid a deploy to production" carries
// two negations that reach the deploy, and therefore asserts it. Reading a
// negator's presence is the third recorded forbidden fix.
//
// # Quotation is not suppression
//
// An earlier version read a quotation introduced by a citing word as a MENTION
// and blanked it. It did not survive its own counterexample: in
//
//	the runbook says "push to main" and the run does exactly that
//
// the operation is quoted and then performed, and blanking the only hazardous
// token in the text made the performance invisible. Nothing about being quoted
// is evidence that an action will not be taken, and the blanking had unbounded
// reach besides -- one unbalanced delimiter erased every declaration after it.
//
// So quotation marks are punctuation here and nothing else. A quoted
// PROHIBITION is still bounded, because the prohibition inside it is read
// exactly as it would be anywhere else: `the objective says "never push to
// main"` is bounded by its "never", not by its quotation marks. The price is
// that a bare cited operation carrying no negation of its own escalates -- what
// it did before any of this existed, and the direction to be wrong in.
//
// # Forbidden repairs
//
// Three are recorded in the graph, because each would make the symptom
// disappear while leaving the defect:
//
//  1. rephrasing the plan's safety constraint so the matcher stops seeing it.
//     The constraint is the thing being protected.
//  2. whitelisting an objective file, a path or a caller. That fixes the inputs
//     somebody has met and nothing else -- and an exempted caller loses the real
//     escalation too.
//  3. ignoring any sentence that contains "never" or "not". That is this same
//     lexical hack with the sign flipped, and it eventually swallows a real
//     declaration such as "the run cannot avoid a deploy to production".
//
// This is a claim-reader, as it always was. It reads the plan's own account
// more accurately; it does not become a safety net, and an UNDECLARED publish
// is stopped by the structural stage boundary rather than by any of this.

// negators deny the predicate they attach to.
//
// Counted by parity, never by presence: "the run cannot avoid a deploy to
// production" carries two and asserts the deploy.
//
// " nor " is deliberately absent. Negative concord spells ONE prohibition with
// two words -- "neither merge nor push to main" -- so counting it as a second
// negation cancelled the first and escalated the most careful phrasing of the
// sentence that caused this defect. It is a disjunction here.
var negators = []string{"never", "not", "cannot", "no", "none", "neither"}

// subjectNegators are the quantifiers that can head a negated SUBJECT. The
// adverbial negators are not among them: "never part of this run" is not a
// sentence anybody writes.
var subjectNegators = []string{"no", "none", "neither"}

// negatorStems are verbs that negate their own complement, in any inflection:
// "refuses to push to main", "forbidden to deploy", "prevented from publishing".
var negatorStems = []string{"refus", "reject", "forbid", "prohibit", "prevent", "avoid", "declin"}

// verbEndings are the inflections a negating verb stem may carry.
//
// Bare prefix matching read "preventive", "prevention", "avoidance", "refusal"
// and "rejected" as negations, and one spurious negator flips parity -- the
// fail-open direction. A stem plus a VERBAL ending is a verb; a stem plus
// "-ive", "-ion", "-ance" or "-al" is the noun or adjective beside it.
var verbEndings = []string{"", "e", "s", "es", "ed", "en", "den", "ing"}

// auxiliaries and modals mark where a predicate begins, and they are usable
// here for one reason: in English they are a closed class. The alternative is
// guessing at lexical verbs, which is open-ended.
var auxiliaries = []string{
	"will", "shall", "would", "should", "must", "may", "might", "can", "could",
	"do", "does", "did", "is", "are", "was", "were", "be", "been", "being",
	"has", "have", "had",
}

// permissionVerbs negate their complement when they are themselves negated:
// "never allow it to deploy" is a prohibition on deploying, not on allowing.
var permissionVerbs = []string{
	"allow", "allows", "allowed", "permit", "permits", "permitted",
	"let", "lets", "authorize", "authorizes", "authorized",
	"authorise", "authorises", "authorised", "enable", "enables", "enabled",
}

// pronouns are subject pronouns: a closed class, and the evidence that a new
// clause has started rather than a coordinated phrase continuing.
var pronouns = []string{"we", "i", "you", "he", "she", "it", "they", "there"}

// determiners may stand between a negation and the operation it negates
// without ending the predicate: "forbids a push to main".
var determiners = []string{
	"a", "an", "the", "this", "that", "these", "those", "its", "their", "our", "any",
}

// adverbials are the modifiers a prohibition puts between its negator and its
// verb: "we must not at any point push to main".
//
// Short and closed on purpose. A word that is not listed ENDS the walk, which
// asserts the operation, so the cost of an omission here is a false escalation
// and never a false grant.
var adverbials = []string{
	"ever", "again", "at", "any", "all", "in", "under",
	"point", "time", "times", "circumstances", "practice", "case", "cases",
	"further", "directly", "immediately", "actually", "ultimately",
	"explicitly", "silently", "automatically",
}

// disjunctions distribute a negation over both of their elements: "never merge
// OR push to main" forbids the push as well.
var disjunctions = []string{"or", "nor"}

// conjunctions do NOT distribute it. "we will not stop AND push to main" is two
// statements, and the one that matters is asserted. That asymmetry is
// De Morgan's, not a preference.
var conjunctions = []string{"and", "plus", "but"}

// clauseBreaks are where polarity resets.
//
// Punctuation only. Subordinating WORDS used to be listed here and no longer
// need to be: an unrecognised word ends the walk by itself, so "do not stop
// before deploy to production" asserts the deploy because "before" and "stop"
// are not predicate material -- not because " before " was written down.
//
// A comma DOES break, which fails toward escalation: "we never merge, and we
// will push to main" asserts the push.
var clauseBreaks = []string{
	".", ";", ":", "!", "?", "\n", ",",
	" - ", "—", "–", "(", ")", "[", "]",
}

// word is one word-byte run of a clause, with its offsets.
type word struct {
	text  string
	start int
	end   int
}

// clause is one clause read once: its words, and which of those words belong to
// an outward operation. The second is what lets a coordinated operation stand
// between a negator and the one being classified.
type clause struct {
	text  string
	words []word
	inOp  []bool
}

// readClause tokenises a clause and marks the words every outward operation in
// it covers.
func readClause(text string) clause {
	c := clause{text: text}
	for i := 0; i < len(text); {
		if !isWordByte(text[i]) {
			i++
			continue
		}
		j := i
		for j < len(text) && isWordByte(text[j]) {
			j++
		}
		c.words = append(c.words, word{text: text[i:j], start: i, end: j})
		i = j
	}
	c.inOp = make([]bool, len(c.words))
	mark := func(token string, atWordBoundary bool) {
		for _, at := range occurrencesOf(text, token, atWordBoundary) {
			first, last := c.span(at, at+len(token))
			for i := first; i >= 0 && i <= last; i++ {
				c.inOp[i] = true
			}
		}
	}
	for _, verb := range outwardVerbs {
		mark(verb, true)
	}
	for _, phrase := range outwardPhrases {
		mark(phrase, false)
	}
	return c
}

// span returns the first and last word indices the byte range [at,end) covers,
// or (-1,-1) if it covers none.
func (c clause) span(at, end int) (int, int) {
	first, last := -1, -1
	for i, w := range c.words {
		if w.end > at && w.start < end {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	return first, last
}

// asserted reports whether the operation covering [at,end) is one this clause
// declares, rather than one a negation in it denies.
//
// The default is ASSERTED. Each of the three suppressing relations must be
// positively established against THIS occurrence; none of them is a state the
// clause carries around.
func (c clause) asserted(at, end int) bool {
	first, last := c.span(at, end)
	if first < 0 {
		return true
	}
	return !c.negatedPredicate(first) && !c.negativeSubject(first) && !c.frontedNegativeAdverbial(first) && !c.postposed(first, last) && !c.mentioned(at, end)
}

// mentioned reports a quoted operation that the clause explicitly characterizes
// as forbidden. The quotation alone is not enough: a plan can quote the command
// it is about to execute. The forbidden characterization is the evidence that
// this occurrence describes a prohibited action rather than asserts one.
func (c clause) mentioned(at, end int) bool {
	open := strings.LastIndex(c.text[:at], "\"")
	if open < 0 || strings.Count(c.text[:at], "\"")%2 == 0 {
		return false
	}
	close := strings.Index(c.text[end:], "\"")
	if close < 0 {
		return false
	}
	context := strings.TrimSpace(c.text[end+close+1:])
	return strings.HasPrefix(context, "as forbidden") ||
		strings.HasPrefix(context, "as prohibited") ||
		strings.HasPrefix(context, "is forbidden") ||
		strings.HasPrefix(context, "is prohibited")
}

// negatedPredicate walks left from the operation and reports whether it lands
// on a negation with nothing but one predicate's own material in between.
//
// Every step has to be recognised. An unrecognised word is a new predicate as
// far as this can tell, and the walk stops there: "no reviewer is bypassed and
// the run will push to main" stops at "run" and declares the push, and "with no
// additional review push to main" stops at "review" and declares it too.
func (c clause) negatedPredicate(k int) bool {
	for i := k - 1; i >= 0; i-- {
		w := c.words[i].text
		switch {
		case listed(negators, w) && !c.compound(i):
			return !c.cancelled(i)
		case negatingVerb(w):
			return !c.cancelled(i)
		case listed(disjunctions, w):
			// "never merge or push to main": one negation over two elements.
			n := c.negatorBeforeDisjunct(i)
			return n >= 0 && !c.cancelled(n)
		case c.predicateMaterial(i):
			continue
		case i > 0 && listed(permissionVerbs, c.words[i-1].text):
			continue // the object of a control verb: "never allow IT to deploy"
		default:
			return false
		}
	}
	return false
}

// predicateMaterial reports whether a word can stand inside the predicate a
// negation governs without ending it.
func (c clause) predicateMaterial(i int) bool {
	w := c.words[i].text
	return c.inOp[i] || w == "to" ||
		listed(auxiliaries, w) || listed(determiners, w) ||
		listed(adverbials, w) || listed(permissionVerbs, w)
}

// negatorBeforeDisjunct resolves the first element of a negated coordination:
// in "never merge or push to main" the negator governs the second disjunct too,
// even though "merge" is not in the outward vocabulary and so is not otherwise
// recognisable.
//
// Bounded to a SHORT BARE element -- no auxiliary, no subject pronoun, no
// second coordination, four words at most -- because that is what distinguishes
// a coordinated verb phrase from a new clause that happens to begin with "or".
func (c clause) negatorBeforeDisjunct(d int) int {
	for i := d - 1; i >= 0 && d-i <= 5; i-- {
		w := c.words[i].text
		if listed(negators, w) && !c.compound(i) {
			return i
		}
		if listed(auxiliaries, w) || listed(pronouns, w) ||
			listed(disjunctions, w) || listed(conjunctions, w) {
			return -1
		}
	}
	return -1
}

// negativeSubject reports a clause opening with a negative quantifier whose
// subject heads the predicate the operation sits in: "no part of this run will
// push to main", "no step in the plan will push to main".
//
// The AUXILIARY is the positive evidence, and it is what separates this from
// two statements run together. "no exceptions deploy to production at the end"
// and "no manual approval required push to main automatically" carry none, and
// assert their operation. A coordination ends it as well: "no rollback is
// possible and the run will push to main" declares the push.
func (c clause) negativeSubject(k int) bool {
	if len(c.words) == 0 || !listed(subjectNegators, c.words[0].text) || c.compound(0) {
		return false
	}
	aux := -1
	for i := 1; i < k; i++ {
		w := c.words[i].text
		if listed(disjunctions, w) || listed(conjunctions, w) || listed(negators, w) {
			return false
		}
		if aux < 0 {
			if listed(auxiliaries, w) {
				aux = i
			}
			continue
		}
		// Past the auxiliary only the predicate's own material may stand;
		// anything else is a second predicate, as in "no reviewer is bypassed
		// the run will push to main".
		if !c.predicateMaterial(i) {
			return false
		}
	}
	return aux >= 0
}

// frontedNegativeAdverbial reports inverted prohibitions such as "under no
// circumstances should the plan push to main". The opening negative adverbial
// governs the predicate introduced by the following auxiliary, not every
// later word in the clause.
func (c clause) frontedNegativeAdverbial(k int) bool {
	if len(c.words) < 4 || c.words[0].text != "under" ||
		c.words[1].text != "no" || c.words[2].text != "circumstances" {
		return false
	}
	// The auxiliary does not end the scan. Coordination does.
	//
	// Returning at the first auxiliary made every later conjunction
	// unreachable, so "under no circumstances should the plan merge AND the
	// run will push to main" suppressed an asserted publish: the fronted
	// prohibition was read as governing a clause it had already handed over.
	// A prohibition stops governing where a new predicate is coordinated onto
	// it, wherever its own auxiliary happened to fall.
	// Coordination alone does not end the prohibition: what ends it is a NEW
	// PREDICATE. "merge or push to main" coordinates two bare verbs under one
	// subject and one auxiliary, so the prohibition reaches both. "merge and
	// the run will push to main" coordinates a fresh subject and a fresh
	// auxiliary onto it, and the prohibition stops at that hand-over.
	//
	// Returning at the first auxiliary made the question unaskable: every
	// later coordination was unreachable, so an asserted publish in a second
	// predicate was suppressed. Returning at the first coordination answers it
	// the other way and suppresses nothing, but re-escalates the bare-verb
	// prohibition that this shape exists to read.
	sawAuxiliary := false
	for i := 3; i < k; i++ {
		if listed(negators, c.words[i].text) && !c.compound(i) {
			return false
		}
		if listed(conjunctions, c.words[i].text) || listed(disjunctions, c.words[i].text) {
			if c.newPredicateBefore(i+1, k) {
				return false
			}
			continue
		}
		if listed(auxiliaries, c.words[i].text) {
			sawAuxiliary = true
		}
	}
	return sawAuxiliary
}

// newPredicateBefore reports a new SUBJECT taking a predicate in [from,to).
//
// An auxiliary alone is not the boundary. "merge or BE ALLOWED TO push to
// main" coordinates a second predicate under the same subject and the same
// modal, and the prohibition still reaches it; reading "be" as a fresh
// predicate re-escalated an operation nobody proposed. What a coordinated
// CLAUSE brings that a coordinated predicate does not is a subject of its own:
// "merge and THE RUN will push to main" hands the sentence to someone else,
// and the prohibition stops there.
func (c clause) newPredicateBefore(from, to int) bool {
	subject := false
	for i := from; i < to; i++ {
		w := c.words[i].text
		if subject && listed(auxiliaries, w) {
			return true
		}
		if c.inOp[i] {
			continue
		}
		// A subject is not a word this classifier knows. Naming the ways one
		// can be spelled -- determiners, then pronouns -- left every bare noun
		// out: "and RUNNER will push to main" handed the sentence over and was
		// not seen to, so the prohibition kept governing an asserted publish.
		// The closed vocabularies are the function words; what is left over is
		// open class, and an open-class word taking an auxiliary is a subject
		// taking a predicate. This asks what a word is NOT, so it cannot be
		// outrun by a noun nobody listed.
		if listed(determiners, w) || listed(pronouns, w) || !c.functionWord(i) {
			subject = true
		}
	}
	return false
}

// functionWord reports a word drawn from one of this classifier's closed
// vocabularies. Everything else is open class -- a noun, a name, a verb nobody
// enumerated -- which is exactly what cannot be listed in advance.
func (c clause) functionWord(i int) bool {
	w := c.words[i].text
	return w == "to" ||
		listed(auxiliaries, w) || listed(determiners, w) ||
		listed(adverbials, w) || listed(permissionVerbs, w) ||
		listed(negators, w) || listed(pronouns, w) ||
		listed(conjunctions, w) || listed(disjunctions, w) ||
		negatingVerb(w)
}

// postposed reports the operation standing as the clause's own SUBJECT with the
// predicate made of it negated: "push to main is never allowed".
//
// Positive evidence in both directions. Only the operation's own material may
// precede it, so `the objective says "push to main" is refused` is not this
// shape and escalates. And the auxiliary must follow across at most the
// operation's own object, with no coordination and no second negation, so
// "push to main and the tag is not signed" is not this shape either.
func (c clause) postposed(first, last int) bool {
	for i := 0; i < first; i++ {
		if !c.inOp[i] && !listed(determiners, c.words[i].text) {
			return false
		}
	}
	aux, gap := -1, 0
	for i := last + 1; i < len(c.words); i++ {
		w := c.words[i].text
		if listed(auxiliaries, w) {
			aux = i
			break
		}
		if c.inOp[i] {
			continue
		}
		if listed(disjunctions, w) || listed(conjunctions, w) ||
			listed(negators, w) || w == "to" || gap >= 2 {
			return false
		}
		gap++
	}
	if aux < 0 {
		return false
	}
	for i := aux + 1; i < len(c.words); i++ {
		w := c.words[i].text
		if listed(negators, w) && !c.compound(i) {
			// The negation must govern a predicate ABOUT the operation, not a
			// later predicate that merely requires it: "push to main must not be
			// skipped" asserts the push.
			for j := i + 1; j < len(c.words); j++ {
				predicate := c.words[j].text
				if listed(auxiliaries, predicate) {
					continue
				}
				// Parity, the same law the rest of the classifier uses. A
				// negator over a NEGATING verb is two negations, and two
				// negations assert: "push to main is not forbidden" permits
				// the push. Reading the second negation as further evidence of
				// prohibition suppressed the assertion it actually makes.
				if negatingVerb(predicate) {
					return false
				}
				return listed(permissionVerbs, predicate)
			}
			return false
		}
		if negatingVerb(w) {
			return true
		}
		if listed(auxiliaries, w) || listed(adverbials, w) {
			continue
		}
		return false
	}
	return false
}

// cancelled reports whether the negation immediately governing the predicate
// at i reverses it: "the run cannot avoid a deploy to production" carries
// "cannot" before "avoid" and therefore ASSERTS the deploy.
//
// Parity is the decision, so a single spurious negator does not blunt the
// answer -- it inverts it. That is why compounds are excluded.
func (c clause) cancelled(i int) bool {
	for j := i - 1; j >= 0; j-- {
		w := c.words[j].text
		if (listed(negators, w) && !c.compound(j)) || negatingVerb(w) {
			return true
		}
		if !c.predicateMaterial(j) {
			return false
		}
	}
	return false
}

// compound reports whether the word at i is joined to a neighbour by a hyphen,
// which makes it part of a compound word rather than a token of its own.
//
// "-" is not a word byte, so without this "no-op" donates a free "no" -- and
// this repository writes "no-op" constantly.
func (c clause) compound(i int) bool {
	w := c.words[i]
	return (w.start > 0 && c.text[w.start-1] == '-') ||
		(w.end < len(c.text) && c.text[w.end] == '-')
}

// negatingVerb reports whether a word is one of the negatorStems in a verbal
// inflection -- "refuses", "forbidden", "prevented" -- and not the noun or
// adjective built on the same stem.
func negatingVerb(w string) bool {
	for _, stem := range negatorStems {
		if !strings.HasPrefix(w, stem) {
			continue
		}
		for _, end := range verbEndings {
			if w[len(stem):] == end {
				return true
			}
		}
	}
	return false
}

// listed reports membership of one of this file's closed vocabularies.
func listed(set []string, w string) bool {
	for _, s := range set {
		if s == w {
			return true
		}
	}
	return false
}

// clausesOf splits text where polarity resets.
func clausesOf(text string) []string {
	for _, b := range clauseBreaks {
		text = strings.ReplaceAll(text, b, "\x00")
	}
	return strings.Split(text, "\x00")
}

// declaredOutwardActions reads a plan's own steps and consequences for an
// outward action it ASSERTS.
//
// Word-anchored for the bare tokens, so "deploy" does not fire inside
// "deployment.go" or "redeployable"; phrase-anchored for the ambiguous ones.
// Each occurrence is then read in its own clause, and it is a declaration
// unless a negation can be shown to govern it.
func declaredOutwardActions(steps []string, consequences string) []string {
	// Read PER STATEMENT, and never joined. A construction in one step has no
	// way to reach an operation in another.
	var clauses []clause
	for _, s := range append(append([]string{}, steps...), consequences) {
		s = strings.ToLower(s)
		// Contractions carry their negator in a form no word split recovers.
		s = strings.ReplaceAll(s, "n't", " not ")
		for _, part := range clausesOf(s) {
			clauses = append(clauses, readClause(part))
		}
	}

	var found []string
	for _, verb := range outwardVerbs {
		if assertedIn(clauses, verb, true) {
			found = append(found, verb)
		}
	}
	for _, phrase := range outwardPhrases {
		if assertedIn(clauses, phrase, false) {
			found = append(found, strings.TrimSpace(phrase))
		}
	}
	return found
}

// assertedIn reports whether the token appears anywhere as an action the plan
// asserts, rather than one it denies.
func assertedIn(clauses []clause, token string, atWordBoundary bool) bool {
	for _, c := range clauses {
		for _, at := range occurrencesOf(c.text, token, atWordBoundary) {
			if c.asserted(at, at+len(token)) {
				return true
			}
		}
	}
	return false
}

// occurrencesOf returns the start offsets of token in text, at word boundaries
// when atWordBoundary is set.
func occurrencesOf(text, token string, atWordBoundary bool) []int {
	var out []int
	for i := 0; i+len(token) <= len(text); {
		j := strings.Index(text[i:], token)
		if j < 0 {
			break
		}
		start := i + j
		end := start + len(token)
		if !atWordBoundary || wordAt(text, start, end) {
			out = append(out, start)
		}
		i = start + 1
	}
	return out
}

// wordAt reports whether [start,end) sits on word boundaries in text.
func wordAt(text string, start, end int) bool {
	return (start == 0 || !isWordByte(text[start-1])) &&
		(end == len(text) || !isWordByte(text[end]))
}

// containsWord reports a token present at word boundaries.
//
// isWordByte knows only lowercase, so this reads lowercased text. Every caller
// passes an assessment's own Boundary or Condition, which are written that way.
func containsWord(text, token string) bool {
	return len(occurrencesOf(text, token, true)) != 0
}

func isWordByte(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9')
}

// outwardSurfaces are repository paths whose CONTENT causes outward effects
// when it later runs. Editing one is still an edit; the effect belongs to the
// action that runs it.
//
// Named on a bounded assessment rather than used to refuse it, because
// refusing here would mean an agent may never touch a release workflow even to
// fix it in a worktree — while a later publish of that same file is exactly
// what a second assessment is for.
var outwardSurfaces = []string{
	".github/workflows/",
	"packaging/",
	"internal/publish/",
}

// AssessConsequences answers one question about one action.
func AssessConsequences(a Action) ConsequenceAssessment {
	assessment := ConsequenceAssessment{}

	for _, f := range a.Files {
		for _, s := range outwardSurfaces {
			if strings.HasPrefix(strings.TrimPrefix(f, "./"), s) {
				assessment.Effects = append(assessment.Effects,
					"touches an outward-effect surface: "+f)
			}
		}
	}

	// A declared outward action escalates whatever the stage is. This is the
	// direction a claim is allowed to move an assessment.
	declaredOutward := declaredOutwardActions(a.DeclaredSteps, a.DeclaredConsequences)
	if len(declaredOutward) != 0 {
		assessment.Result = ConsequenceUnacceptable
		assessment.Effects = append(assessment.Effects, "the plan declares an outward action: "+strings.Join(declaredOutward, ", "))
		assessment.Evidence = append(assessment.Evidence, "read from the plan's own steps and consequences")
		assessment.Boundary = "none established: the plan states it will act outside the worktree"
		return assessment
	}

	switch a.Stage {
	case StageObserve:
		assessment.Result = ConsequenceBounded
		assessment.Boundary = "nothing is written: the observation lane reads the repository, " +
			"creates no candidate worktree, and ends by reporting findings rather than by admitting a change"
		assessment.Evidence = append(assessment.Evidence,
			"stage is structural — set by the entrypoint, not claimed by the plan",
			"the run is verified to have produced no working-tree change before it may report observed")
	case StageCandidateEdit:
		assessment.Result = ConsequenceBounded
		assessment.Boundary = "the candidate worktree: files are edited in an isolated checkout, " +
			"the diff is audited before it leaves, and discarding the worktree leaves the world unchanged"
		assessment.Evidence = append(assessment.Evidence,
			"stage is structural — set by the workflow, not claimed by the plan",
			"no outward action is declared by the plan (absence of a declaration is not evidence of absence, "+
				"which is why the boundary above is what carries this result)")
	case StagePublish:
		assessment.Result = ConsequenceUnacceptable
		assessment.Boundary = "none established: this stage is what makes a change observable outside the repository"
		assessment.Effects = append(assessment.Effects, "the action itself leaves the worktree")
		assessment.Evidence = append(assessment.Evidence, "stage is structural")
	default:
		// An unclassified stage fails closed as ignorance rather than as risk.
		assessment.Result = ConsequenceCannotEstablish
		assessment.Boundary = "unknown: this action's stage has not been classified, so nothing here bounds it"
		assessment.Evidence = append(assessment.Evidence, "stage "+string(a.Stage)+" has no reading")
	}
	return assessment
}

// Bounded reports whether the technical lane may continue on this action.
func (c ConsequenceAssessment) Bounded() bool { return c.Result == ConsequenceBounded }

// documentEvidenceFor returns the protecting invariant identities recorded for a
// document, and whether the graph was asked at all.
//
// Sorted, because a record's identity must not depend on map iteration order: two runs
// over one world would otherwise produce two different evidence identities for the same
// document.
func (a Action) documentEvidenceFor(file string) ([]string, bool) {
	if a.DocumentEvidence == nil {
		return nil, false
	}
	ids, asked := a.DocumentEvidence[path.Clean(strings.TrimSpace(file))]
	if !asked {
		return nil, false
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if t := strings.TrimSpace(id); t != "" {
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out, true
}

// documentArtifacts are the planned documents whose governance could not be established:
// the graph was never asked, or it was asked and the identities it returned are blank.
func (a Action) ungovernedDocumentArtifacts() []string {
	planned := a.plannedCreateSet()
	var out []string
	for _, f := range a.Files {
		c := path.Clean(strings.TrimSpace(f))
		if classifyArtifact(c) != classDocument {
			continue
		}
		// A document this task declared it will CREATE, proven absent at the
		// pinned base, cannot already be protected by an invariant: an
		// invariant's protects.files names a file that exists. The exemption is
		// from that impossibility alone. It confers no protection, credits the
		// document with no invariant identity, and ends the moment the file is
		// created -- after which it is reviewed as an ordinary artifact and
		// owed a normal identity once it lands.
		if planned[c] {
			continue
		}
		ids, asked := a.documentEvidenceFor(c)
		if !asked {
			// Never looked. Absence of a Go anchor is not proof of ordinariness.
			out = append(out, c)
			continue
		}
		if len(ids) == 0 && len(a.DocumentEvidence[c]) != 0 {
			// Asked, and every identity it returned was blank: an identity that cannot be
			// named is not an identity.
			out = append(out, c)
		}
	}
	return out
}

// unsupportedArtifacts are planned files no evidence class can prove anything about.
func (a Action) unsupportedArtifacts() []string {
	var out []string
	for _, f := range a.Files {
		if c := path.Clean(strings.TrimSpace(f)); classifyArtifact(c) == classUnsupported {
			out = append(out, c)
		}
	}
	return out
}

// probeSet are the planned files worth asking the graph about ONE AT A TIME.
//
// Production Go, for its coverage verdict, plus DOCUMENTS, for the identity of the
// invariants that protect them. Passing only architecturalFiles() left documents
// unprobed, so their evidence record stayed absent and every document read as "the graph
// never looked" -- fail-closed, but permanently, which is a different defect from the one
// T3 fixes.
//
// Tests are not probed: their governance is the edit grant, computed elsewhere, and a
// per-file coverage answer about a test file is exactly the evidence no derivation family
// can produce.
func (a Action) probeSet() []string {
	var out []string
	for _, f := range a.Files {
		c := path.Clean(strings.TrimSpace(f))
		switch classifyArtifact(c) {
		case classProductionGo, classDocument:
			out = append(out, c)
		}
	}
	return out
}

// cleanPlannedPath is the one canonicalisation the evidence records use, so a key written
// by the probe and a key read by the classifier cannot differ by spelling.
func cleanPlannedPath(f string) string { return path.Clean(strings.TrimSpace(f)) }
