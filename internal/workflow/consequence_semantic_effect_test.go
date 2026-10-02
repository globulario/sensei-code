package workflow

import (
	"strings"
	"testing"
)

// DF-34: a consequence verb is grounded in what it acts on, stated as a typed
// relation bound to one occurrence, and never in the word alone. These read the
// production classifier: declaredOutwardActionsWithEffects, and AssessConsequences
// over an Action carrying the relations, the call a live plan assessment makes.

// The measured 2026-10-02 plan sentences, verbatim from the objective. Both
// name e.Bus.Publish, the engine's in-process event bus. The second carries the
// whole word twice -- "append-then-publish" and "publish completion" -- so it
// takes two relations, one per occurrence.
const (
	df34PublishedSentence = "the route-bearing ReviewCompleted record must be durably appended before it is published or followed."
	df34PublishSentence   = "Add a checked append-then-publish path for ReviewStarted and route-bearing ReviewCompleted ... do not update delivered-review state, publish completion, or follow its ..."
)

// The Objective-49 truncate language. No verbatim copy of that plan is in
// this repository; this restates the operation ruling 70 describes -- a
// truncation bounded to a proven all-NUL suffix after durable quarantine,
// manifest creation and source re-verification.
const df34TruncateSentence = "After the durable quarantine copy and its manifest are written and the source is re-verified, truncate the session record to its proven all-NUL suffix boundary."

func effect(step int, op string, n int, scope string) DeclaredEffect {
	return DeclaredEffect{Statement: EffectInStep, Step: step, Operation: op, Occurrence: n, Target: "the target this occurrence acts on", Scope: scope}
}

func assessed(steps []string, consequences string, effects []DeclaredEffect) ConsequenceAssessment {
	return AssessConsequences(Action{Stage: StageCandidateEdit, DeclaredSteps: steps, DeclaredConsequences: consequences, DeclaredEffects: effects})
}

func mustBound(t *testing.T, label string, steps []string, consequences string, effects []DeclaredEffect) {
	t.Helper()
	if got := declaredOutwardActionsWithEffects(steps, consequences, effects); len(got) != 0 {
		t.Errorf("%s: declared outward %v, want none", label, got)
	}
	if a := assessed(steps, consequences, effects); a.Result != ConsequenceBounded {
		t.Errorf("%s: AssessConsequences = %s (%v), want BOUNDED", label, a.Result, a.Effects)
	}
}

func mustEscalate(t *testing.T, label string, steps []string, consequences string, effects []DeclaredEffect, want string) {
	t.Helper()
	got := declaredOutwardActionsWithEffects(steps, consequences, effects)
	if !listed(got, want) {
		t.Errorf("%s: declared outward %v, want it to include %q", label, got, want)
	}
	a := assessed(steps, consequences, effects)
	if a.Result != ConsequenceUnacceptable || !strings.Contains(strings.Join(a.Effects, "|"), "the plan declares an outward action: ") {
		t.Errorf("%s: AssessConsequences = %s (%v), want UNACCEPTABLE on a declared outward action", label, a.Result, a.Effects)
	}
}

// W1 + W5: the measured event-bus publication, through the live path.
func TestDF34MeasuredEventBusPublishIsNotOutward(t *testing.T) {
	steps := []string{df34PublishSentence}
	// The witness can fail: without the relation this is the measured refusal.
	mustEscalate(t, "no relation", steps, "", nil, "publish")
	bus := []DeclaredEffect{effect(1, "publish", 1, EffectInProcess), effect(1, "publish", 2, EffectInProcess)}
	mustBound(t, "in_process relations", steps, "", bus)
	mustEscalate(t, "first occurrence only", steps, "", bus[:1], "publish")
	mustEscalate(t, "second occurrence only", steps, "", bus[1:], "publish")

	// The first measured sentence says "published", which the whole-word
	// reader never matched at base: it is not outward with or without a
	// relation, and a relation naming an occurrence it lacks changes nothing.
	mustBound(t, "published, no relation", []string{df34PublishedSentence}, "", nil)
	mustBound(t, "published, unmatched relation", []string{df34PublishedSentence}, "", []DeclaredEffect{effect(1, "publish", 1, EffectInProcess)})
	mustBound(t, "both measured sentences", []string{df34PublishedSentence, df34PublishSentence}, "",
		[]DeclaredEffect{effect(2, "publish", 1, EffectInProcess), effect(2, "publish", 2, EffectInProcess)})
}

// The measured Objective-49 governed local truncation, through the live path.
func TestDF34GovernedLocalTruncateIsNotOutward(t *testing.T) {
	steps := []string{df34TruncateSentence}
	mustEscalate(t, "no relation", steps, "", nil, "truncate")
	mustBound(t, "governed_local relation", steps, "", []DeclaredEffect{effect(1, "truncate", 1, EffectGovernedLocal)})
	mustBound(t, "in consequences", nil, df34TruncateSentence,
		[]DeclaredEffect{{Statement: EffectInConsequences, Operation: "truncate", Occurrence: 1, Target: "the session record's NUL suffix", Scope: EffectGovernedLocal}})
}

// W2: a real outward publication escalates without a relation, with an outward
// or unknown one, and even against a relation that calls it in-process: each
// is positively recognised by an outward entry of its own, which the relation
// contradicts, so the word stays asserted.
func TestDF34OutwardPublicationStillEscalates(t *testing.T) {
	for _, c := range []struct{ prose, entry string }{
		{"publish the release", "publish the release"},
		{"npm publish", "npm publish"},
		{"publish the package to the registry", "publish the package"},
		{"publish the artifact", "publish the artifact"},
	} {
		steps := []string{c.prose}
		mustEscalate(t, c.prose+": no relation", steps, "", nil, "publish")
		mustEscalate(t, c.prose+": outward", steps, "", []DeclaredEffect{effect(1, "publish", 1, EffectOutward)}, "publish")
		mustEscalate(t, c.prose+": unknown", steps, "", []DeclaredEffect{effect(1, "publish", 1, EffectUnknown)}, "publish")
		inProcess := []DeclaredEffect{effect(1, "publish", 1, EffectInProcess)}
		mustEscalate(t, c.prose+": against in_process, its own entry", steps, "", inProcess, c.entry)
		mustEscalate(t, c.prose+": against in_process, the word", steps, "", inProcess, "publish")
	}
}

// W3: an ambiguous publish that nothing places stays asserted.
func TestDF34AmbiguousPublishStillEscalates(t *testing.T) {
	for _, s := range []string{"publish it", "then publish"} {
		mustEscalate(t, s, []string{s}, "", nil, "publish")
		mustEscalate(t, s+": unknown", []string{s}, "", []DeclaredEffect{effect(1, "publish", 1, EffectUnknown)}, "publish")
	}
}

// Destructive or unbounded truncation stays asserted whatever is said about it
// other than a valid governed_local relation on exactly its occurrence.
func TestDF34DestructiveTruncateStillEscalates(t *testing.T) {
	for _, s := range []string{"truncate the users table in the shared database", "truncate the session record", "truncate it"} {
		steps := []string{s}
		mustEscalate(t, s+": no relation", steps, "", nil, "truncate")
		mustEscalate(t, s+": outward", steps, "", []DeclaredEffect{effect(1, "truncate", 1, EffectOutward)}, "truncate")
		mustEscalate(t, s+": unknown", steps, "", []DeclaredEffect{effect(1, "truncate", 1, EffectUnknown)}, "truncate")
		mustEscalate(t, s+": in_process is not truncate's bound", steps, "", []DeclaredEffect{effect(1, "truncate", 1, EffectInProcess)}, "truncate")
	}
	mustEscalate(t, "drop table beside a bounded truncate", []string{"drop table sessions and truncate the record"}, "",
		[]DeclaredEffect{effect(1, "truncate", 1, EffectGovernedLocal)}, "drop table")
}

// Every incomplete, unknown, contradictory or unmatched relation leaves the
// measured occurrence asserted.
func TestDF34InvalidRelationsSuppressNothing(t *testing.T) {
	// The measured sentence's second occurrence is bound validly throughout, so
	// each case decides the first.
	steps := []string{df34PublishSentence}
	valid := effect(1, "publish", 1, EffectInProcess)
	second := effect(1, "publish", 2, EffectInProcess)
	with := func(f func(*DeclaredEffect)) []DeclaredEffect { e := valid; f(&e); return []DeclaredEffect{e, second} }
	cases := map[string][]DeclaredEffect{
		"governed_local is not publish's bound": with(func(e *DeclaredEffect) { e.Scope = EffectGovernedLocal }),
		"scope outside the vocabulary":          with(func(e *DeclaredEffect) { e.Scope = "local" }),
		"scope folded case":                     with(func(e *DeclaredEffect) { e.Scope = "In_Process" }),
		"scope absent":                          with(func(e *DeclaredEffect) { e.Scope = "" }),
		"operation outside the vocabulary":      with(func(e *DeclaredEffect) { e.Operation = "deploy" }),
		"operation folded case":                 with(func(e *DeclaredEffect) { e.Operation = "Publish" }),
		"operation of another word":             with(func(e *DeclaredEffect) { e.Operation = "truncate" }),
		"blank target":                          with(func(e *DeclaredEffect) { e.Target = "  " }),
		"occurrence zero":                       with(func(e *DeclaredEffect) { e.Occurrence = 0 }),
		"unmatched occurrence":                  with(func(e *DeclaredEffect) { e.Occurrence = 3 }),
		"step out of range":                     with(func(e *DeclaredEffect) { e.Step = 2 }),
		"step zero":                             with(func(e *DeclaredEffect) { e.Step = 0 }),
		"statement outside the vocabulary":      with(func(e *DeclaredEffect) { e.Statement = "steps" }),
		"names the consequences instead":        with(func(e *DeclaredEffect) { e.Statement = EffectInConsequences; e.Step = 0 }),
		"contradicted by an outward relation":   {valid, second, effect(1, "publish", 1, EffectOutward)},
		"contradicted by another target":        {valid, second, func() DeclaredEffect { e := valid; e.Target = "the release registry"; return e }()},
	}
	for label, effects := range cases {
		mustEscalate(t, label, steps, "", effects, "publish")
	}
	// Control: the same valid relation, duplicated without disagreement, still binds.
	mustBound(t, "agreeing duplicate", steps, "", []DeclaredEffect{valid, second, valid})
	// A word outside the operation vocabulary has no bounded scope at all, not
	// even an absent one.
	mustEscalate(t, "operation outside the vocabulary, scope absent", []string{"deploy the service"}, "",
		[]DeclaredEffect{{Statement: EffectInStep, Step: 1, Operation: "deploy", Occurrence: 1, Target: "t"}}, "deploy")
	// Consequences with a step number states no single site.
	mustEscalate(t, "consequences carrying a step", nil, "then publish",
		[]DeclaredEffect{{Statement: EffectInConsequences, Step: 1, Operation: "publish", Occurrence: 1, Target: "t", Scope: EffectInProcess}}, "publish")
}

// A relation binds ONE occurrence: a second publish in the same statement, the
// same word in another statement, and every other outward word are unaffected.
func TestDF34RelationBindsOnlyItsOccurrence(t *testing.T) {
	twice := []string{"publish completion on the event bus, then publish the package to the registry"}
	mustEscalate(t, "second occurrence", twice, "", []DeclaredEffect{effect(1, "publish", 1, EffectInProcess)}, "publish")
	// Mislabelling the package publication in-process does not bound it.
	both := []DeclaredEffect{effect(1, "publish", 1, EffectInProcess), effect(1, "publish", 2, EffectInProcess)}
	mustEscalate(t, "both occurrences called in_process", twice, "", both, "publish")
	mustEscalate(t, "both occurrences called in_process, its own entry", twice, "", both, "publish the package")
	// Control: two in-process occurrences that nothing recognises as outward bind.
	mustBound(t, "both in-process occurrences bound", []string{"publish completion on the event bus, then publish the next event"}, "", both)

	other := []string{df34PublishSentence, "publish the package to the registry"}
	mustEscalate(t, "same word in another step", other, "", []DeclaredEffect{effect(1, "publish", 1, EffectInProcess)}, "publish")
	mustEscalate(t, "same word in consequences", []string{df34PublishSentence}, "then publish",
		[]DeclaredEffect{effect(1, "publish", 1, EffectInProcess)}, "publish")
	mustEscalate(t, "another outward word", []string{"publish completion and deploy the service"}, "",
		[]DeclaredEffect{effect(1, "publish", 1, EffectInProcess)}, "deploy")
}

// W6: varying the explanatory prose while the governing relation is unchanged
// reaches the same bounded decision; changing only the relation's scope to an
// outward or unknown one restores escalation over the same prose.
func TestDF34StructuredProseParity(t *testing.T) {
	cases := []struct {
		op, bound string
		prose     []string
	}{
		{"publish", EffectInProcess, []string{
			"Append ReviewCompleted durably, then publish it",
			"publish the record only after the append returns",
			"Route-bearing records publish once durable",
		}},
		{"truncate", EffectGovernedLocal, []string{
			df34TruncateSentence,
			"truncate the record to its last valid line",
			"Once the quarantine manifest is written, truncate",
			"Re-verify the source and truncate the suffix",
		}},
	}
	for _, c := range cases {
		for _, s := range c.prose {
			steps := []string{"read the record", s}
			mustBound(t, c.op+" bounded: "+s, steps, "", []DeclaredEffect{effect(2, c.op, 1, c.bound)})
			for _, scope := range []string{EffectOutward, EffectUnknown} {
				mustEscalate(t, c.op+" "+scope+": "+s, steps, "", []DeclaredEffect{effect(2, c.op, 1, scope)}, c.op)
			}
		}
	}
}

// The relation arrives on the architecture wire contract and the strict
// validator accepts it; a body without it reads exactly as before.
func TestDF34DeclaredEffectsCrossTheArchitectureContract(t *testing.T) {
	body := `{"decision":"proceed","summary":"s","plan":"p","steps":[` + quoteJSON(df34PublishSentence) + `],
	 "consequences":"c","files":["internal/workflow/engine.go"],"mode":"modify","claims":[{"statement":"s","source":"repository"}],
	 "declared_effects":[{"statement":"step","step":1,"operation":"publish","occurrence":1,"target":"Engine.emit event bus","scope":"in_process"},
	                     {"statement":"step","step":1,"operation":"publish","occurrence":2,"target":"Engine.emit event bus","scope":"in_process"}]}`
	if err := StrictValidateArchitectureBody(body); err != nil {
		t.Fatalf("strict contract refused declared_effects: %v", err)
	}
	var d architectureDecision
	if err := strictDecodeModelJSON(body, &d); err != nil {
		t.Fatal(err)
	}
	if len(d.DeclaredEffects) != 2 || d.DeclaredEffects[0] != (DeclaredEffect{Statement: EffectInStep, Step: 1, Operation: "publish", Occurrence: 1, Target: "Engine.emit event bus", Scope: EffectInProcess}) {
		t.Fatalf("declared_effects decoded as %+v", d.DeclaredEffects)
	}
	mustBound(t, "decoded", d.Steps, d.Consequences, d.DeclaredEffects)
	mustEscalate(t, "decoded without effects", d.Steps, d.Consequences, nil, "publish")
}

// declared_effects name occurrences in a proceed plan's steps and
// consequences; a reply or an escalation that carries them states two
// decisions, and the strict contract refuses it.
func TestDF34DeclaredEffectsAreReservedForProceed(t *testing.T) {
	effects := `"declared_effects":[{"statement":"step","step":1,"operation":"publish","occurrence":1,"target":"Engine.emit event bus","scope":"in_process"}]`
	proceed := `{"decision":"proceed","summary":"s","plan":"p","steps":["publish completion"],"consequences":"c",
	 "files":["internal/workflow/engine.go"],"mode":"modify","claims":[{"statement":"s","source":"repository"}],` + effects + `}`
	if err := StrictValidateArchitectureBody(proceed); err != nil {
		t.Fatalf("proceed carrying declared_effects refused: %v", err)
	}
	reply := `{"decision":"reply","message":"m"`
	escalate := `{"decision":"escalate","summary":"s","human_question":"q","recommendation":"1","options":[{"id":"1","label":"one","description":"d1"},{"id":"2","label":"two","description":"d2"}]`
	for label, head := range map[string]string{"reply": reply, "escalate": escalate} {
		// Control: the same body without declared_effects is accepted, so the
		// refusal below is about declared_effects and nothing else.
		if err := StrictValidateArchitectureBody(head + `}`); err != nil {
			t.Fatalf("%s control refused: %v", label, err)
		}
		err := StrictValidateArchitectureBody(head + `,` + effects + `}`)
		if err == nil || !strings.Contains(err.Error(), "declared_effects") {
			t.Errorf("%s carrying declared_effects: err = %v, want a refusal naming declared_effects", label, err)
		}
	}
}

func quoteJSON(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}
