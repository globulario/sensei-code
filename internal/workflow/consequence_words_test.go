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

// POLARITY, NOT STRINGS. One hazardous operation, read in forty-two contexts.
//
// Observed 2026-09-20 on task-1789870806342069862: the plan step "publication
// may open a branch and pull request only, never merge or push to main" was
// assessed UNACCEPTABLE with "the plan declares an outward action: push to",
// and the task stopped at a Level-3 human boundary the plan had promised never
// to reach. The inversion is the harm: the more carefully an objective writes
// down what it will NOT do, the more certainly it escalates.
//
// So the operation is held constant -- one outward action, almost always "push
// to main" -- and only the context around it moves. A row that passed because
// the wording changed would prove nothing; these rows share the wording.
//
// Rows 2, 3 and 39 are the defect: a prohibition, a quoted rule, and the second
// sentence the live run also refused. The BOUNDED rows below them are the
// ordinary prohibition forms an earlier attempt escalated anyway. Every other
// row is a CONTROL of the same shape -- a plan that genuinely declares the
// action, which must still reach a person -- and each one names a specific way
// to overshoot.
//
// # The four required contexts
//
//	affirmative intent            rows 1, 40, 41
//	explicit prohibition          rows 2, 13, 18, 26, 30, 31, 32, 33, 39, 42, 43
//	quoted or described forbidden action   row 3
//	asserted action wrapped in reassurance rows 4, 5, 7, 27
//
// "do not merge" is in the task's required behaviour and is not a row, because
// it would be vacuous: "merge" is not in outwardVerbs at all, so no wording of
// it can produce a merge consequence and the row would prove nothing about
// polarity. Row 2 carries the same prohibition over an operation that IS in the
// vocabulary.
//
// # What each control is guarding
//
//	row 4  a negation AFTER the operation -- suppressing a sentence that
//	       contains "no"
//	row 5  a negation BEFORE it, governing something else, joined by "and" --
//	       counting every negator earlier in the clause; row 4 cannot see this
//	row 6  the operation quoted and PERFORMED -- suppressing every quoted span
//	row 7  row 5 with the connector swapped for a dash
//	row 8  a double negation -- reading a negator's PRESENCE rather than what
//	       it negates, which is the third recorded forbidden fix; no other row
//	       can see this
//	row 9  a step NAMED in quotation marks and then run -- treating labelling
//	       as citation
//	row 10 a conjunction under a negation ("not stop and push to main");
//	       row 2's disjunction is what makes these two differ, and only this
//	       pair proves the asymmetry is De Morgan's rather than a preference
//	row 11 a noun built on a negating stem ("preventive") -- matching those
//	       stems by bare prefix
//	row 12 a prohibition in the previous SENTENCE -- not splitting clauses
//	row 14 an IMPERATIVE second clause after a dash, which supplies neither a
//	       pronoun nor an auxiliary to stop at
//	row 15 "instead of X, <asserted>" -- reading a prepositional negator as one
//	       that runs forward, which inverts the guard outright
//	row 16 row 8's sentence plus "no-op" -- letting a hyphenated compound
//	       donate a negator, which reverses parity rather than blunting it
//	row 17 a quotation introduced by a FETCHING verb ("reads"), which a
//	       citation list had to keep growing to exclude
//	row 19 a subordinator ("before") -- row 12's property where the boundary
//	       is a word rather than a full stop, and needs no break entry
//	row 20 a RUN-ON with no connector at all
//	row 21 "so" -- a consequence connector introducing a result, not more of
//	       the prohibition
//	row 22 a THIRD labelling verb, applying rows 9 and 17's argument to the
//	       whole class rather than the two words somebody happened to test
//	row 23 "cannot complete without a push to main" -- a double negative
//	       through a prepositional negator
//	row 24 "without further review push to main" -- the pair to 23
//	row 25 an ADVERBIAL between the two halves of a double negative
//	row 27 "with no additional review push to main" -- a negated noun phrase
//	       inside an adjunct, then a bare imperative
//	row 28 row 23 with the same claim in the other word order, which an
//	       implementation that only looks BACKWARDS from the operation reads
//	       the opposite way
//	row 29 a quotation whose next clause performs what it cited
//	row 34 a negated subject with its own finite verb, then a second predicate
//	row 35 the operation as the object of a preposition after a negated subject
//	row 36 existential "there is no ..." over the same shape
//	row 37 a negated fragment followed by a bare imperative
//	row 38 the same, with no auxiliary anywhere in the clause
//	row 1  a bare declaration -- anything that stops reading
//
// Rows 27, 28, 29, 34, 35, 36, 37 and 38 are the ones that matter most, and
// they are here because an earlier implementation carried a negation forward
// until something recognised stopped it. Under that default every construction
// nobody had listed fell toward SUPPRESSION, so each of these plain
// declarations was read as bounded. They are the proof that the default is now
// the other way round: a negation suppresses an occurrence only where it can be
// shown to govern it.
//
// Rows 30, 31, 32 and 33 are the cost of that inversion paid off rather than
// accepted: ordinary prohibitions -- an embedded predicate under a permission
// verb, a negated subject with an auxiliary, and a postposed prohibition --
// which the same earlier implementation escalated. A classifier that only
// fixed the false grants by escalating everything would pass rows 27-38 and
// fail these.
//
// The pairings are the point. Rows 4/5/7 are one sentence with its reassurance
// moved and its connector swapped; rows 3/6/9/17/22/29 are one quotation under
// six introducing words; rows 23/28 are one claim in two word orders; rows
// 2/10 are one coordination under two connectors. A repair that gets one of a
// pair right and the other wrong is exactly the repair this test exists to
// refuse.
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
		// 3. QUOTED AND FORBIDDEN -- the third required context. The plan cites
		// the rule it is under, verbatim, in the sentence that also says what
		// this run does instead.
		//
		// What makes it bounded is the PROHIBITION inside the quotation, read
		// exactly as it would be read unquoted; the quotation marks contribute
		// nothing. That is deliberate, and row 29 is why: a repair that treated
		// a cited span as a mention and blanked it lost the operation in `the
		// runbook says "push to main" and the run does exactly that`, where the
		// very next clause performs what was quoted.
		name:       "a quoted prohibition is bounded by its prohibition",
		step:       `the objective the worker was given says "never push to main", and this run adds the test that proves it`,
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
		// 6. Quoted and PERFORMED. Row 3's operation is quoted too, and what
		// separates the two is what stands INSIDE the quotation: a prohibition
		// there, none here. A repair that suppressed every quoted span made
		// them identical, and this is the row where a run that had just said it
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
		// to perform, and an earlier repair that read labelling words as
		// citation granted exactly the plans that were most explicit about what
		// they would do. A quotation suppresses nothing now -- compare row 3,
		// where what is quoted carries its own prohibition.
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
		// the one spelling people actually use.
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
		// 17. Row 9 removed labelling words from the citation list; this
		// removed FETCHING ones, and the list itself is gone now. A machine
		// reads a command in order to run it, so no introducing word makes a
		// quotation safe -- which is why "reads" and "named" reach the same
		// answer by the same route.
		name:       "an ordinary verb before a quotation is not a citation",
		step:       "the workflow reads `deploy to production` from the matrix and executes it",
		wantResult: ConsequenceUnacceptable,
		wantRoute:  RouteHuman,
		says:       "outside the worktree",
	}, {
		// 18. NEGATIVE CONCORD. One prohibition
		// spelled with two words: counted as two negations it cancelled itself
		// and escalated, which is the original bug in the most careful
		// phrasing of the sentence that caused it. Pair to row 2.
		name:       "neither/nor is one prohibition, not two",
		step:       "neither merge nor push to main",
		wantResult: ConsequenceBounded,
		wantRoute:  RouteArchitectural,
	}, {
		// 19. A subordinator ends the prohibition. Row 12 proves a full stop
		// resets polarity; here the boundary is a word, and it needs no entry
		// in any break list -- "before" and "stop" are not predicate material,
		// so the walk from the deploy stops at them.
		name:       "a prohibition does not reach past the subordinator that ends it",
		step:       "do not stop before deploy to production",
		wantResult: ConsequenceUnacceptable,
		wantRoute:  RouteHuman,
		says:       "outside the worktree",
	}, {
		// 20. A RUN-ON, with no connector at all. Rows 5, 7, 14 and 19 are each
		// saved by something in the text -- a conjunction, a dash, a
		// subordinator -- and terse plan prose often supplies none of them.
		// What ends the prohibition here is simply that "run" is not predicate
		// material, so the walk from the push stops one word short of the
		// auxiliary chain.
		//
		// Row 34 is this sentence with both auxiliaries removed and must
		// escalate too. An earlier implementation read that one as bounded,
		// because a noun-phrase subject ended nothing.
		name:       "a new predicate with no connector still ends the negation",
		step:       "no reviewer is bypassed the run will push to main",
		wantResult: ConsequenceUnacceptable,
		wantRoute:  RouteHuman,
		says:       "outside the worktree",
	}, {
		// 21. "so" introduces a RESULT, not more of the prohibition. It needs
		// no entry in the break list and has none: it is not predicate
		// material, so the walk from the deploy stops there. An earlier version
		// had to list " so " separately from " so that " to get this right.
		name:       "a consequence connector does not carry the prohibition into its result",
		step:       "the branch is not merged so deploy to production happens from the tag",
		wantResult: ConsequenceUnacceptable,
		wantRoute:  RouteHuman,
		says:       "outside the worktree",
	}, {
		// 22. The third member of rows 9 and 17's family, and the one that
		// showed the argument had to reach the whole list rather than the two
		// words somebody happened to test -- "the matrix describes X and the
		// job runs it" labels a step exactly as "named" does. Three rounds of
		// that is why there is no list left to be incomplete.
		name:       "a labelling verb is not reporting, whatever its spelling",
		step:       `the step described "deploy to production" runs last`,
		wantResult: ConsequenceUnacceptable,
		wantRoute:  RouteHuman,
		says:       "outside the worktree",
	}, {
		// 23. "without" negates a NOUN PHRASE, and here the operation IS that
		// phrase, so the claim is that the release cannot complete unless the
		// push happens: the push is asserted. Nothing positively binds
		// "without" to the operation as a prohibition, so the default decides
		// it. Row 8 is the same property through the "avoid" stem and cannot
		// see this; row 28 is the same claim with the word order reversed.
		name:       "a complement negation still composes into a double negative",
		step:       "the release cannot complete without a push to main",
		wantResult: ConsequenceUnacceptable,
		wantRoute:  RouteHuman,
		says:       "outside the worktree",
	}, {
		// 24. The pair to row 23. Here "without" takes "further review" as its
		// complement rather than the push, so the push is what the plan says it
		// will do -- and an implementation that ran "without" forward as a
		// negator suppressed exactly that.
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
	}, {
		// 27. A negated NOUN PHRASE inside an adjunct, then a bare imperative.
		// "no" negates the review, not the push, and the plan is saying it will
		// push WITHOUT one -- the worst sentence in the table to misread. An
		// implementation that carries a negation forward until something
		// recognised stops it reads this as bounded, because nothing between
		// "no" and the push is on any stop list.
		name:       "a negated adjunct does not clear the imperative after it",
		step:       "with no additional review push to main",
		wantResult: ConsequenceUnacceptable,
		wantRoute:  RouteHuman,
		says:       "outside the worktree",
	}, {
		// 28. Row 23's claim in the other word order, and the pair that proves
		// the reading does not depend on it. The release cannot complete unless
		// the push happens, so the push is asserted both ways round; an
		// implementation that collects only what PRECEDES the operation reads
		// this one as bounded while row 23 escalates -- the same sentence
		// answered two ways.
		name:       "a fronted complement negation is still a double negative",
		step:       "without a push to main the release cannot complete",
		wantResult: ConsequenceUnacceptable,
		wantRoute:  RouteHuman,
		says:       "outside the worktree",
	}, {
		// 29. CITED, THEN PERFORMED. Row 3 is bounded and this is not, and the
		// only difference is that the quotation here carries no prohibition and
		// the next clause runs it. Blanking a cited span removes the single
		// hazardous token in this text, so the router is handed a bounded
		// result for a run that has just said it will publish. Rows 6, 9, 17
		// and 22 each vary the introducing word; this one varies what the
		// sentence goes on to do.
		name:       "a citation whose next clause performs it is a use",
		step:       `the runbook says "push to main" and the run does exactly that`,
		wantResult: ConsequenceUnacceptable,
		wantRoute:  RouteHuman,
		says:       "outside the worktree",
	}, {
		// 30. An EMBEDDED PREDICATE under a permission verb. "never allow it to
		// deploy" prohibits deploying, not allowing, and it is one of the most
		// ordinary ways to write a limit. Treating the pronoun as the start of
		// a new predicate escalated it -- a person asked to authorize a
		// boundary the plan had just drawn.
		name:       "a prohibition through a permission verb is not a declaration",
		step:       "never allow it to deploy",
		wantResult: ConsequenceBounded,
		wantRoute:  RouteArchitectural,
	}, {
		// 31. A NEGATED SUBJECT with an auxiliary. The auxiliary is what says
		// "no part of this run" heads the predicate the push is in; row 34
		// removes it and must escalate, which is what keeps this row from being
		// satisfied by suppressing every clause that begins with "no".
		name:       "a negated subject with an auxiliary is a prohibition",
		step:       "no part of this run will push to main",
		wantResult: ConsequenceBounded,
		wantRoute:  RouteArchitectural,
	}, {
		// 32. The same shape with a different subject noun, because the rule
		// must not depend on the noun: subject nouns are an open class, and a
		// classifier that happened to know "part" and not "step" would be the
		// second forbidden fix with a smaller whitelist.
		name:       "the negated subject may be any noun",
		step:       "no step in the plan will push to main",
		wantResult: ConsequenceBounded,
		wantRoute:  RouteArchitectural,
	}, {
		// 33. POSTPOSED. The prohibition comes AFTER the operation it governs,
		// which nothing reading leftwards from the operation can see. This is
		// how a plan states a rule about a named action rather than a step it
		// is taking.
		name:       "a postposed prohibition governs the operation before it",
		step:       "push to main is never allowed",
		wantResult: ConsequenceBounded,
		wantRoute:  RouteArchitectural,
	}, {
		// 34. Row 31 with the auxiliary removed, so "no reviewer bypassed" is a
		// complete predicate of its own and the push that follows is a second
		// one. Without this row, row 31 is satisfiable by suppressing anything
		// that starts with "no", which is the third forbidden fix narrowed to
		// one word.
		name:       "a negated subject with its own verb does not reach the next predicate",
		step:       "no reviewer bypassed the run push to main",
		wantResult: ConsequenceUnacceptable,
		wantRoute:  RouteHuman,
		says:       "outside the worktree",
	}, {
		// 35. The operation as the object of a PREPOSITION after a negated
		// subject: what is denied is the approval gate, and the push is the
		// thing nothing now stands in the way of. Read as bounded, this is a
		// plan announcing an unguarded publish and being granted it.
		name:       "a negated subject does not reach into a following prepositional phrase",
		step:       "no approval gate remains for the push to main",
		wantResult: ConsequenceUnacceptable,
		wantRoute:  RouteHuman,
		says:       "outside the worktree",
	}, {
		// 36. The existential form of row 35. "there is no support for X" says
		// X is unsupported, not that X will not happen -- and the clause does
		// not even begin with the negator, so a rule keyed on that alone would
		// pass this by accident rather than on purpose.
		name:       "an existential negation does not clear the operation it is about",
		step:       "there is no support for a push to main",
		wantResult: ConsequenceUnacceptable,
		wantRoute:  RouteHuman,
		says:       "outside the worktree",
	}, {
		// 37. A negated FRAGMENT followed by a bare imperative, with no
		// connector between them. "no manual approval required" is the
		// reassurance; "push to main automatically" is the plan.
		name:       "a negated fragment does not clear the imperative after it",
		step:       "no manual approval required push to main automatically",
		wantResult: ConsequenceUnacceptable,
		wantRoute:  RouteHuman,
		says:       "outside the worktree",
	}, {
		// 38. The terse version, with no auxiliary anywhere. Plan prose written
		// as notes looks exactly like this, and every word of it is either a
		// negator, a noun or the operation.
		name:       "a negated noun fragment does not clear the imperative after it",
		step:       "no exceptions deploy to production at the end",
		wantResult: ConsequenceUnacceptable,
		wantRoute:  RouteHuman,
		says:       "outside the worktree",
	}, {
		// 39. The SECOND sentence the live defect refused, run directly, which
		// is what proved it was not one verb and not one wording. The
		// prohibition and the account of what the run actually does are two
		// sentences, and both have to be read correctly for this to be bounded.
		name:       "the second observed sentence is bounded too",
		step:       "the run must never deploy anything; it only edits files in the candidate worktree",
		wantResult: ConsequenceBounded,
		wantRoute:  RouteArchitectural,
	}, {
		// 40. The required behaviour, stated bare: "push to main" is a
		// consequence signal. Row 1 wraps it in a sentence; this is the
		// operation with no context at all, which is what every other row is a
		// deviation from.
		name:       "the bare operation is a declaration",
		step:       "push to main",
		wantResult: ConsequenceUnacceptable,
		wantRoute:  RouteHuman,
		says:       "outside the worktree",
	}, {
		// 41. And with an explicit subject and auxiliary, which is the minimal
		// affirmative declaration: "the plan will push to main".
		name:       "an explicit affirmative declaration escalates",
		step:       "the plan will push to main",
		wantResult: ConsequenceUnacceptable,
		wantRoute:  RouteHuman,
		says:       "outside the worktree",
	}, {
		// 42. Its minimal pair, one word apart: "never push to main". Rows 40,
		// 41 and 42 are the task's required behaviour in full, and the only
		// thing separating the last from the first two is that word.
		name:       "the bare prohibition is not a declaration",
		step:       "never push to main",
		wantResult: ConsequenceBounded,
		wantRoute:  RouteArchitectural,
	}, {
		// 43. Row 33's shape where the subject is a MULTI-WORD operation whose
		// first token is not the one being classified: "to production" is an
		// outward phrase in its own right and it sits after "deploy" rather
		// than at the front of the clause. Both occurrences have to be read as
		// the subject of the same negated predicate, or the phrase escalates
		// while the verb does not.
		name:       "a postposed prohibition governs a multi-word operation",
		step:       "deploy to production is never permitted",
		wantResult: ConsequenceBounded,
		wantRoute:  RouteArchitectural,
	}, {
		name:       "an unrelated earlier negator does not cancel cannot avoid",
		step:       "the review is not skipped and the run cannot avoid a deploy to production",
		wantResult: ConsequenceUnacceptable,
		wantRoute:  RouteHuman,
		says:       "outside the worktree",
	}, {
		name:       "a quoted action required not to be skipped is asserted",
		step:       `the final step "push to main" must not be skipped`,
		wantResult: ConsequenceUnacceptable,
		wantRoute:  RouteHuman,
		says:       "outside the worktree",
	}, {
		name:       "fronted negative adverbial prohibits the action",
		step:       "under no circumstances should the plan push to main",
		wantResult: ConsequenceBounded,
		wantRoute:  RouteArchitectural,
	}, {
		name:       "fronted negative adverbial affirmative minimal pair escalates",
		step:       "under all circumstances should the plan push to main",
		wantResult: ConsequenceUnacceptable,
		wantRoute:  RouteHuman,
		says:       "outside the worktree",
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

// A QUOTATION IS NOT SUPPRESSION.
//
// An earlier repair read a quotation introduced by a citing word as a MENTION
// and blanked the span. Two faults, and this proves both are gone.
//
// It could not survive its own counterexample. A plan that quotes an operation
// and then performs it -- `the runbook says "push to main" and the run does
// exactly that` -- has exactly one hazardous token, inside the quotation, so
// blanking it handed the authority router a bounded result for a run that had
// just said it would publish. That is the end-to-end row 29; here it is at the
// classifier.
//
// And the blanking had UNBOUNDED REACH: an unbalanced delimiter in one step
// paired with a delimiter several steps later and erased every declaration in
// between, with no negation and no citation anywhere near them. Backticks make
// that ordinary rather than exotic -- plans are markdown-flavoured prose, and a
// lone backtick around a path after a word like "says" is a normal thing to
// write.
//
// So quotation marks are punctuation and nothing more. The last case is what
// that costs and what it does not: a quoted PROHIBITION is still bounded,
// because the prohibition inside it is read exactly as it would be read
// unquoted.
func TestAQuotationIsNotSuppression(t *testing.T) {
	for _, tc := range []struct {
		name  string
		steps []string
		// want is the exact reading. Asserting only that something was found
		// would pass on a regression that blanked the later steps, because the
		// step holding the delimiter can contribute a token of its own.
		want []string
	}{{
		name:  "a quoted operation the next clause performs is a declaration",
		steps: []string{`the runbook says "push to main" and the run does exactly that`},
		want:  []string{"push to"},
	}, {
		name:  "a quoted operation with no prohibition in it is a declaration",
		steps: []string{`the objective says "push to main" is the rule here`},
		want:  []string{"push to"},
	}, {
		name:  "a quoted prohibition is still bounded",
		steps: []string{`the objective says "never push to main" and this run obeys it`},
		want:  nil,
	}, {
		name:  "an unbalanced quotation does not blank the steps after it",
		steps: []string{`the objective says "push to main`, "deploy to production", `is refused"`},
		want:  []string{"deploy", "push to", "to production"},
	}, {
		// "publish" in the last step is read too, and that is the vocabulary
		// working as it always has: "." is not a word byte, so "publish.go" is
		// a word-boundary match. It is listed here rather than written around,
		// because an expected reading that quietly omits a token is not one.
		name:  "an unbalanced backtick does not blank the steps after it",
		steps: []string{"the rule says `git push", "then deploy to production", "see publish.go`"},
		want:  []string{"deploy", "publish", "git push", "to production"},
	}, {
		// And inside ONE step, across its own lines. A step is prose and
		// carries its own line breaks, so per-step handling alone would look
		// like it covered this.
		name:  "an unbalanced delimiter does not blank the next line of its own step",
		steps: []string{"the rule says `git push\ndeploy to production is the last step\nsee publish.go`"},
		want:  []string{"deploy", "publish", "git push", "to production"},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			got := declaredOutwardActions(tc.steps, "")
			same := len(got) == len(tc.want)
			for i := range got {
				if same && got[i] != tc.want[i] {
					same = false
				}
			}
			if !same {
				t.Fatalf("read %v from %q, want %v", got, tc.steps, tc.want)
			}
		})
	}
}

func TestOutwardActionIntentContexts(t *testing.T) {
	for _, tc := range []struct {
		name       string
		step       string
		wantResult ConsequenceResult
		wantRoute  Route
		wantEffect string
	}{
		{"affirmative action escalates", "the plan will push to main", ConsequenceUnacceptable, RouteHuman, "push to"},
		{"explicit prohibition is bounded", "never push to main", ConsequenceBounded, RouteArchitectural, ""},
		{"quoted forbidden action is bounded", `the policy describes "push to main" as forbidden behavior`, ConsequenceBounded, RouteArchitectural, ""},
		{"reassurance does not suppress an action", "the plan will push to main and no reviewer is bypassed", ConsequenceUnacceptable, RouteHuman, "push to"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			action := Action{Stage: StageCandidateEdit, Files: []string{"internal/workflow/consequence.go"}, DeclaredSteps: []string{tc.step}}
			if got := AssessConsequences(action); got.Result != tc.wantResult {
				t.Fatalf("AssessConsequences = %s, want %s", got.Result, tc.wantResult)
			} else if tc.wantEffect != "" {
				found := false
				for _, effect := range got.Effects {
					found = found || containsWord(effect, tc.wantEffect)
				}
				if !found {
					t.Fatalf("effects = %v, want an effect naming %q", got.Effects, tc.wantEffect)
				}
			}
			got := routeAuthorityForAction(scopedPreflight(t, okBody(t, nil, "APPROVAL_GATE_NONE")), nil, action)
			if got.Route != tc.wantRoute {
				t.Fatalf("route = %s, want %s", got.Route, tc.wantRoute)
			}
		})
	}
}

// A fronted prohibition governs its own predicate, not everything after it.
//
// Found by an external chunked review of this candidate, conducted under a
// recorded bootstrap exception because the governed review channel returned no
// verdict on a candidate this size. Confirmed by executing the assessor before
// the repair: the first case below came back BOUNDED, suppressing an asserted
// publish, which weakens the Level-3 path rather than over-triggering it.
//
// The boundary is not coordination itself. "merge or push to main" coordinates
// two bare verbs under one subject and one auxiliary, and the prohibition
// reaches both. "merge and the run will push to main" coordinates a fresh
// subject and auxiliary onto it, and the prohibition stops at that hand-over.
// A first repair that ended the scan at any coordination fixed the first case
// and broke the fifth; both are here so neither can be traded for the other.
func TestAFrontedProhibitionStopsAtANewPredicate(t *testing.T) {
	for _, c := range []struct {
		want ConsequenceResult
		step string
	}{
		{ConsequenceUnacceptable, "under no circumstances should the plan merge and the run will push to main"},
		{ConsequenceUnacceptable, "under no circumstances should the plan merge, and the run will publish the release"},
		{ConsequenceUnacceptable, "under no circumstances should the tree be dirty or the runner will deploy"},
		{ConsequenceBounded, "under no circumstances should the plan push to main"},
		{ConsequenceBounded, "under no circumstances should the plan merge or push to main"},
		{ConsequenceUnacceptable, "the run will push to main"},
		{ConsequenceBounded, "never merge or push to main"},
	} {
		got := AssessConsequences(Action{Stage: StageCandidateEdit, DeclaredSteps: []string{c.step}})
		if got.Result != c.want {
			t.Errorf("%q\n got: %v %v\nwant: %v", c.step, got.Result, got.Effects, c.want)
		}
	}
}

// Two negations are not a prohibition, and a coordinated predicate is not a new
// one. Both found by independent review of c62b084.
//
// "push to main is not forbidden" carried a negator AND a negating verb, and
// postposed read the second as further evidence of prohibition instead of
// applying the parity law the rest of the classifier uses. It suppressed an
// asserted publish -- the dangerous direction.
//
// "merge or be allowed to push to main" coordinates a second predicate under
// one subject and one modal, so the prohibition still reaches it. Treating any
// auxiliary after a coordination as a new predicate re-escalated it: the safe
// direction, but it recreates the false-authority tax this work exists to end.
// A predicate is new when a new SUBJECT takes it, not when a verb appears.
func TestNegationParityAndCoordinatedPredicates(t *testing.T) {
	for _, c := range []struct {
		want ConsequenceResult
		step string
	}{
		{ConsequenceBounded, "push to main is forbidden"},
		{ConsequenceUnacceptable, "push to main is not forbidden"},
		{ConsequenceBounded, "push to main is never allowed"},
		{ConsequenceUnacceptable, "push to main is not prohibited"},
		{ConsequenceBounded, "under no circumstances should the plan merge or be allowed to push to main"},
		{ConsequenceBounded, "under no circumstances should the plan merge or push to main"},
		{ConsequenceUnacceptable, "under no circumstances should the plan merge and the run will push to main"},
		{ConsequenceUnacceptable, "under no circumstances should the tree be dirty or the runner will deploy"},
	} {
		got := AssessConsequences(Action{Stage: StageCandidateEdit, DeclaredSteps: []string{c.step}})
		if got.Result != c.want {
			t.Errorf("%q\n got: %v %v\nwant: %v", c.step, got.Result, got.Effects, c.want)
		}
	}
}
