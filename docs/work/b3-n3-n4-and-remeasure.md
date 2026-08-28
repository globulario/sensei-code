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
  `N*.log`, `N*.receipts.jsonl`, `N*.recipes-after.json`, graph digest at
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
