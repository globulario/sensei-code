package workflow

// A plan handed in through `run --plan` is judged by every gate exactly as an
// architect's plan is, and is never recorded as the architect's.

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/session"
)

const goodPlan = `{"decision":"proceed","summary":"add a regression test","plan":"create gosumcheck/main_test.go","files":["gosumcheck/main_test.go"],"steps":["write the test"],"mode":"modify"}`

func TestASuppliedPlanIsValidatedAndDigested(t *testing.T) {
	p, err := ParseSuppliedPlan([]byte(goodPlan))
	if err != nil {
		t.Fatal(err)
	}
	if p.decision.Plan != "create gosumcheck/main_test.go" || p.decision.Mode != "modify" {
		t.Fatalf("plan body was not carried: %+v", p.decision)
	}
	again, _ := ParseSuppliedPlan([]byte(goodPlan))
	if p.Digest == "" || p.Digest != again.Digest {
		t.Fatal("the digest must identify the exact bytes, and identically each time")
	}
	other, _ := ParseSuppliedPlan([]byte(goodPlan + "\n"))
	if other.Digest == p.Digest {
		t.Fatal("different bytes must not share a digest")
	}
}

// Every way a supplied plan could claim more than a bound, or less than one,
// is refused before a task exists.
func TestASuppliedPlanFailsClosed(t *testing.T) {
	cases := map[string]string{
		"empty":                       ``,
		"not json":                    `plan: do it`,
		"self-asserted provenance":    `{"decision":"proceed","plan":"x","plan_source":"architect"}`,
		"unknown field":               `{"decision":"proceed","plan":"x","authority":"granted"}`,
		"reply is a conversation":     `{"decision":"reply","message":"hello","plan":"x"}`,
		"escalate needs an architect": `{"decision":"escalate","plan":"x"}`,
		"no decision":                 `{"plan":"x"}`,
		"no plan text":                `{"decision":"proceed","summary":"s"}`,
		"invalid mode":                `{"decision":"proceed","plan":"x","mode":"observe"}`,
		"plan asks the human itself":  `{"decision":"proceed","plan":"x","human_question":"may I?"}`,
		"two objects":                 `{"decision":"proceed","plan":"x"} {"decision":"proceed","plan":"y"}`,
	}
	for name, raw := range cases {
		if _, err := ParseSuppliedPlan([]byte(raw)); err == nil {
			t.Errorf("%s: accepted %q", name, raw)
		}
	}
}

// The regression the lane exists to hold: identical plan bodies from the two
// sources produce identical bounds for the reviewer, distinguishable only by
// the provenance the engine stamped.
func TestIdenticalPlanBodiesProduceIdenticalBoundsWithDistinctProvenance(t *testing.T) {
	p, err := ParseSuppliedPlan([]byte(goodPlan))
	if err != nil {
		t.Fatal(err)
	}
	fromArchitect := p.decision // the architect returned exactly this body
	base := taskContext{Task: "add a test", Conversation: "c", WorkspaceStatus: "w", Preflight: "pf",
		Rationale: fromArchitect.Summary, Steps: fromArchitect.Steps, Mode: planMode(fromArchitect.Mode)}
	architect, supplied := base, base
	architect.PlanSource = PlanByArchitect
	supplied.PlanSource, supplied.PlanDigest = PlanSupplied, p.Digest

	binding := roles.Binding{TaskID: "t", BaseSHA: "b", CandidateDigest: "d"}
	a := reviewPacket(architect, binding, certifiedStart{}, fromArchitect.Plan, "diff", "audit", "ev")
	s := reviewPacket(supplied, binding, certifiedStart{}, p.decision.Plan, "diff", "audit", "ev")
	if a.PlanSource != string(PlanByArchitect) || s.PlanSource != string(PlanSupplied) || s.PlanDigest != p.Digest {
		t.Fatalf("provenance not stamped: architect=%q supplied=%q/%q", a.PlanSource, s.PlanSource, s.PlanDigest)
	}
	// Everything that bounds the review is identical.
	s.PlanSource, s.PlanDigest, s.Provenance.At = a.PlanSource, a.PlanDigest, a.Provenance.At
	if a != s {
		t.Fatalf("the bound differs by more than provenance:\n%+v\n%+v", a, s)
	}

	ap := reviewPrompt(reviewPacket(architect, binding, certifiedStart{}, fromArchitect.Plan, "diff", "audit", "ev"))
	sp := reviewPrompt(reviewPacket(supplied, binding, certifiedStart{}, p.decision.Plan, "diff", "audit", "ev"))
	if !strings.Contains(sp, "not architect-produced; sha256 "+p.Digest) {
		t.Fatal("the reviewer is not told the plan was supplied")
	}
	if strings.Contains(ap, "not architect-produced") {
		t.Fatal("an architect's plan is labelled as supplied")
	}
	if strings.ReplaceAll(sp, planSourceLabel(s2(supplied, p.Digest)), "") != ap {
		t.Fatal("the review prompt differs by more than the source label")
	}

	ip := reviewPrompt(inspectionPacket(supplied, binding, certifiedStart{}, p.decision.Plan, "report"))
	if !strings.Contains(ip, "not architect-produced") {
		t.Fatal("the inspection reviewer is not told the plan was supplied")
	}
}

func s2(tc taskContext, digest string) roles.IndependentReviewPacket {
	return roles.IndependentReviewPacket{PlanSource: string(tc.PlanSource), PlanDigest: digest}
}

// The architect is never asked to revise a supplied plan: the escalation paths
// end the run instead of putting two authors under one label.
func TestASuppliedPlanIsNeverRevisedByTheArchitect(t *testing.T) {
	e := &Engine{}
	p, _ := ParseSuppliedPlan([]byte(goodPlan))
	e.supplyPlan("t1", p)
	if _, err := e.resolveArchitectureForRevision(nil, nil, certifiedStart{}, "t1", "task", "prompt", "the reviewer escalated"); err == nil || !strings.Contains(err.Error(), "supply a revised plan") {
		t.Fatalf("a supplied plan was handed to the architect for revision: %v", err)
	}
	if e.planSource("t1") != PlanSupplied || e.planSource("t2") != PlanByArchitect {
		t.Fatal("plan source is not derived from how the task entered")
	}
}

// The announced plan carries its source in the text, because that text is what
// a resumed task will hold as its plan.
func TestASuppliedPlanAnnouncesItselfAsSupplied(t *testing.T) {
	p, _ := ParseSuppliedPlan([]byte(goodPlan))
	s := planSummaryFrom(p.decision, PlanSupplied, p.Digest)
	if !strings.HasPrefix(s, "Supplied plan (not architect-produced; sha256 "+p.Digest+")") {
		t.Fatalf("summary does not state the source first:\n%s", s)
	}
	if planSummaryFrom(p.decision, PlanByArchitect, "") != planSummary(p.decision) {
		t.Fatal("an architect's summary changed")
	}
	if planEventSource(PlanSupplied) == planEventSource(PlanByArchitect) {
		t.Fatal("a supplied plan is attributed to the architect")
	}
}

// Supplying a plan establishes nothing about a human being present.
func TestASuppliedPlanDoesNotEstablishHumanAuthority(t *testing.T) {
	if (Objective{Text: "x", Provenance: SubmittedWithSuppliedPlan}).HumanAuthorized() {
		t.Fatal("a file on disk was read as a person")
	}
}

// A restart must not change who may author the governing plan. The source is
// re-established from the session record, and a supplied plan the record does
// not hold intact is not resumed under the architect instead.
func TestASuppliedPlanSurvivesResumeOrFailsClosed(t *testing.T) {
	p, _ := ParseSuppliedPlan([]byte(goodPlan))
	record, _ := json.Marshal(proposedPlan{architectureDecision: p.decision, PlanSource: PlanSupplied, PlanDigest: p.Digest})

	// The record as the engine wrote it is readable by the session store.
	found := session.FindInterrupted([]event.Event{
		{TaskID: "t", Kind: event.TaskCreated, Summary: "task"},
		{TaskID: "t", Kind: event.PlanProposed, Summary: "plan", Payload: record},
	})
	if len(found) != 1 || found[0].PlanSource != string(PlanSupplied) || found[0].PlanDigest != p.Digest {
		t.Fatalf("the session record does not carry the plan source: %+v", found)
	}

	e := &Engine{}
	src, err := e.restorePlanSource(found[0])
	if err != nil || src != PlanSupplied || e.planSource("t") != PlanSupplied {
		t.Fatalf("a supplied plan was not re-established on resume: %v %q", err, src)
	}
	if _, err := e.resolveArchitectureForRevision(nil, nil, certifiedStart{}, "t", "task", "prompt", "escalated"); err == nil {
		t.Fatal("after a resume the architect may revise the supplied plan")
	}
	restored, _ := e.suppliedPlan("t")
	if restored.decision.Plan != p.decision.Plan || restored.Digest != p.Digest {
		t.Fatal("the resumed bound is not the supplied one")
	}

	// Missing, corrupt, or digest-mismatched records fail closed.
	for name, bad := range map[string]session.Interrupted{
		"no record":       {TaskID: "x", PlanSource: "supplied", PlanDigest: p.Digest},
		"digest mismatch": {TaskID: "x", PlanSource: "supplied", PlanDigest: "other", PlanRecord: record},
		"corrupt":         {TaskID: "x", PlanSource: "supplied", PlanDigest: p.Digest, PlanRecord: []byte(`{"plan_source":"supplied"`)},
		"unknown source":  {TaskID: "x", PlanSource: "oracle"},
	} {
		if _, err := (&Engine{}).restorePlanSource(bad); err == nil {
			t.Errorf("%s: resumed anyway", name)
		}
	}
	if _, err := (&Engine{}).restorePlanSource(session.Interrupted{TaskID: "x", PlanSource: "supplied", PlanDigest: p.Digest}); !errors.Is(err, errSuppliedPlanContextUnavailable) {
		t.Fatalf("wrong refusal: %v", err)
	}
	// A record with no source predates the field: the architect's.
	if src, err := (&Engine{}).restorePlanSource(session.Interrupted{TaskID: "old"}); err != nil || src != PlanByArchitect {
		t.Fatalf("an old record is not the architect's: %v %q", err, src)
	}
}
