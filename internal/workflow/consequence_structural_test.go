package workflow

// Consequence assessment is a property of the ACTION.
//
// A live self-audit found that AssessConsequences was reached only from inside
// the consequence-blind-spot branch: whether an action's consequences were
// assessed at all was decided by metadata about the graph's coverage of the
// region. Ordinary candidate edits looked correct anyway, because their stage
// happens to produce the route the fall-through produced — a right answer
// resting on the wrong dependency.
//
// Blind spots may inform the assessment. They must not own the control-flow
// edge that makes the assessment exist.

import (
	"encoding/json"
	"strings"
	"testing"
)

// okBody renders an OK preflight with the given blind spots and approval gate.
func okBody(t *testing.T, spots []string, gate string) string {
	t.Helper()
	b, err := json.Marshal(spots)
	if err != nil {
		t.Fatal(err)
	}
	return `{
		"status": "PREFLIGHT_STATUS_OK",
		"blind_spots": ` + string(b) + `,
		"coverage": {"sufficient": true, "direct_anchor_count": 2, "indexed_file_count": 1, "file_count": 1},
		"change_risk": {"blast_radius":"BLAST_RADIUS_LOCAL","approval_gate":"` + gate + `"},
		` + healthyAuthority + `
	}`
}

// The required attacks, in the brief's own order.
func TestConsequencesAreAssessedForEveryRoutedAction(t *testing.T) {
	const none = "APPROVAL_GATE_NONE"
	const human = "APPROVAL_GATE_HUMAN_APPROVAL_REQUIRED"

	for _, tc := range []struct {
		name   string
		spots  []string
		gate   string
		action Action
		want   Route
		says   string
	}{{
		// The happy path has to keep working, or "assess everything" is just a
		// way of refusing everything.
		name: "candidate edit, no blind spots, bounded", gate: none,
		action: Action{Stage: StageCandidateEdit, Files: []string{"internal/tui/app.go"}},
		want:   RouteArchitectural,
	}, {
		// The defect, stated as a case. With no blind spot to send it there,
		// nothing used to read this action's stage at all, and a publish
		// reached a grant.
		name: "publish, no blind spots", gate: none,
		action: Action{Stage: StagePublish, Files: []string{"internal/tui/app.go"}},
		want:   RouteHuman, says: "observable outside the repository",
	}, {
		// Deployment-shaped work arrives as a declared step rather than as a
		// stage. It must still be assessed, and a declaration may only escalate.
		name: "declared migration, no blind spots", gate: none,
		action: Action{Stage: StageCandidateEdit, Files: []string{"internal/tui/app.go"},
			DeclaredSteps: []string{"write the schema change", "then run the migration against production"}},
		want: RouteHuman, says: "outside the worktree",
	}, {
		// Ignorance, not risk: nobody knows what this action is, and asking a
		// human to adjudicate it would be asking about something no one has
		// evidence for.
		name: "unclassified stage, no blind spots", gate: none,
		action: unclassifiedAction(),
		want:   RouteCannotEstablish, says: "could not be established",
	}, {
		// An explicit gate outranks a bounded assessment. A bounded action is
		// permission to continue in the technical lane, never permission to
		// walk past a verdict about who decides.
		name: "explicit approval gate over a bounded action", gate: human,
		action: Action{Stage: StageCandidateEdit, Files: []string{"internal/tui/app.go"}},
		want:   RouteHuman, says: "requires approval for this change class",
	}, {
		// And a consequence signal may escalate an unbounded action while not
		// being what caused it to be assessed.
		name: "consequence signal over a publish", spots: []string{"anchor with severity=critical"}, gate: none,
		action: Action{Stage: StagePublish, Files: []string{"internal/tui/app.go"}},
		want:   RouteHuman, says: "anchor with severity=critical",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			got := routeAuthorityForAction(scopedPreflight(t, okBody(t, tc.spots, tc.gate)), nil, tc.action)
			if got.Route != tc.want {
				t.Fatalf("route = %s (%s), want %s", got.Route, got.Condition, tc.want)
			}
			if tc.says != "" && !strings.Contains(got.Condition, tc.says) {
				t.Fatalf("condition does not say why: %q", got.Condition)
			}
		})
	}
}

// The same action must reach the same consequence result with the blind-spot
// list removed.
//
// This is the assertion the defect would fail. Blind spots decided whether the
// assessment RAN, so stripping an unrelated one silently deleted the
// consequence judgement from the route.
func TestRemovingABlindSpotDoesNotRemoveTheAssessment(t *testing.T) {
	for _, action := range []Action{
		{Stage: StagePublish, Files: []string{"internal/tui/app.go"}},
		{Stage: StageCandidateEdit, Files: []string{"internal/tui/app.go"},
			DeclaredSteps: []string{"git push the branch"}},
		unclassifiedAction(),
		{Stage: StageCandidateEdit, Files: []string{"internal/tui/app.go"}},
	} {
		// An unrelated blind spot: it is a consequence signal about the region,
		// and it says nothing about what this action does.
		with := routeAuthorityForAction(scopedPreflight(t,
			okBody(t, []string{"anchor with severity=critical"}, "APPROVAL_GATE_NONE")), nil, action)
		without := routeAuthorityForAction(scopedPreflight(t,
			okBody(t, nil, "APPROVAL_GATE_NONE")), nil, action)

		if with.Route != without.Route {
			t.Errorf("stage %q: route %s with a blind spot, %s without it; the blind-spot list "+
				"is deciding whether this action's consequences are judged",
				action.Stage, with.Route, without.Route)
		}
	}
}

// The live specimen, with its blind spots taken away.
//
// The brief asks for the governed edit-stage specimen replayed with blind spots
// removed while stage and approval data are preserved. Every one of the 135
// probed files is replayed twice: as an ordinary candidate edit, which must
// stay bounded wherever it was, and as a publish over the same file, which must
// escalate every time whether or not the graph published a blind spot about it.
func TestTheLiveSpecimenIsAssessedWithoutItsBlindSpots(t *testing.T) {
	rows := loadProbe(t)
	stripped := 0
	for _, r := range rows {
		// A DEGRADED row is excluded, and the exclusion is the point rather
		// than a convenience. That reading is about the INSTRUMENT -- whether
		// the surface is degraded for a coverage reason or for an unstated one
		// -- and its only evidence is the blind-spot list. Stripping the list
		// destroys the row's meaning instead of isolating the action's. The
		// dependency this test exists to forbid is the other kind: blind spots
		// deciding whether an ACTION's consequences are judged.
		if r.Status == "PREFLIGHT_STATUS_DEGRADED" {
			continue
		}
		bare := r
		bare.BlindSpots = nil
		if len(r.BlindSpots) != 0 {
			stripped++
		}

		edit := Action{Stage: StageCandidateEdit, Files: []string{r.File}}
		// A bounded edit must not become unassessable by losing metadata.
		if got := routeAuthorityForAction(scopedPreflight(t, probeBody(bare)), nil, edit); got.Route == RouteCannotEstablish &&
			replayOne(t, r).Route != RouteCannotEstablish {
			t.Fatalf("%s: removing the blind spots made a routable edit unassessable: %s", r.File, got.Condition)
		}

		publish := Action{Stage: StagePublish, Files: []string{r.File}}
		if got := routeAuthorityForAction(scopedPreflight(t, probeBody(bare)), nil, publish); got.Route != RouteHuman {
			t.Fatalf("%s: a publish with no blind spots routed to %s (%s); an outward action must "+
				"escalate on its own stage", r.File, got.Route, got.Condition)
		}
	}
	if stripped == 0 {
		t.Fatal("no probed file carried a blind spot, so this replay removed nothing and proves nothing")
	}
	t.Logf("replayed %d files, %d of which carried blind spots that were stripped", len(rows), stripped)
}

// The control-flow regression: the assessment must not go back under a branch.
//
// The behavioural tests above would all still pass if AssessConsequences were
// called twice — once where it belongs and once inside the blind-spot branch —
// and the second call is how the dependency creeps back. So the structure is
// pinned directly: the assessment is established before the blind-spot list is
// ever read, and it is established exactly once.
func TestTheAssessmentIsNotReachedThroughABlindSpotBranch(t *testing.T) {
	body := routerBody(t)

	assess := strings.Index(body, "AssessConsequences(action)")
	if assess < 0 {
		t.Fatal("decideRouteForAction no longer assesses the action's consequences at all")
	}
	if n := strings.Count(body, "AssessConsequences("); n != 1 {
		t.Fatalf("the action's consequences are assessed %d times; a second call is how the "+
			"blind-spot dependency comes back", n)
	}
	branch := strings.Index(body, "if len(scoped.BlindSpots) != 0 {")
	if branch < 0 {
		t.Fatal("the blind-spot branch is gone; this pin needs rewriting against whatever replaced it")
	}
	if assess > branch {
		t.Fatal("the consequence assessment happens inside or after the blind-spot branch, so " +
			"whether an action is assessed at all depends on the graph's coverage metadata")
	}
	// And the gate still runs first, so a bounded assessment cannot clear it.
	gate := strings.Index(body, "Sensei requires approval for this change class")
	if gate < 0 || gate > assess {
		t.Fatal("the explicit approval gate no longer precedes the consequence assessment; a " +
			"bounded action must never be able to clear a gate")
	}
}

// routerBody returns decideRouteForAction's source verbatim.
//
// Verbatim, because this pins ORDER and the strings that mark each step, and
// the shared funcBody helper renders identifiers rather than source.
func routerBody(t *testing.T) string {
	t.Helper()
	src := rawSource(t, "internal/workflow/authority.go")
	at := strings.Index(src, "func decideRouteForAction(")
	if at < 0 {
		t.Fatal("decideRouteForAction is gone from authority.go")
	}
	rest := src[at:]
	if end := strings.Index(rest, "\n}\n"); end > 0 {
		rest = rest[:end]
	}
	return rest
}

// There is no way to route without stating the action.
//
// The action-less wrapper was where Action{} came from: a test that was not
// "about" the action left it out, and unspecified silently reached a grant.
func TestThereIsNoActionlessRouting(t *testing.T) {
	src := rawSource(t, "internal/workflow/authority.go")
	for _, gone := range []string{
		"func routeAuthority(scoped sensei.PreflightDecision, claims []Claim) Routing",
		"func decideRoute(scoped sensei.PreflightDecision, claims []Claim) Routing",
	} {
		if strings.Contains(src, gone) {
			t.Errorf("%s is back; an unspecified action is not a neutral one", gone)
		}
	}
}
