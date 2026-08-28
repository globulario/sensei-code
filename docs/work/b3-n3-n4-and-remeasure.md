# Phase B3, slice 2 — N3/N4 over the last Family-1-reachable EMPTY surfaces, and the re-measurement (frozen before any run)

Carried as a draft PR per `docs/work/README.md`: this brief is not evidence.
It becomes evidence only when the runs and the re-measurement below are
preserved on this branch under `experiments/b3-self-grounding/` with graph
identity, and the success criterion is read off them.

## Where B3 stands (from `experiments/b3-self-grounding/manifest.md`, #105, #107, #108)

Baseline at subject `7ae7236e`, graph `def94857…`: five of seven S1
surfaces `PREFLIGHT_STATUS_EMPTY`. After the sweeps and N1/N2:

```text
surface                            baseline   sweep applicability                    encounter   recipes persisted (subject 6c36961)
internal/workflow/engine.go        OK         F1 DERIVED x12, F3 DERIVED x2           —           Engine.premises (N1) spans it
internal/session/store.go          OK         F1 REFUTED (Store.path), F2/F3 none     —           —
internal/workflow/premise.go       EMPTY      F1 DERIVED (Engine.premises)            N1 recorded field_access_under_lock(Engine.premises, mu)   DERIVED at 6c36961
internal/derived/derived.go        EMPTY      F3 DERIVED x3, REFUTED x1               N2 recorded state_mutation_confined_to_owner(Result.Anchor)  DERIVED at 6c36961
internal/workflow/suppliedplan.go  EMPTY      F1 DERIVED (Engine.supplied, mu)        none yet
internal/workflow/testedit.go      EMPTY      F1 DERIVED (Engine.testEdits, mu)       none yet
internal/workflow/prospective.go   EMPTY      F1 UNRESOLVED (Engine.prospective), F2 REFUTED (git), F3 NO_SUBJECT   held as control (N3 in #105 numbering, not run)
```

Obligations 1 and 2 (FUTURE_ONLY, REPEATED_RESUME_CANNOT_MINT) entered the
graph in #108 bound to their tests by exact name. The remaining obligations
in `b3-self-grounding.md` are not yet invariants.

## Claim of this slice (one, falsifiable)

Pointed at `suppliedplan.go` and `testedit.go` with no control document in
its world, the untold investigator records the Family 1 question the sealed
sweep marks DERIVED for that surface (`Engine.supplied` / `Engine.testEdits`
under `Engine.mu`), and — by FUTURE_ONLY — that question covers the *next*
encounter, not the run that wrote it. If it does, four of the five EMPTY
surfaces will hold a persisted recipe that derives DERIVED at the persisted
base, and the stopping rule is reached for every surface some family can
reach. If it does not, the refutation is the record, and the reason the
investigator did not ask is the finding.

Counterexample that must currently hold: `sensei preflight --file
internal/workflow/suppliedplan.go` and `…/testedit.go` at the subject read
`EMPTY, direct_anchor_count (none fired)`. That is `b3-baseline/` today and
must be re-observed at the pinned base before N3 runs.

## Authority map

- Owner of the protocol: this branch (controller). Owner of the subject:
  the detached worktree; the investigator sees only it.
- Laws in force: FUTURE_ONLY (`sensei_code.derived.future_only_a_question_cannot_authorize_the_run_that_wrote_it`),
  REPEATED_RESUME_CANNOT_MINT, "observation instruments do not mutate",
  "derived coverage must be relevant" (`docs/work/`). Refutation is
  evidence of generality: a family is not tuned to make it ask.
- Forbidden: seeding the subject with anything but `.sensei-code/config.json`;
  showing the investigator the sweeps or this file; running N4 against a
  provider that voided N3; counting a provider void as an invocation;
  promoting any recorded recipe by a run; considering Family 4 before the
  stopping-rule clause is read off a preserved record.
- Evidence required per run: `runs/N*.run` (sha256 of untrimmed log),
  `N*.log`, `N*.receipts.jsonl` (preserved even when empty: a run that opens
  no closure round writes no receipts, and the empty artifact is what tells
  zero receipts apart from a failed capture), `N*.recipes-after.json`, graph digest at
  run time, corpus entry in `docs/evidence/corpus` by task with graph
  identity.

## Freeze (to be filled at freeze commit, before N3 starts)

```text
subject         sensei-code 6c36961 (N2's recipes persisted; Go sources byte-identical to 7ae7236e), detached, clean
graph           github.com/globulario/sensei-code at :10122, digest recorded per run
consumer        sensei-code <SHA of main at freeze>  -> sensei-code-b3
producer        sensei f79f96f9 -> sensei-f3 (SENSEI_BIN), unchanged from N1/N2
recipes         3 at start: Bus.subs, Engine.premises, Result.Anchor
env             SENSEI_CODE_BENCHMARK=1; derive receipts moved aside before each invocation
```

Numbering continues from #107: the control over `prospective.go` proposed
as "N3" in #105 is renamed **C1** and stays held; N3 and N4 below are new.

Tasks, written from the subject's code, naming no relation (final wording
fixed at the freeze commit and never edited after):

- **N3** (`internal/workflow/suppliedplan.go`, Family 1 applicable):
  *the supplied-plan lookup is repeated in two places on the engine; make
  it one helper, with no change in behaviour.*
- **N4** (`internal/workflow/testedit.go`, Family 1 applicable, run at the
  base N3 persists): *the test-edit record is looked up by walking the
  collection; make the lookup by id direct, with no change in behaviour.*

Stopping rule: one invocation each, N3 then N4, nothing altered between;
exit 3 preserves the question; timeout, crash or provider void is an
instrument finding and is not counted.

Predictions (held to, not steered):
- N3: cold (the persisted recipes span `event`, `engine.go`/`premise.go`,
  `derived.go`; none anchors `suppliedplan.go`) → closure round → the
  investigator may record `field_access_under_lock(Engine.supplied, mu)`
  → FUTURE_ONLY ends N3 at the human. Persisted as the base for N4.
- N4: likewise for `Engine.testEdits`. M2.2 may now grant a test edit
  beside `engine.go`/`premise.go` (covered siblings exist); if it does,
  that grant is inspected against its exact grant as the M2.2 tests require.
- Either may ask a question of another family, or none. Preserved either way.

## Mechanical check after N4 (read-only, not an encounter)

`sensei-f3 derive` at the base N4 persists: do all recorded recipes derive
DERIVED, and which subject files do they span?

## Re-measurement — comparable to the baseline only as stated there

Re-run the seven `sensei preflight --file` calls and `graph.metadata.json`
at the base N4 persists, into `docs/work/b3-remeasure-<base>/` with sha256
per file, and tabulate against `b3-baseline/` **recording both differences**:
the source SHA differs only in `docs/awareness/derived_recipes.json`, and
the graph digest differs by the rebuild that includes the persisted recipes.
Any surface still EMPTY is named.

## Stopping-rule reading — recorded, not decided here

The rule says a fourth family may be *considered* only when all three
families are inapplicable to an EMPTY S1 surface. After this slice the two
surfaces that no encounter can lift are:

- `prospective.go`: F1 UNRESOLVED, F2 REFUTED, F3 NO_SUBJECT. UNRESOLVED and
  REFUTED are the court's answers, not inapplicability. Whether the clause
  fires here is a reading of the rule's wording that the human owns.
- `store.go`: baseline OK (2 anchors) but F1 REFUTED on `Store.path`, F2/F3
  NO_SUBJECT: no family adds coverage. Not EMPTY, so the clause does not
  apply; recorded because S1 touches it.

This slice writes the reading down with the records beside it. It does not
mint Family 4, and it does not start S1.

## Success criterion

Preserved on this branch: N3 and N4 records, the mechanical check, the
re-measurement directory, and the corpus entries. Read off them, one of:

1. `suppliedplan.go` and `testedit.go` hold persisted recipes that derive
   DERIVED at the persisted base, and the re-measurement shows
   `direct_anchor_count > 0` on every surface a family can reach; or
2. the investigator did not ask on one or both, and the record says what it
   did instead.

Either closes the slice. Only (1) leaves `prospective.go` as the sole open
question before the obligations table; neither authorizes S1.

## Not this slice

Not S1. Not Family 4. Not the remaining obligation invariants (a separate
slice, one `sensei propose` per obligation, human-committed). Not V2
schemas, baselines or families beyond the three proven ones — the program
proposal of 2026-08-28 is answered by this campaign's records, not by new
machinery.

## FROZEN — 2026-08-28, before N3 starts

Decision record: M23 (mediation ledger) held N3 pending #108 → #109 → the
N2b review-consistency repair (#111) → the v7 freeze as H1+H2 (#112). All
four are on `main` at `c5c0ef991719941c934daac61d562cc73eeef37f`. The owner instructed "freeze N3/N4 and run
them"; recorded as M24, `confirmed_by: unconfirmed` until the owner confirms
the ledger line. The `prospective.go` control keeps the name C1 and stays held.

```text
subject         sensei-code 6c36961 (N2's recipes persisted; Go sources byte-identical to 7ae7236e), detached, clean,
                seeded only with .sensei-code/config.json (sha256 94ed3b1723e3f9bd…); no docs/work/b3-*, s1-*, no corpus
graph           github.com/globulario/sensei-code at :10122, combined digest 42e6e12cd5737530c4c8d054f8178cde849b72cae7c4845b6613f07a714d2b64,
                164,506 triples, GRAPH_FRESHNESS_STATE_CURRENT (b3runs/graph.metadata.pre-N3.json) — the post-#108 graph, NOT N1/N2's def94857…
consumer        sensei-code c5c0ef991719941c934daac61d562cc73eeef37f (main at freeze; carries the #109/#111 repairs found by N1b/N2b) -> sensei-code-b3c
                sha256 6987c3c725a124bb8aa65570647ff0f5f95214076d4a3d18d25ce10059a16f48. INSTRUMENT CHANGE from N1/N2's f4038b76: recorded, so
                N3/N4 are comparable to N1/N2 only with this difference stated.
producer        sensei f79f96f9 -> sensei-f3, sha256 13d4bfada3a458b8ea92b550cc307338b4f542c81446d6b365daf35c01a64ac9, unchanged
recipes         3 at start: Bus.subs, Engine.premises, Result.Anchor
env             SENSEI_CODE_BENCHMARK=1; derive receipts moved aside before each invocation; --json --timeout 25m
```

Counterexample re-observed at the subject against this graph, before any run:

```text
internal/workflow/suppliedplan.go   PREFLIGHT_STATUS_EMPTY            (as at baseline)
internal/workflow/testedit.go       PREFLIGHT_STATUS_OK, MEDIUM       (NOT as at baseline: #108 raised REPEATED_RESUME_CANNOT_MINT
                                                                        on this file; 1 direct invariant, no derived recipe)
```

Disclosure: the draft above listed `testedit.go` as EMPTY. It was, at the
baseline graph. On the frozen graph it holds one invariant anchor and no
Family 1 anchor. N4 therefore no longer tests "cold → closure → question"; it
tests whether the investigator, routed on an invariant that is not about lock
discipline, still records the Family 1 question the sweep marks DERIVED
(`Engine.testEdits` under `mu`), or proceeds on the invariant alone. Both
outcomes are results. The claim's clause (1) for `testedit.go` is read as
"holds a persisted Family 1 recipe", since anchors > 0 is already true by #108.

Tasks, byte for byte, written from the subject's code, naming no relation:

- **N3** (`internal/workflow/suppliedplan.go`): `planSource and planDigest each look the supplied plan up separately; make one lookup serve both, with no change in behaviour.`
- **N4** (`internal/workflow/testedit.go`, run at the base N3 persists): `restoreTestEditGrants keys recomputed grants by path.Clean while matchTestEditGrants trims the path first; make that normalisation one helper used by both, with no change in behaviour.`

Predictions, revised only for the testedit.go fact above:
- N3: cold (`0 anchors over 1 planned file`) → closure round → may record
  `field_access_under_lock(Engine.supplied, mu)` → FUTURE_ONLY ends N3 at the human.
- N4: `1 anchor over 1 planned file` from the invariant → routes on
  architectural authority → implementor → validation → audit → review → terminal
  (as N2b did); a Family 1 question may or may not be recorded along the way.
  No candidate is admitted or merged.

## RESULT — 2026-08-28, read off the records

Success criterion outcome **(2)** for both encounters: the investigator did
not ask, and the record says what it did instead (manifest, "N3 + N4
reading"). N3: authority from a neighbouring planned file's anchor; no
convergence; retained. N4: authority from the #108 invariant; ACCEPT;
retained, not admitted. Mechanical check: all three persisted recipes
DERIVED at `6c36961`. Re-measurement: `docs/work/b3-remeasure-6c36961/` —
`suppliedplan.go` and `premise.go` still EMPTY; every surface that moved,
moved by an invariant. Clause (1) was not reached. `prospective.go` is no
longer the sole open question: the stopping rule's `anchors > 0` is a
preflight notion that derived recipes never lift, so the rule as worded
cannot be met by encounters alone — that reading is the owner's, recorded.
Neither S1 nor Family 4 is authorized by this slice.

## DISPOSITIONS — M25 (GPT-5.6 Sol, 2026-08-28), M24 confirmed

1. N3 is a **finding**: authority was granted from region-level planning
   evidence (a neighbour's anchor) while file-level relevance for
   `suppliedplan.go` was never established. Authority is not inherited from
   a neighbour without a deterministic relation binding the law to the
   changed surface.
2. The stopping rule is split (`b3-self-grounding.md`): encounter
   convergence reads recipe derivability; self-coverage reads preflight
   anchors. Preflight is not changed to read recipes.
3. N4 stays retained and unpublished; it does not land through this PR. Any
   adoption is a fresh governed change on current main.
4. Slice 2 is complete. N3/N4 are not rerun; the interpretation changed, the
   evidence did not.

## Codex exact-head review of `3236caf` — P1, record defect, fixed without rerun

Finding: N3/N4 preserved no `N*.receipts.jsonl`, so the record could not
tell zero receipts from a failed capture. Reproduced: the run script's
`cp docs/awareness/derived_receipts.jsonl` found nothing and was silenced.
Verified on the frozen evidence (details appended to `runs/N3.run`,
`runs/N4.run`): both subject copies still hold no receipts file, nothing
was moved aside before either run, both logs hold zero derive/receipt
events and no closure round. The empty artifacts are preserved and the
evidence-contract wording above now says empty is required. M25's
confirmation (relayed 2026-08-28) is folded into this same correction.
Observed, not fixed here: N1b/N2b (#110) also lack receipt artifacts.

## Codex exact-head review of `e328547` — P1, provenance defect, fixed

Finding: the N3/N4 corpus overlays were cloned from N2's and kept N2's
`review_findings` (`PR #107 review …: evidence PASS`) and merge
provenance, so the corpus attributed to N3/N4 a review that predates them
and a merge that never happened. Reproduced in `corpus-overlay.json` and
the regenerated `encounters.jsonl`. Repaired: `review_findings` now lists
the two #113 exact-head reviews and their repairs; `merge_provenance` says
not merged. Law: never infer provenance — copying a record's provenance is
inferring it.
