package workflow

import "testing"

// The consequence lane classifies declared ACTIONS. It does not search prose
// for frightening words.
//
// The first governed run against a foreign repository was refused as "declares
// an outward action: release" because its plan said "acquisition and release
// logic remains unchanged" -- semaphore.Weighted.Release, a method. Ambiguous
// prose must never silently become an outward action; an undeclared publish is
// stopped by the stage boundary regardless.
func TestOutwardActionsAreReadFromDeclaredActionsNotScaryWords(t *testing.T) {
	notOutward := []string{
		"acquisition and release logic remains unchanged",
		"Weighted.Release restores capacity to the semaphore",
		"release a waiter once its weight is available",
		"notifyWaiters wakes queued callers under mu",
		"migrate the field into the struct that owns the lock",
		"production of the snapshot happens under the same mutex",
		"the deployment.go helper is unchanged",
		"upload_test.go covers the parser",
	}
	for _, s := range notOutward {
		if got := declaredOutwardActions([]string{s}, ""); len(got) != 0 {
			t.Fatalf("%q was read as an outward action (%v); ambiguous prose must not "+
				"silently acquire consequence authority", s, got)
		}
	}
	outward := map[string]string{
		"cut a release for v1.2":                        "cut a release",
		"publish release v1.2 to the registry":          "publish",
		"deploy to the staging cluster":                 "deploy",
		"git push the branch to origin":                 "git push",
		"run the migration against the production db":   "run the migration",
		"notify the team once the artifact is uploaded": "notify the team",
	}
	for s, want := range outward {
		got := declaredOutwardActions([]string{s}, "")
		if len(got) == 0 {
			t.Fatalf("%q declares an outward action and was not read as one", s)
		}
		found := false
		for _, g := range got {
			if g == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("%q: expected %q among %v", s, want, got)
		}
	}
}

// And the assessment itself, end to end, on the sentence that caused the halt.
func TestASemaphorePlanIsNotADeployment(t *testing.T) {
	a := Action{Stage: StageCandidateEdit, Files: []string{"semaphore/semaphore.go"},
		DeclaredSteps: []string{"add Stats returning held weight and queued waiters under mu"},
		DeclaredConsequences: "Calling Stats briefly contends on Weighted's existing mutex, " +
			"but acquisition and release logic remains unchanged."}
	if got := AssessConsequences(a); got.Result != ConsequenceBounded {
		t.Fatalf("a semaphore edit was assessed %s (%s); the word 'release' named a method",
			got.Result, got.Boundary)
	}
	a.DeclaredSteps = append(a.DeclaredSteps, "then cut a release and publish it")
	if got := AssessConsequences(a); got.Result != ConsequenceUnacceptable {
		t.Fatal("a plan that declares cutting a release must still escalate")
	}
}

// POLARITY, NOT STRINGS. One hazardous operation, read in twenty-six contexts.
//
// Observed 2026-09-20 on task-1789870806342069862: the plan step "publication
// may open a branch and pull request only, never merge or push to main" was
// assessed UNACCEPTABLE with "the plan declares an outward action: push to",
// and the task stopped at a Level-3 human boundary the plan had promised never
// to reach. The inversion is the harm: the more carefully an objective writes
// down what it will NOT do, the more certainly it escalates.
//
// So the operation is held constant -- one outward action, mostly "push to
// main" -- and only the context around it moves. A row that passed because the
// wording changed would prove nothing; these rows share the wording.
//
// Rows 2 and 3 are the defect: a prohibition and a reported rule, both refused
// before this repair. EVERY OTHER ROW IS A CONTROL, and they are all the same
// shape -- a plan that genuinely declares the action, which must still reach a
// person. Each one names a specific way to overshoot, and each of those is
// somebody's plausible first fix:
//
//	row 4  a negation AFTER the operation -- suppressing a sentence that
//	       contains "no"
//	row 5  a negation BEFORE it, governing something else, joined by "and" --
//	       counting every negator earlier in the clause; row 4 cannot see this
//	row 6  the operation quoted and PERFORMED -- suppressing every quoted
//	       span; row 3 cannot see this
//	row 7  row 5 with the connector swapped for a dash -- ending a negation
//	       only at a coordination; row 5 cannot see this
//	row 8  a double negation -- reading a negator's PRESENCE rather than what
//	       it negates, which is the third recorded forbidden fix; no other row
//	       can see this
//	row 9  a step NAMED in quotation marks and then run -- treating labelling
//	       as citation; row 6 cannot see this, because it has no cue at all
//	row 10 a conjunction under a negation ("not stop and push to main") --
//	       carrying a negation across "and"; row 5 cannot see this, because
//	       its negation is stopped by a pronoun too. Pair to row 2.
//	row 11 a noun built on a negating stem ("preventive") -- matching those
//	       stems by bare prefix
//	row 12 a prohibition in the previous SENTENCE -- not splitting clauses
//	row 14 an IMPERATIVE second clause after a dash -- stopping a negation
//	       only at a pronoun or an auxiliary; row 7 cannot see this, because
//	       "the run will" supplies both
//	row 15 "instead of X, <asserted>" -- reading a prepositional negator as
//	       one that runs forward, which inverts the guard outright
//	row 16 row 8's sentence plus "no-op" -- letting a hyphenated compound
//	       donate a negator, which reverses parity rather than blunting it
//	row 17 a quotation introduced by a FETCHING verb ("reads") -- row 9
//	       removed labelling words and cannot see this one
//	row 19 a subordinator ("before") -- row 12's property where the boundary
//	       is a word rather than a full stop
//	row 20 a RUN-ON with no connector -- the only row where the new predicate
//	       must be recognised by its subject alone
//	row 21 " so " -- a consequence connector, distinct from " so that "
//	row 22 a THIRD labelling verb -- applying rows 9 and 17's argument to the
//	       two words somebody happened to test, and no further
//	row 23 "cannot complete without a push to main" -- dropping a
//	       complement negator outright instead of scoping it
//	row 24 "without further review push to main" -- the pair to 23: listing
//	       that same negator as one that runs forward
//	row 25 an ADVERBIAL between the two halves of a double negative --
//	       ending a negation at a preposition, which was tried and removed
//	row 26 the same adverbial inside a prohibition -- the false-escalation
//	       half of that same removed rule
//	row 1  a bare declaration -- anything that stops reading
//
// Rows 13 and 18 are bounded like rows 2 and 3, and both are the original bug
// intact in a spelling people actually use: "don't" unless the contraction is
// expanded, and "neither merge nor push to main" unless a concord is counted
// once. Row 18 is the pair to row 2 -- the same prohibition, spelled the more
// careful way.
//
// The pairings are the point. Rows 4/5/7 are one sentence with its reassurance
// moved and its connector swapped; rows 3/6/9 are one quotation under three
// introducing words. A repair that gets one of a pair right and the other wrong
// is exactly the repair this test exists to refuse.
func TestTheSameOutwardOperationIsClassifiedByWhatThePlanAsserts(t *testing.T) {
	for _, tc := range []struct {
		name       string
		step       string
		wantResult ConsequenceResult
		wantRoute  Route
		says       string
	}{{
		// 1. Asserted. The plan says it will do the thing.
		name:       "an affirmative outward action escalates",
		step:       "once the tests pass the run will push to main so CI picks the change up",
		wantResult: ConsequenceUnacceptable,
		wantRoute:  RouteHuman,
		says:       "outside the worktree",
	}, {
		// 2. Prohibited. The plan says it will NOT do the thing. Verbatim from
		// the live halt, so this row is bound to the observation rather than to
		// a paraphrase of it -- and note that one negator governs a coordination
		// ("never merge or push to main"), which is why " or " is not a clause
		// break.
		name:       "an explicit prohibition is not a declaration",
		step:       "publication may open a branch and pull request only, never merge or push to main",
		wantResult: ConsequenceBounded,
		wantRoute:  RouteArchitectural,
	}, {
		// 3. Mentioned. The operation is quoted, as the name of a boundary the
		// run is writing a test for. Deliberately carries no negator of its own,
		// so it can only pass if mentions are distinguished from uses.
		name:       "a quoted forbidden action is a mention, not a use",
		step:       `the objective the worker was given says "push to main" is out of bounds, and this run adds the test that proves it`,
		wantResult: ConsequenceBounded,
		wantRoute:  RouteArchitectural,
	}, {
		// 4. Asserted, wrapped in reassurance that FOLLOWS it. The negator sits
		// in the SAME clause as the push and negates something else entirely,
		// so this row fails both ways a repair can overshoot: suppressing a
		// sentence that contains "no", and counting negators anywhere in the
		// clause instead of only ahead of the operation they govern.
		name:       "reassuring prose does not clear an asserted outward action",
		step:       "every edit stays in the disposable candidate worktree; the last step will push to main and no reviewer is bypassed",
		wantResult: ConsequenceUnacceptable,
		wantRoute:  RouteHuman,
		says:       "outside the worktree",
	}, {
		// 5. The same reassurance, moved AHEAD of the push -- and row 4 cannot
		// see this. Counting every negator earlier in the clause clears the
		// push here while row 4 still passes, so ordering is the whole test:
		// "no" negates the bypass, the coordination then starts a new predicate
		// with its own subject, and the push it introduces is asserted. A
		// negation that merely precedes an operation has said nothing about it.
		name:       "a negation that governs something else does not clear the push that follows it",
		step:       "no reviewer is bypassed and the run will push to main",
		wantResult: ConsequenceUnacceptable,
		wantRoute:  RouteHuman,
		says:       "outside the worktree",
	}, {
		// 6. Quoted and PERFORMED. Row 3's operation is quoted too, and the
		// only difference is the word introducing the quotation: "says" reports
		// it, "execute" runs it. Suppressing every quoted span makes these two
		// rows identical, and this is the one where a run that had just said it
		// would publish reached a grant with nobody asked.
		name:       "an asserted quoted command is a use, not a mention",
		step:       `the final step is to execute "push to main" exactly as written`,
		wantResult: ConsequenceUnacceptable,
		wantRoute:  RouteHuman,
		says:       "outside the worktree",
	}, {
		// 7. Row 5 with the connector swapped, and nothing else. An earlier
		// version stopped a negation only at a coordination, so row 5 passed
		// on the word "and" and this sentence -- the same claim, one
		// punctuation mark -- was granted. A missing connector must not be the
		// difference between asking a person and not.
		name:       "a connector the break list never listed does not carry the negation",
		step:       "no reviewer is bypassed - the run will push to main",
		wantResult: ConsequenceUnacceptable,
		wantRoute:  RouteHuman,
		says:       "outside the worktree",
	}, {
		// 8. PARITY, NOT PRESENCE -- the negative control for the third
		// recorded forbidden fix, which is to read a negator's presence
		// instead of what it negates. Two negations that reach the operation
		// cancel, and the plan is declaring the deploy. A classifier that
		// suppressed on presence would grant this while rows 2 and 3 stayed
		// green, so without this row that forbidden fix is implementable
		// without any test noticing.
		name:       "a double negation asserts the action it double-negates",
		step:       "the run cannot avoid a deploy to production",
		wantResult: ConsequenceUnacceptable,
		wantRoute:  RouteHuman,
		says:       "outside the worktree",
	}, {
		// 9. Named, then run. "named" is how a plan labels a step it is about
		// to perform, and reading labelling words as citation granted exactly
		// the plans that were most explicit about what they would do. Only
		// reported speech makes a quotation a mention; compare row 3.
		name:       "naming a step is not reporting a rule",
		step:       `the step named "deploy to production" runs last`,
		wantResult: ConsequenceUnacceptable,
		wantRoute:  RouteHuman,
		says:       "outside the worktree",
	}, {
		// 10. A conjunction under a negation does not distribute: "not stop"
		// and "push to main" are two statements, and the second is asserted.
		// Row 5 cannot see this -- its negation is also stopped by "is" and
		// "will" -- so without this row the conjunction rule is unproven, and
		// a disjunction is what makes row 2 bounded. This is the pair to row 2.
		name:       "a conjunction under a negation does not carry it",
		step:       "we will not stop and push to main",
		wantResult: ConsequenceUnacceptable,
		wantRoute:  RouteHuman,
		says:       "outside the worktree",
	}, {
		// 11. A negating STEM must appear as a verb. Bare prefix matching read
		// "preventive", "prevention", "avoidance", "refusal" and "rejected" as
		// negations, and one spurious negator flips parity -- the fail-open
		// direction. This repository's own prose is full of these words.
		name:       "a noun built on a negating stem is not a negation",
		step:       "preventive checks push to main immediately",
		wantResult: ConsequenceUnacceptable,
		wantRoute:  RouteHuman,
		says:       "outside the worktree",
	}, {
		// 12. A sentence boundary resets polarity. Without clause splitting a
		// prohibition in one sentence suppresses a declaration in the next,
		// which is how a plan states its limits and then states its plan.
		name:       "a prohibition does not reach into the next sentence",
		step:       "never merge. deploy to production is the final step",
		wantResult: ConsequenceUnacceptable,
		wantRoute:  RouteHuman,
		says:       "outside the worktree",
	}, {
		// 13. A contraction is a negation. No word split recovers the negator
		// inside "don't", so without the expansion this prohibition reads as a
		// declaration and escalates -- the original false-escalation bug, in
		// the one spelling people actually use. The only bounded control here
		// besides rows 2 and 3.
		name:       "a contracted negation is still a negation",
		step:       "we don't push to main",
		wantResult: ConsequenceBounded,
		wantRoute:  RouteArchitectural,
	}, {
		// 14. An IMPERATIVE second clause. Row 7 passes on "the run will",
		// which supplies a pronoun and an auxiliary for the negation to stop
		// at; plan prose is mostly imperative and supplies neither, so the
		// prohibition ran straight into the instruction after the dash. The
		// connector is what a reader sees as the boundary.
		name:       "an imperative after a connector does not inherit the prohibition",
		step:       "never touch the vendor tree - deploy to production from the release branch",
		wantResult: ConsequenceUnacceptable,
		wantRoute:  RouteHuman,
		says:       "outside the worktree",
	}, {
		// 15. A PREPOSITIONAL negator names the branch NOT taken, so what
		// follows is the branch the plan is choosing. Read as a forward-running
		// negator it suppressed precisely the asserted action -- the guard
		// inverted on a sentence whose whole purpose is to state intent.
		name:       "instead of X names the rejected branch, not the asserted one",
		step:       "instead of merging push to main",
		wantResult: ConsequenceUnacceptable,
		wantRoute:  RouteHuman,
		says:       "outside the worktree",
	}, {
		// 16. Row 8's own sentence, plus three characters. "-" is not a word
		// byte, so "no-op" donated a free "no" and flipped parity -- the
		// double-negation control inverted by a word this repository writes
		// constantly. One spurious negator does not degrade parity, it reverses
		// it, which is why this is not a rounding error.
		name:       "a hyphenated compound does not donate a negator",
		step:       "the no-op run cannot avoid a deploy to production",
		wantResult: ConsequenceUnacceptable,
		wantRoute:  RouteHuman,
		says:       "outside the worktree",
	}, {
		// 17. Row 9 removed labelling words; this removes FETCHING ones. A
		// machine reads a command in order to run it, so "reads", "writes" and
		// "states" introduce a quotation that is used, not reported. Row 9
		// cannot see this: "named" labels, "reads" retrieves.
		name:       "an ordinary verb before a quotation is not a citation",
		step:       "the workflow reads `deploy to production` from the matrix and executes it",
		wantResult: ConsequenceUnacceptable,
		wantRoute:  RouteHuman,
		says:       "outside the worktree",
	}, {
		// 18. NEGATIVE CONCORD, and the fifth bounded row. One prohibition
		// spelled with two words: counted as two negations it cancelled itself
		// and escalated, which is the original bug in the most careful
		// phrasing of the sentence that caused it. Pair to row 2.
		name:       "neither/nor is one prohibition, not two",
		step:       "neither merge nor push to main",
		wantResult: ConsequenceBounded,
		wantRoute:  RouteArchitectural,
	}, {
		// 19. A subordinator ends the clause it subordinates. Row 12 proves a
		// sentence boundary resets polarity; this is the same property where
		// the boundary is a word rather than a full stop, which is how a plan
		// usually writes a limit and then the step that follows it.
		name:       "a prohibition does not reach past the subordinator that ends it",
		step:       "do not stop before deploy to production",
		wantResult: ConsequenceUnacceptable,
		wantRoute:  RouteHuman,
		says:       "outside the worktree",
	}, {
		// 20. A RUN-ON, with no connector at all. Rows 5, 7, 14 and 19 are each
		// saved by something in the text -- a conjunction, a dash, a
		// subordinator -- and terse plan prose often supplies none of them.
		// Here the only thing that ends the prohibition is the AUXILIARY in the
		// second predicate, which is what keeps predicateMarkers real work
		// rather than a rule the break list already covers.
		//
		// Not the subject: strip both "is" and "will" and "no reviewer bypassed
		// the run push to main" is still read as bounded, because a noun-phrase
		// subject ends nothing. That shape is a known limit, recorded with the
		// others above declaredOutwardActions.
		name:       "a new predicate with no connector still ends the negation",
		step:       "no reviewer is bypassed the run will push to main",
		wantResult: ConsequenceUnacceptable,
		wantRoute:  RouteHuman,
		says:       "outside the worktree",
	}, {
		// 21. "so" is a consequence connector: what follows is the result, not
		// more of the prohibition. It is listed separately from " so that ",
		// which is a purpose clause, and only this row tells the two apart.
		name:       "a consequence connector does not carry the prohibition into its result",
		step:       "the branch is not merged so deploy to production happens from the tag",
		wantResult: ConsequenceUnacceptable,
		wantRoute:  RouteHuman,
		says:       "outside the worktree",
	}, {
		// 22. Rows 9 and 17 removed labelling and fetching words; this is the
		// third member of that family, and it is the one that shows the
		// argument has to be applied to the whole list rather than to the two
		// words somebody happened to test. "the matrix describes X and the job
		// runs it" labels a step exactly as "named" does.
		name:       "a labelling verb is not reporting, whatever its spelling",
		step:       `the step described "deploy to production" runs last`,
		wantResult: ConsequenceUnacceptable,
		wantRoute:  RouteHuman,
		says:       "outside the worktree",
	}, {
		// 23. "without" negates a NOUN PHRASE, and here the operation IS that
		// phrase, so this is a double negative that asserts the push: the
		// release cannot complete unless it happens. Row 8 is the same property
		// through the "avoid" stem and cannot see this; dropping "without" from
		// the negators outright fixed row 24's shape and broke this one.
		name:       "a complement negation still composes into a double negative",
		step:       "the release cannot complete without a push to main",
		wantResult: ConsequenceUnacceptable,
		wantRoute:  RouteHuman,
		says:       "outside the worktree",
	}, {
		// 24. The pair to row 23, and the reason "without" is scoped rather
		// than simply listed. Here its complement is "further review", not the
		// push, so the push is what the plan says it will do -- and reading
		// "without" as a negator that runs forward suppressed exactly that.
		name:       "a complement negation does not reach the clause after its complement",
		step:       "without further review push to main",
		wantResult: ConsequenceUnacceptable,
		wantRoute:  RouteHuman,
		says:       "outside the worktree",
	}, {
		// 25. The outer negator of a DOUBLE NEGATIVE, with an adverbial in the
		// way. Row 8 is one fixed string and cannot see this: a rule that ended
		// a negation at a preposition was added beside it, and "in practice"
		// then cut "cannot" off from "avoid", leaving parity 1 and clearing the
		// deploy. That rule is gone, and this row is what keeps it gone --
		// forbidden fix 3's class has to stay closed against ordinary adverbial
		// English, not just against the one sentence somebody wrote down.
		name:       "an adverbial does not cut the outer negator out of a double negative",
		step:       "the run cannot in practice avoid a deploy to production",
		wantResult: ConsequenceUnacceptable,
		wantRoute:  RouteHuman,
		says:       "outside the worktree",
	}, {
		// 26. And the other direction the same rule broke: a careful
		// prohibition re-escalating is the harm this whole change exists to
		// remove, so it is a control in its own right rather than an
		// acceptable cost.
		name:       "an adverbial inside a prohibition does not re-escalate it",
		step:       "we must not at any point push to main",
		wantResult: ConsequenceBounded,
		wantRoute:  RouteArchitectural,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			action := Action{Stage: StageCandidateEdit,
				Files:         []string{"internal/workflow/consequence.go"},
				DeclaredSteps: []string{tc.step}}

			got := AssessConsequences(action)
			if got.Result != tc.wantResult {
				t.Fatalf("AssessConsequences = %s (%s), want %s\n  effects: %v\n  step: %q",
					got.Result, got.Boundary, tc.wantResult, got.Effects, tc.step)
			}

			routing := routeAuthorityForAction(
				scopedPreflight(t, okBody(t, nil, "APPROVAL_GATE_NONE")), nil, action)
			if routing.Route != tc.wantRoute {
				t.Fatalf("route = %s (%s), want %s\n  step: %q",
					routing.Route, routing.Condition, tc.wantRoute, tc.step)
			}
			// And it must reach that route for the stated reason. An escalation
			// that arrived by some other path would satisfy the route check
			// while proving nothing about this classifier.
			if tc.says != "" && !containsWord(routing.Condition, tc.says) {
				t.Fatalf("escalated without naming why: %q", routing.Condition)
			}
			if tc.wantResult == ConsequenceBounded && !containsWord(got.Boundary, "candidate worktree") {
				t.Fatalf("bounded without naming the boundary: %q", got.Boundary)
			}
		})
	}
}

// A quotation belongs to the step that wrote it.
//
// Masking ran over the joined text, so an unbalanced delimiter in one step
// paired with a delimiter several steps later and blanked everything between
// them -- every declaration in the gap, silently, with no negation and no
// citation anywhere near them. It is the only fault in this classifier whose
// reach is unbounded: one stray backtick, and an arbitrary number of asserted
// outward actions stop existing.
//
// Backticks make it ordinary rather than exotic. Plans are markdown-flavoured
// prose, and a lone backtick around a path or an identifier, after a word like
// "says", is a completely normal thing to write.
func TestAStrayDelimiterDoesNotReachIntoAnotherStep(t *testing.T) {
	for _, tc := range []struct {
		name  string
		steps []string
		// survives names the token that sits on the OTHER side of the stray
		// delimiter. Asserting only that something was found would pass on a
		// regression that blanked the later lines, because the same line as
		// the delimiter can contribute a token of its own.
		survives string
	}{{
		name:     "an unbalanced quotation does not blank the steps after it",
		steps:    []string{`the objective says "push to main`, "deploy to production", `is refused"`},
		survives: "deploy",
	}, {
		name:     "an unbalanced backtick does not blank the steps after it",
		steps:    []string{"the rule says `git push", "then deploy to production", "see publish.go`"},
		survives: "deploy",
	}, {
		// And inside ONE step, across its own lines. Steps are joined with a
		// newline, so per-step masking alone would look like it covered this;
		// a step is prose and carries its own line breaks, and the reach is
		// the same.
		name:     "an unbalanced delimiter does not blank the next line of its own step",
		steps:    []string{"the rule says `git push\ndeploy to production is the last step\nsee publish.go`"},
		survives: "deploy",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			got := declaredOutwardActions(tc.steps, "")
			found := false
			for _, g := range got {
				if g == tc.survives {
					found = true
				}
			}
			if !found {
				t.Fatalf("%q was erased by a delimiter on another line; read %v from %q",
					tc.survives, got, tc.steps)
			}
			// And the same delimiter, balanced inside one step, still masks --
			// otherwise this would pass on a classifier that had simply stopped
			// reading quotations at all.
			cited := []string{`the objective says "push to main" is refused`}
			if got := declaredOutwardActions(cited, ""); len(got) != 0 {
				t.Fatalf("a cited rule inside one step was read as a declaration: %v", got)
			}
		})
	}
}
