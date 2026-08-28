# Phase B3 — self-grounding sensei-code with Families 1–3 (frozen before any run)

Per the Phase B split of 2026-08-28: B1 (foreign proving ground) is closed,
B2 (corpus) is under way, and S1 (receipt/restore consolidation) may not be
handed to the loop until the surfaces it touches hold mechanically established
coverage and the obligations it must preserve are mechanically visible. B3
produces that. It is a measurement campaign with a target, not a repair.

## Baseline — machine-bound (`b3-baseline/`, sha256 per file in its README)

Measured on the **subject world**, not on a working tree: sensei-code source
`7ae7236e218480c0779a2960c01d41027e169e1b` (main before any Phase-B control
document existed), checked out detached; producer sensei `f79f96f9`; graph
`github.com/globulario/sensei-code` at `localhost:10122`, combined digest
`def94857a06a997412c56c682c39481b226f1834f93a4173425852965367b912`,
158,349 triples, `BUILD_PROVENANCE_STATE_STAMPED`, freshness CURRENT
(`b3-baseline/graph.metadata.json`). Each row quotes its
`b3-baseline/<file>.preflight.json`:

```text
surface                            status   risk                    confidence   direct_anchor_count  direct_invariants
internal/workflow/engine.go        OK       ARCHITECTURE_SENSITIVE  HIGH         3                    3
internal/session/store.go          OK       ARCHITECTURE_SENSITIVE  MEDIUM       2                    2
internal/workflow/suppliedplan.go  EMPTY    UNKNOWN_IMPACT          LOW          (none fired)         0
internal/workflow/prospective.go   EMPTY    UNKNOWN_IMPACT          LOW          (none fired)         0
internal/workflow/testedit.go      EMPTY    UNKNOWN_IMPACT          LOW          (none fired)         0
internal/workflow/premise.go       EMPTY    UNKNOWN_IMPACT          LOW          (none fired)         0
internal/derived/derived.go        EMPTY    UNKNOWN_IMPACT          LOW          (none fired)         0
self recipes at start              1: field_access_under_lock(internal/event Bus.subs)
```

Five of seven S1 surfaces return empty knowledge. The V2 constitution
forbids self-simplification on exactly that reading. A later measurement is
comparable to this one only at the same source SHA and graph digest, or with
both differences recorded.

## Controller and subject are separate checkouts

In the foreign campaigns the protocol lived outside the repository under
investigation. Here it would not: once this note, the S1 design, the corpus
and the selections are on `main`, an investigator reading its workspace could
read what the families are meant to discover, and "untold" is no longer
proven. So:

```text
controller checkout (this branch and its successors on main)
    B2 corpus · B3 protocol · S1 design · selections · predictions · overlays
        │  not visible
        ▼
subject checkout: detached at the PRE-CONTROL source world 7ae7236e
    pinned sensei-code source · graph built from the subject only
    no docs/work/b3-*, s1-*, docs/evidence/corpus, experiments/*/selection*
        │
        ▼
    investigator (architect, workers, reviewers) -- sees the subject only
```

The baseline above was measured on that subject world, which is what makes
it the baseline of the thing the investigator will see. Runs use the subject
worktree as `--repo-root`; their records are written to the controller. When
S1 eventually runs, its facts are re-derived against S1's own pinned base.

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

## How the runs go — preregistered order, applicability first

```text
frozen S1 surface list (the seven above)
        │
        ▼
Family 1 applicability sweep   lock discipline: every (type, field, lock) in the surfaces, stable order
        │
        ▼
Family 2 applicability sweep   command confinement: every literal exec.Command executable under the surfaces
        │
        ▼
Family 3 applicability sweep   mutation confinement: every exported (T.F) with >=1 write, the sealed
                               mutscan reused unchanged (sha256 949ac76c…)
        │
        ▼
record every DERIVED / REFUTED / UNRESOLVED / NO_SUBJECT, per family, per surface
        │
        ▼
natural governed encounters, only where a family is mechanically applicable
```

The order is 1 → 2 → 3 by construction, not by expectation: the family is
never chosen because it is expected to work. Note the mechanical fact that
shapes it: Family 3's sealed predicate admits only exported structs with
exported fields, and M2.2's grant machinery (`testEditFacts`,
`testEditGrant`, `testEditRecord`) is unexported, while `session.Interrupted`
and its receipt fields are exported — so Family 3 has plausible subjects in
`internal/session`, not necessarily in `testedit.go`. Whatever the sweeps
say is the answer.

Discipline as in the foreign campaigns: selection mechanical and
pre-declared per family; every passed-over verdict recorded; `NO_SUBJECT`
reported rather than fabricated; tasks written from the code, naming no
relation; the investigator never shown a selection; each encounter enters
the B2 corpus with its graph identity, so a run at the same source SHA
against a later graph is a distinct record.

Stopping rule — split by M25 (2026-08-28), after slice 2 showed the two
instruments disagree (`docs/work/b3-remeasure-6c36961/`):

- **Encounter-convergence criterion (B3 proper):** every S1 surface some
  family can reach holds a persisted recipe that *derives DERIVED* at the
  pinned base, from each family applicable to it — read by `sensei derive`,
  not by preflight.
- **Self-coverage criterion (production authority):** every S1 surface reads
  `direct_anchor_count > 0` in `sensei preflight`, and every obligation above
  is a graph invariant bound to its test. Only governed invariants/claims
  lift this; recipes are never promoted into canonical anchors to satisfy it.

Recipe DERIVED ≠ canonical preflight coverage. Encounter learning may improve
routing before it improves canonical self-coverage; that is a result, not a
failure. A fourth family may be considered only when all three families are
inapplicable (NO_SUBJECT) to a reachable-by-none surface. S1 is not started
on a partial reading of either criterion.

## Not this campaign

Not S1 itself. Not a new family on speculation. Not "coverage" of test files
(M2.2 handles edits to them as operational authority). Not promotion of any
proposal by a run.
