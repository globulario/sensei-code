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
	var out []string
	for _, f := range a.architecturalFiles() {
		if unexamined[f] {
			out = append(out, f)
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

// declaredOutwardActions reads a plan's own steps and consequences for an
// outward action it declares.
//
// Word-anchored for the bare tokens, so "deploy" does not fire inside
// "deployment.go" or "redeployable"; phrase-anchored for the ambiguous ones.
func declaredOutwardActions(steps []string, consequences string) []string {
	declared := strings.ToLower(strings.Join(append(append([]string{}, steps...), consequences), " \n "))
	var found []string
	for _, verb := range outwardVerbs {
		if containsWord(declared, verb) {
			found = append(found, verb)
		}
	}
	for _, phrase := range outwardPhrases {
		if strings.Contains(declared, phrase) {
			found = append(found, strings.TrimSpace(phrase))
		}
	}
	return found
}

// containsWord reports a token present at word boundaries.
func containsWord(text, token string) bool {
	for i := 0; ; {
		j := strings.Index(text[i:], token)
		if j < 0 {
			return false
		}
		start := i + j
		end := start + len(token)
		beforeOK := start == 0 || !isWordByte(text[start-1])
		afterOK := end == len(text) || !isWordByte(text[end])
		if beforeOK && afterOK {
			return true
		}
		i = start + 1
		if i >= len(text) {
			return false
		}
	}
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
	var out []string
	for _, f := range a.Files {
		c := path.Clean(strings.TrimSpace(f))
		if classifyArtifact(c) != classDocument {
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
