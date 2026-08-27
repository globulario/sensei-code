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
	bound, err := e.restorePlanBound(found[0])
	if err != nil || bound.Source != PlanSupplied || e.planSource("t") != PlanSupplied {
		t.Fatalf("a supplied plan was not re-established on resume: %v %+v", err, bound)
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
		if _, err := (&Engine{}).restorePlanBound(bad); err == nil {
			t.Errorf("%s: resumed anyway", name)
		}
	}
	if _, err := (&Engine{}).restorePlanBound(session.Interrupted{TaskID: "x", PlanSource: "supplied", PlanDigest: p.Digest}); !errors.Is(err, errSuppliedPlanContextUnavailable) {
		t.Fatalf("wrong refusal: %v", err)
	}
	// A record with no source is the architect's only when the architect
	// emitted it; that fact is recorded beside the payload, not inside it.
	if b, err := (&Engine{}).restorePlanBound(session.Interrupted{TaskID: "old", Plan: "summary only", PlanEventSource: event.SourceArchitect}); err != nil || b.Source != PlanByArchitect || b.Plan != "summary only" {
		t.Fatalf("an old architect record is not resumed as its summary: %v %+v", err, b)
	}
	if _, err := (&Engine{}).restorePlanBound(session.Interrupted{TaskID: "old", Plan: "summary only"}); !errors.Is(err, errSuppliedPlanContextUnavailable) {
		t.Fatalf("absence of a source alone was read as the architect's: %v", err)
	}
}

// The sharp case: a valid supplied record that lost only plan_source must be
// refused, and must not become the architect's.
func TestASuppliedRecordThatLostItsSourceIsNotTheArchitects(t *testing.T) {
	p, _ := ParseSuppliedPlan([]byte(goodPlan))
	full, _ := json.Marshal(proposedPlan{architectureDecision: p.decision, PlanSource: PlanSupplied, PlanDigest: p.Digest})
	var m map[string]json.RawMessage
	json.Unmarshal(full, &m)
	delete(m, "plan_source")
	stripped, _ := json.Marshal(m)
	found := session.FindInterrupted([]event.Event{
		{TaskID: "t", Kind: event.TaskCreated, Summary: "task"},
		{TaskID: "t", Kind: event.PlanProposed, Source: planEventSource(PlanSupplied), Summary: "plan", Payload: stripped},
	})
	if len(found) != 1 || found[0].PlanSource != "" || found[0].PlanDigest != p.Digest {
		t.Fatalf("precondition: the record lost only plan_source: %+v", found)
	}
	e := &Engine{}
	b, err := e.restorePlanBound(found[0])
	if !errors.Is(err, errSuppliedPlanContextUnavailable) {
		t.Fatalf("resumed as %+v (%v); must refuse", b, err)
	}
	if e.planSource("t") == PlanSupplied {
		t.Fatal("a refused resume still registered a supplied plan")
	}
	if b.Source == PlanByArchitect {
		t.Fatal("a supplied record became the architect's by losing a field")
	}
}

// A resumed task continues under the exact plan text that was routed, not the
// rendered summary -- which, when a plan has steps, omits the plan entirely.
func TestAResumedSuppliedPlanContinuesUnderTheExactBound(t *testing.T) {
	const raw = `{"decision":"proceed","summary":"implement prospective authority","plan":"THE GOVERNING BOUND: only main_test.go may be created; no dependency outside testing","steps":["modify A","test B"],"mode":"inspect","consequences":"c","related_invariants":["inv.1"]}`
	p, err := ParseSuppliedPlan([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	summary := planSummaryFrom(p.decision, PlanSupplied, p.Digest)
	if strings.Contains(summary, "THE GOVERNING BOUND") {
		t.Fatal("precondition: the summary must be the lossy rendering this test guards against")
	}
	record, _ := json.Marshal(proposedPlan{architectureDecision: p.decision, PlanSource: PlanSupplied, PlanDigest: p.Digest})
	found := session.FindInterrupted([]event.Event{
		{TaskID: "t", Kind: event.TaskCreated, Summary: "task"},
		{TaskID: "t", Kind: event.PlanProposed, Summary: summary, Payload: record},
	})
	bound, err := (&Engine{}).restorePlanBound(found[0])
	if err != nil {
		t.Fatal(err)
	}
	if bound.Plan != p.decision.Plan {
		t.Fatalf("resumed under %q, not the routed plan", bound.Plan)
	}
	if bound.Rationale != "implement prospective authority" || len(bound.Steps) != 2 || bound.Mode != ModeInspect ||
		bound.Consequences != "c" || len(bound.Invariants) != 1 {
		t.Fatalf("the bound's semantic fields were not restored: %+v", bound)
	}
	// And what the reviewer is then bound to is that exact plan.
	tc := taskContext{Task: "task", Rationale: bound.Rationale, Steps: bound.Steps, Mode: bound.Mode, PlanSource: bound.Source, PlanDigest: p.Digest}
	pkt := reviewPacket(tc, roles.Binding{TaskID: "t"}, certifiedStart{}, bound.Plan, "d", "a", "e")
	if pkt.Plan != p.decision.Plan || pkt.PlanSource != string(PlanSupplied) {
		t.Fatalf("the reviewer packet does not carry the exact supplied bound: %+v", pkt)
	}
}

// The durable decision record names who actually decided and what a human
// actually granted. A supplied plan was decided by nobody in the run, and an
// unattended submission was granted by nobody a person is established to be.
func TestASuppliedPlanDecisionIsNotRecordedAsTheArchitects(t *testing.T) {
	e := &Engine{}
	e.Config.Architect.Name = "chatgpt"
	p, _ := ParseSuppliedPlan([]byte(goodPlan))
	e.supplyPlan("t", p)
	e.recordObjective("t", Objective{Text: "task", Provenance: SubmittedWithSuppliedPlan})
	got := e.decisionAuthority("t", certifiedStart{})
	if strings.Contains(got.DecidedBy, "ChatGPT") || !strings.Contains(got.DecidedBy, p.Digest) {
		t.Fatalf("the record attributes a supplied plan to the architect: %+v", got)
	}
	if strings.Contains(got.HumanGrant, "/run") || !strings.Contains(got.HumanGrant, "none established") {
		t.Fatalf("the record claims a human grant nobody gave: %+v", got)
	}
	// And an unattended architect run is not granted by a person either.
	a := &Engine{}
	a.Config.Architect.Name = "chatgpt"
	a.recordObjective("u", Objective{Text: "task", Provenance: SubmittedUnattended})
	if g := a.decisionAuthority("u", certifiedStart{}); strings.Contains(g.HumanGrant, "/run") || !strings.Contains(g.DecidedBy, "ChatGPT") {
		t.Fatalf("an unattended run is recorded as a /run grant: %+v", g)
	}
}
