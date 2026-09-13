package workflow

// A retried actor must not be able to acquire a fresh retry budget by changing
// its plan.
//
// Observed live on task-1789272620293170079 (2026-09-13): one substantive
// coverage-unexamined gap consumed SIX closure rounds under closureBudget = 1.
// The architect replanned 7 -> 5 -> 7 -> 0 -> 7 -> 6 files; each scope change
// altered GapIdentity.Key(); premiseReceiptFor then failed to find the opening
// receipt and issued a new one; and spendClosure, correctly keyed on
// premiseReceipt.ID, received a fresh budget each time.
//
// The law is already stated in premise.go, about PremiseResolution: "authored by
// the architect but keyed by an engine-issued ID, so what the model chooses is
// the outcome, not the identity." It holds for the field and failed for the
// lookup: the model cannot write the identity, but it chose the plan, and the
// plan selected the identity.
//
// These witnesses assert the BUDGET consequence, not key equality. A test that
// compared keys would pass against a repair that fixed the comparison and left
// the budget reachable another way.

import (
	"testing"
)

const episodeWorld = "e7d3fede98ff88b89904b096a40b363adc9c5667"

// closureEpisode drives one closure round the way the engine does: select the
// receipt for this routing, spend the budget against its ID, then let the round
// answer (silence reads as unresolved, which is what a round that closed nothing
// records).
func closureEpisode(t *testing.T, e *Engine, taskID, kind, subject string, scope []string) (id string, budgetGranted bool) {
	t.Helper()
	r := e.premiseReceiptFor(taskID, Routing{
		Condition: "graph coverage is absent for planned file(s) the graph has not examined",
		Gap:       GapIdentity{Kind: kind, Subject: subject, Scope: scope, World: episodeWorld},
	}, "")
	granted := e.spendClosure(taskID, r.ID)
	e.applyPremiseResolutions(taskID, []PremiseResolution{{Gap: r.ID, Outcome: "unresolved"}})
	return r.ID, granted
}

// A — narrowing the plan must not mint a new episode.
func TestNarrowingThePlanDoesNotBuyASecondClosureRound(t *testing.T) {
	e := &Engine{}
	opening, granted := closureEpisode(t, e, "task-A", "coverage-unexamined", "", []string{"A", "B", "C"})
	if !granted {
		t.Fatal("the opening closure round was refused its one allowed budget")
	}
	narrowed, granted := closureEpisode(t, e, "task-A", "coverage-unexamined", "", []string{"A", "B"})
	if narrowed != opening {
		t.Errorf("narrowing the plan minted a new closure episode: %s -> %s", opening, narrowed)
	}
	if granted {
		t.Fatalf("a second closure round was granted after narrowing; closureBudget = %d is not binding", closureBudget)
	}
}

// B — overlapping-but-shifted scope is the same episode.
func TestAnOverlappingReplanDoesNotBuyASecondClosureRound(t *testing.T) {
	e := &Engine{}
	opening, _ := closureEpisode(t, e, "task-B", "coverage-unexamined", "", []string{"A", "B", "C"})
	shifted, granted := closureEpisode(t, e, "task-B", "coverage-unexamined", "", []string{"B", "C", "D"})
	if shifted != opening {
		t.Errorf("an overlapping replan minted a new closure episode: %s -> %s", opening, shifted)
	}
	if granted {
		t.Fatal("an overlapping replan was granted a fresh closure budget")
	}
}

// C — a plan that names no files at all must not escape the episode. This is the
// 14:32:21 observation: coverage collapsed to 0 anchors over 0 planned files.
func TestAnEmptyReplanDoesNotEscapeTheClosureEpisode(t *testing.T) {
	e := &Engine{}
	opening, _ := closureEpisode(t, e, "task-C", "coverage-unexamined", "", []string{"A", "B", "C"})
	empty, granted := closureEpisode(t, e, "task-C", "coverage-unexamined", "", nil)
	if empty != opening {
		t.Errorf("a zero-file replan minted a new closure episode: %s -> %s", opening, empty)
	}
	if granted {
		t.Fatal("a zero-file replan was granted a fresh closure budget; scope became empty and the budget reset")
	}
}

// D — re-expanding back to the opening scope is still one episode. Observed as
// 7 -> 5 -> 7: the return trip must not be a third round.
func TestReturningToTheOpeningScopeIsNotAThirdEpisode(t *testing.T) {
	e := &Engine{}
	opening, _ := closureEpisode(t, e, "task-D", "coverage-unexamined", "", []string{"A", "B", "C"})
	if _, granted := closureEpisode(t, e, "task-D", "coverage-unexamined", "", []string{"A", "B"}); granted {
		t.Fatal("narrowing bought a round")
	}
	back, granted := closureEpisode(t, e, "task-D", "coverage-unexamined", "", []string{"A", "B", "C"})
	if back != opening {
		t.Errorf("returning to the opening scope minted a new episode: %s -> %s", opening, back)
	}
	if granted {
		t.Fatal("returning to the opening scope was granted a fresh closure budget")
	}
}

// The live trajectory, end to end: one substantive gap, six rounds, one budget.
func TestTheObservedTrajectorySpendsExactlyOneClosureRound(t *testing.T) {
	e := &Engine{}
	seven := []string{"exchange.go", "transport.go", "runner.go", "exchange_test.go", "await_test.go", "runner_test.go", "migration.md"}
	trajectory := [][]string{
		seven,
		{"exchange.go", "transport.go", "runner.go", "exchange_test.go", "await_test.go"},
		seven,
		nil,
		seven,
		{"exchange.go", "transport.go", "runner.go", "exchange_test.go", "await_test.go", "migration.md"},
	}
	var ids []string
	granted := 0
	for _, scope := range trajectory {
		id, ok := closureEpisode(t, e, "task-live", "coverage-unexamined", "", scope)
		ids = append(ids, id)
		if ok {
			granted++
		}
	}
	if granted != 1 {
		t.Fatalf("the observed 7->5->7->0->7->6 trajectory was granted %d closure rounds, want exactly %d", granted, closureBudget)
	}
	for i, id := range ids {
		if id != ids[0] {
			t.Errorf("round %d left the opening episode: %s != %s", i, id, ids[0])
		}
	}
}

// Discrimination must survive: two genuinely unrelated coverage gaps in one task
// are different episodes and each gets its own round. A repair that made
// everything one episode would pass A-D and break this.
func TestUnrelatedGapsRemainDistinctEpisodes(t *testing.T) {
	e := &Engine{}
	first, granted := closureEpisode(t, e, "task-E", "coverage-unexamined", "", []string{"internal/ghbridge/transport.go"})
	if !granted {
		t.Fatal("the first gap was refused its round")
	}
	second, granted := closureEpisode(t, e, "task-E", "coverage-unexamined", "", []string{"internal/report/report.go"})
	if second == first {
		t.Fatalf("two unrelated coverage gaps share one episode (%s); discrimination was lost", first)
	}
	if !granted {
		t.Fatal("an unrelated gap was refused its own closure round")
	}

	// A different KIND over the same files is also a different question.
	third, granted := closureEpisode(t, e, "task-E", "unverified-premise", "", []string{"internal/ghbridge/transport.go"})
	if third == first {
		t.Fatal("a different gap kind over the same files reused the receipt")
	}
	if !granted {
		t.Fatal("a different gap kind was refused its own round")
	}

	// And Subject still discriminates: two premises about one file are two
	// questions, which is what sensei-code#97 established.
	fourth, _ := closureEpisode(t, e, "task-F", "unverified-premise", "premise-one", []string{"x.go"})
	fifth, granted := closureEpisode(t, e, "task-F", "unverified-premise", "premise-two", []string{"x.go"})
	if fifth == fourth {
		t.Fatal("two different premises about one file share a receipt; Subject stopped discriminating")
	}
	if !granted {
		t.Fatal("a second distinct premise was refused its own round")
	}
}

// Item 7 — the repaired law, stated as one sequence: the actor may change the
// plan, and may not thereby change the closure episode identity.
func TestTheActorMayChangeThePlanButNotTheEpisodeIdentity(t *testing.T) {
	e := &Engine{}
	const task = "task-law"
	opening := []string{"a.go", "b.go", "c.go"}

	// Opening receipt R spends its one allowed closure.
	R, granted := closureEpisode(t, e, task, "coverage-unexamined", "", opening)
	if !granted {
		t.Fatal("R was refused its one allowed closure round")
	}

	// Every reshaping the architect can perform still selects R, and every
	// further attempt is refused because R's budget is spent.
	for name, scope := range map[string][]string{
		"narrowed":    {"a.go"},
		"widened":     {"a.go", "b.go", "c.go", "d.go"},
		"shifted":     {"c.go", "d.go"},
		"reordered":   {"c.go", "a.go", "b.go"},
		"duplicated":  {"a.go", "a.go", "b.go"},
		"whitespaced": {" a.go ", "b.go"},
		"empty":       nil,
		"returned":    opening,
	} {
		id, granted := closureEpisode(t, e, task, "coverage-unexamined", "", scope)
		if id != R {
			t.Errorf("%s replan left episode %s for %s", name, R, id)
		}
		if granted {
			t.Errorf("%s replan was granted a closure round; R's budget was already spent", name)
		}
	}
}

// Normalization is load-bearing on its own: when the ONLY thing linking two
// scopes is trimming or de-duplication, dropping it splits the episode and hands
// out a fresh budget. The broader test above masks this, because a second shared
// file rescues the overlap.
func TestNormalisationAloneCanHoldAnEpisodeTogether(t *testing.T) {
	for name, replan := range map[string][]string{
		"only file differs by whitespace": {" a.go "},
		"only file repeated":              {"a.go", "a.go"},
		"whitespace and blanks":           {"", "  ", "\ta.go"},
	} {
		e := &Engine{}
		opening, granted := closureEpisode(t, e, "task-"+name, "coverage-unexamined", "", []string{"a.go"})
		if !granted {
			t.Fatalf("%s: the opening round was refused", name)
		}
		again, granted := closureEpisode(t, e, "task-"+name, "coverage-unexamined", "", replan)
		if again != opening {
			t.Errorf("%s: %v left episode %s for %s", name, replan, opening, again)
		}
		if granted {
			t.Errorf("%s: %v was granted a fresh closure budget", name, replan)
		}
	}
}

// Each continuation reason is asserted by name, so a repair that collapsed them
// into one boolean -- or that described a degenerate replan as overlap -- is
// detectable.
func TestEachEpisodeContinuationReasonIsDistinct(t *testing.T) {
	open3 := &premiseReceipt{Gap: GapIdentity{Kind: "coverage-unexamined", World: episodeWorld, Scope: []string{"a.go", "b.go", "c.go"}}}
	gap := func(scope []string, kind, subject string) GapIdentity {
		if kind == "" {
			kind = "coverage-unexamined"
		}
		return GapIdentity{Kind: kind, Subject: subject, Scope: scope, World: episodeWorld}
	}
	for name, tc := range map[string]struct {
		gap  GapIdentity
		want episodeContinuation
	}{
		"identical scope":      {gap([]string{"a.go", "b.go", "c.go"}, "", ""), episodeSameScope},
		"reordered scope":      {gap([]string{"c.go", "a.go", "b.go"}, "", ""), episodeSameScope},
		"narrowed scope":       {gap([]string{"a.go"}, "", ""), episodeOverlapping},
		"widened scope":        {gap([]string{"a.go", "b.go", "c.go", "d.go"}, "", ""), episodeOverlapping},
		"shifted with overlap": {gap([]string{"c.go", "d.go"}, "", ""), episodeOverlapping},
		"no files named":       {gap(nil, "", ""), episodeDegenerate},
		"blank files only":     {gap([]string{"", "   "}, "", ""), episodeDegenerate},
		"disjoint scope":       {gap([]string{"x.go", "y.go"}, "", ""), episodeUnrelated},
		"different kind":       {gap([]string{"a.go"}, "unverified-premise", ""), episodeUnrelated},
		"different subject":    {gap([]string{"a.go"}, "", "some-premise"), episodeUnrelated},
		"different world":      {GapIdentity{Kind: "coverage-unexamined", World: "other", Scope: []string{"a.go"}}, episodeUnrelated},
	} {
		if got := continuesClosureEpisode(open3, tc.gap); got != tc.want {
			t.Errorf("%s: continuation = %d, want %d", name, got, tc.want)
		}
	}

	// A degenerate replan must NOT be reported as overlap even though both
	// continue the episode: collapsing them would let an empty scope continue an
	// episode it shares no files with, which is how unrelated gaps would merge.
	if continuesClosureEpisode(open3, gap(nil, "", "")) == episodeOverlapping {
		t.Error("a zero-file replan is described as overlapping; the rule must name it as degenerate")
	}
	// And an episode that itself opened over no files is not a wildcard for
	// path-disjoint work: only Kind/World/Subject discriminate there.
	openNone := &premiseReceipt{Gap: GapIdentity{Kind: "coverage-unexamined", World: episodeWorld}}
	if continuesClosureEpisode(openNone, gap([]string{"x.go"}, "unverified-premise", "")) != episodeUnrelated {
		t.Error("an episode opened over no files absorbed a different gap kind")
	}
	// An episode that opened over no files continues a same-family replan, and
	// the reason must say SAME SCOPE rather than OVERLAPPING: they share no
	// files, because there are none to share, and calling that overlap is a
	// false description of why the episode continued.
	if got := continuesClosureEpisode(openNone, gap([]string{"x.go"}, "", "")); got != episodeSameScope {
		t.Errorf("episode opened over no files: continuation = %d, want %d (same-scope, not overlap)", got, episodeSameScope)
	}
	if got := continuesClosureEpisode(openNone, gap(nil, "", "")); got != episodeDegenerate {
		t.Errorf("no-file episode with a no-file replan: continuation = %d, want %d", got, episodeDegenerate)
	}
}

// A settled receipt ends its episode: an established or refuted premise is
// answered, and a later gap of the same family is a new question with its own
// round. Only the two ENDING outcomes do this — a receipt no round has answered
// yet must not mint a second receipt.
func TestOnlyAnAnsweredPremiseEndsItsEpisode(t *testing.T) {
	for _, outcome := range []string{premiseEstablished, premiseRefuted} {
		e := &Engine{}
		first, _ := closureEpisode(t, e, "task-"+outcome, "coverage-unexamined", "", []string{"a.go"})
		e.applyPremiseResolutions("task-"+outcome, []PremiseResolution{{Gap: first, Outcome: outcome}})
		second, granted := closureEpisode(t, e, "task-"+outcome, "coverage-unexamined", "", []string{"a.go"})
		if second == first {
			t.Errorf("%s: a settled premise kept its episode open", outcome)
		}
		if !granted {
			t.Errorf("%s: a new question after a settled premise was refused its round", outcome)
		}
	}

	// Unanswered (Outcome "") is NOT an ending: selection must still find it.
	e := &Engine{}
	r := e.premiseReceiptFor("task-unanswered", Routing{
		Condition: "c", Gap: GapIdentity{Kind: "coverage-unexamined", World: episodeWorld, Scope: []string{"a.go"}},
	}, "")
	if !e.spendClosure("task-unanswered", r.ID) {
		t.Fatal("the opening round was refused")
	}
	again := e.premiseReceiptFor("task-unanswered", Routing{
		Condition: "c", Gap: GapIdentity{Kind: "coverage-unexamined", World: episodeWorld, Scope: []string{"a.go"}},
	}, "")
	if again.ID != r.ID {
		t.Errorf("a receipt no round had answered minted a second receipt: %s -> %s", r.ID, again.ID)
	}
	if e.spendClosure("task-unanswered", again.ID) {
		t.Error("an unanswered receipt was granted a second closure round")
	}
}

// The claim reference still wins: a plan that names its receipt continues it
// regardless of scope, which is the pre-existing contract.
func TestAClaimReferenceStillSelectsItsReceipt(t *testing.T) {
	e := &Engine{}
	r, _ := closureEpisode(t, e, "task-ref", "coverage-unexamined", "", []string{"a.go"})
	byRef := e.premiseReceiptFor("task-ref", Routing{
		Condition: "reworded entirely",
		Gap:       GapIdentity{Kind: "unverified-premise", World: "elsewhere", Scope: []string{"zzz.go"}},
	}, r)
	if byRef.ID != r {
		t.Fatalf("a claim naming receipt %s selected %s", r, byRef.ID)
	}
}
