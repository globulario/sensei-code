# Phase B3 — self-grounding sensei-code with Families 1–3 (frozen before any run)

Per the Phase B split of 2026-08-28: B1 (foreign proving ground) is closed,
B2 (corpus) is under way, and S1 (receipt/restore consolidation) may not be
handed to the loop until the surfaces it touches hold mechanically established
coverage and the obligations it must preserve are mechanically visible. B3
produces that. It is a measurement campaign with a target, not a repair.

## Baseline, measured 2026-08-28 (`sensei preflight -addr localhost:10122`)

```text
internal/workflow/engine.go          OK      ARCHITECTURE_SENSITIVE   anchors=3
internal/session/store.go            OK      ARCHITECTURE_SENSITIVE   anchors=2
internal/workflow/suppliedplan.go    EMPTY   UNKNOWN_IMPACT           anchors=0
internal/workflow/prospective.go     EMPTY   UNKNOWN_IMPACT           anchors=0
internal/workflow/testedit.go        EMPTY   UNKNOWN_IMPACT           anchors=0
internal/workflow/premise.go         EMPTY   UNKNOWN_IMPACT           anchors=0
internal/derived/derived.go          EMPTY   UNKNOWN_IMPACT           anchors=0
self recipes at start                1: field_access_under_lock(internal/event Bus.subs)
```

Every surface S1 would touch except two returns empty knowledge. The V2
constitution forbids self-simplification on exactly that reading.

## Target

Two things, and `anchors > 0` is only the first:

1. **Coverage over the S1 surfaces** from the three proven families, run
   over sensei-code itself: lock discipline (`Engine.mu` guards the grant,
   receipt, premise and closure maps), command confinement (`git`, `gh`,
   `sensei`, provider binaries — which packages may invoke them), mutation
   confinement (which package writes `taskContext`, `Routing`, `GapIdentity`,
   the grant records, `session.Interrupted`).
2. **The preservation obligations represented in the graph** — as invariants
   bound to required tests by exact name, so `sensei preflight` names them
   when the engine is edited and a renamed test satisfies nothing:

```text
record != authority                          TestARecordedTestEditGrantIsReEstablishedFromTheWorldOrRefused
resume cannot mint authority                 TestARepeatedResumeCannotMintTestEditAuthority
repeated resume cannot strengthen authority  (same)
world / base identity is exact               TestASuppliedPlanSurvivesResumeOrFailsClosed, TestProspectiveFactsAreReadAtThePinnedBase
absence requires positive establishment      TestASuppliedRecordThatLostItsSourceIsNotTheArchitects, TestMutationConfinementIsUnresolvedWhenAReceiverCannotBeBound (sensei)
future-only: a question cannot cover its run TestDerivedCoverageExcludesTheWritingTask
operational authority != architectural       TestOperationalAuthorityIsSubtractedFromTheCoverageQuestionNotAddedToIt
authority reaches the execution boundary     TestTheImplementorIsShownTheProspectiveGrantItOperatesUnder, TestTheGrantSurvivesAReviewFeedbackCycle
candidate inspection is independent+terminal TestAProspectiveSurfaceRefutationStopsBeforeHandoff, TestAGrantedTestEditIsInspectedAgainstItsExactGrant
authority kinds stay semantically distinct   (the operational/architectural test above; plan-source tests)
premise identity is engine-owned             TestAParaphrasedPremiseDoesNotBuyAFreshClosureRound, TestAnUnansweredReceiptIsUnresolvedNotUnasked
```

Each enters the graph through `sensei propose` (invariant + required_test),
reviewed and committed by the human — never promoted by a run.

## How the runs go

Same shape as the foreign campaigns, on this repository at `:10122`:
- selection is mechanical and pre-declared per family (stable path order,
  frozen predicate) over the S1 surfaces; every passed-over verdict recorded;
  `NO_SUBJECT` reported rather than fabricated;
- the hand-derivation tool for Family 3 (`experiments/mutation-v2/selection/
  mutscan`) is reused unchanged, sealed by sha256;
- governed tasks written from the code, naming no relation; the investigator
  is never shown the selection;
- each encounter enters the corpus (B2) like any other.

Stopping rule: the campaign stops when every S1 surface reads `anchors > 0`
with at least one anchor per family that applies to it, and every obligation
above is a graph invariant bound to its test — or when a family cannot express
a relation an S1 surface needs, which is the only condition under which a
fourth family may be considered. Whichever comes first is recorded; S1 is
not started on a partial reading.

## Not this campaign

Not S1 itself. Not a new family on speculation. Not "coverage" of test files
(M2.2 handles edits to them as operational authority). Not promotion of any
proposal by a run.
