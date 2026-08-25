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

import "strings"

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
var outwardVerbs = []string{
	"deploy", "publish", "release", "push to", "git push",
	"migrate", "migration", "drop table", "truncate",
	"upload", "send email", "notify", "post to", "curl -x post",
	"terraform apply", "kubectl apply", "helm install",
	"docker push", "npm publish", "production",
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
	declared := strings.ToLower(strings.Join(append(append([]string{}, a.DeclaredSteps...), a.DeclaredConsequences), " \n "))
	var declaredOutward []string
	for _, verb := range outwardVerbs {
		if strings.Contains(declared, verb) {
			declaredOutward = append(declaredOutward, verb)
		}
	}
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
