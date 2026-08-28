# b3-self-grounding — Families 1–3 applied to sensei-code itself

Protocol: `docs/work/b3-self-grounding.md` (merged bdaa946). Subject: sensei-code
`7ae7236e218480c0779a2960c01d41027e169e1b`, detached, pre-control; graph
`github.com/globulario/sensei-code` at `:10122`, digest `def94857…`
(`docs/work/b3-baseline/`). Producer: sensei `f79f96f9` (`sensei-f3`). The
seven S1 surfaces, in the frozen order. Every verdict is recorded; no subject
was chosen because it was expected to work.

## Applicability sweeps, in the preregistered order 1 → 2 → 3

Tools, sealed by sha256: `tools/lockscan.go` (Family 1 enumeration,
`ee5986e8916e5083…`), the literal-`exec.Command` grep for Family 2 (in
this file), and the unchanged mutation-v2 `mutscan` (`949ac76c0125211a…`,
built go1.26.0) for Family 3.

**Family 1 — lock discipline.** Applicable where a struct in a surface holds
a `sync.Mutex`/`RWMutex`: `Engine.mu` (18 other fields) and `Store.mu`
(1). 19 propositions derived at the subject world (`sweeps/family1.*`):

```
DERIVED 12   Engine.{pending,notes,stops,observing,objectives,supplied,graphs,findings,routings,closures,premises,testEdits}
REFUTED  5   Engine.{Repo,Config,Store,SessionID}, Store.path  -- set-once fields read without the lock; counterexamples
             to the relation, recorded, not judged
UNRESOLVED 2 Engine.Bus (emit: lock state not established), Engine.prospective (5 of 9 accesses via c.prospective)
```

The DERIVED subjects for `supplied`, `premises`, `testEdits`, `stops` span
two files each: `engine.go` **and** `suppliedplan.go` / `premise.go` /
`testedit.go` respectively. Family 1 can therefore anchor three of the five
EMPTY surfaces. `prospective.go` stays out (UNRESOLVED); `derived.go` has no
lock (NO_SUBJECT).

**Family 2 — command confinement.** One literal executable in the surfaces:
`git` at `prospective.go:83/88` (owner `internal/workflow`); `derived.go:239`
invokes a variable (`bin`), unobservable by `Limits()`. The proposition
`command_invocation_confined_to("git" confined to internal/workflow) searched
under .` is **REFUTED**: 31 of 33 observable invocations originate elsewhere
(`internal/gitx`, `internal/doctor`, `cmd/proofbench`, …). A true statement
about this code: `prospective.go` shells to `git` directly rather than through
`internal/gitx`, which is where `git` is otherwise confined. Recorded as a
counterexample; whether it is a defect is a human question. Every other
surface: NO_SUBJECT.

**Family 3 — mutation confinement.** 1,053 exported `(T.F)` subjects in the
module; 43 in the seven surfaces (`sweeps/family3.subjects.jsonl`): 37
DERIVED with zero writes (not subjects), 5 DERIVED with writes, 1 REFUTED:

```
DERIVED   derived.Result.{Outcome,Detail,Anchor}      writes inside internal/derived
DERIVED   Engine.{Store,SessionID}                    writes inside internal/workflow
REFUTED   derived.Recipe.Provenance                    1 of 2 writes outside internal/derived
                                                      (counterexample to confinement)
```

`session.Interrupted`'s exported fields show zero writes to the sealed tool
(they are written through an embedding `partial` in `FindInterrupted`; the
tool follows promotion through go/types, so this is recorded as what the
tool said, to be checked when the recipe runs). Other surfaces: NO_SUBJECT.

## Per-surface applicability (what a recipe could anchor)

```
internal/workflow/engine.go          F1 {'REFUTED': 4, 'UNRESOLVED': 2, 'DERIVED': 12}   F2 NO_SUBJECT                               F3 {'DERIVED': 2}
internal/workflow/suppliedplan.go    F1 {'DERIVED': 1}                                   F2 NO_SUBJECT                               F3 NO_SUBJECT
internal/workflow/prospective.go     F1 NO_SUBJECT                                       F2 REFUTED(git, owner internal/workflow)    F3 NO_SUBJECT
internal/workflow/testedit.go        F1 {'DERIVED': 1}                                   F2 NO_SUBJECT                               F3 NO_SUBJECT
internal/workflow/premise.go         F1 {'DERIVED': 1}                                   F2 NO_SUBJECT                               F3 NO_SUBJECT
internal/session/store.go            F1 {'REFUTED': 1}                                   F2 NO_SUBJECT                               F3 NO_SUBJECT
internal/derived/derived.go          F1 NO_SUBJECT                                       F2 variable exe: unobservable               F3 {'REFUTED': 1, 'DERIVED': 3}
```

## Reading

Two of the five EMPTY surfaces are reachable by Family 1 (`suppliedplan.go`,
`premise.go`, `testedit.go` — three, in fact) and one by Family 3
(`derived.go`). `prospective.go` is reachable by neither as things stand:
Family 1 is UNRESOLVED on `Engine.prospective`, Family 2 REFUTES its `git`
site, Family 3 finds no written exported field in it. That is the first
surface for which the stopping rule's "all three inapplicable" clause may
come into play — recorded here, not resolved here.

## Natural encounters — proposed, NOT frozen

Per the protocol, encounters run only where a family is mechanically
applicable, with the investigator untold. Tasks written from the subject's
code, naming no relation; instrument SHAs and predictions written at freeze:

- **N1** (`internal/workflow/premise.go`, Family 1 applicable): *`applyPremiseResolutions`
  walks every receipt for every resolution; make the lookup by receipt id
  direct, with no change in behaviour.*
- **N2** (`internal/derived/derived.go`, Family 3 applicable): *`Result` is
  built field by field in `CLI.Derive`; return it through one constructor so
  a missing field cannot be left zero, with no change in behaviour.*
- **N3** (`internal/workflow/prospective.go`, no family applicable — a
  control): *`gitShowAt` runs `git show` and then `git ls-tree` on failure;
  make the absence check one call, with no change in behaviour.*

## N1 and N2 — FROZEN (decision of 2026-08-28; N3 held, Family 4 not justified)

```
subject         sensei-code 7ae7236e218480c0779a2960c01d41027e169e1b, detached worktree, pre-control;
                tree clean; docs/work holds no B2/B3/S1 document; experiments/ holds no selection of this campaign
                the ONLY addition: .sensei-code/config.json (the :10122 endpoint), byte-identical to the
                controller's (sha256 prefix recorded in runs/N*.run); nothing else is seeded
graph           github.com/globulario/sensei-code at :10122 (digest def94857… at baseline; recorded per run)
consumer        sensei-code f4038b76f13428b1b1e0e6b35bd04fde5a25ac71 (main; last product change d6fcd11c)  -> sensei-code-b3
producer        sensei f79f96f9  -> sensei-f3 (SENSEI_BIN)
recipes         1 at start, the subject's own: field_access_under_lock(internal/event Bus.subs)
env             SENSEI_CODE_BENCHMARK=1; derive receipts moved aside before each invocation
```

Tasks, byte for byte, written from the subject's code and naming no relation:

- **N1** — *`applyPremiseResolutions` walks every receipt for every resolution; make the lookup by receipt id direct, with no change in behaviour.*
- **N2** — *`Result` is built field by field in `CLI.Derive`; return it through one constructor so a missing field cannot be left zero, with no change in behaviour.*

Stopping rule: one invocation each, N1 then N2, nothing altered between; every
plan, proposed recipe, receipt, coverage line, route and terminal preserved as
`runs/N1.*`, `runs/N2.*` and entered in the B2 corpus with graph identity;
exit 3 preserves the question; timeout or crash is an instrument finding. The
investigator is never shown the sweeps or this section.

Predictions:
- N1: cold (`0 anchors`, the sole recipe is over `internal/event`) → closure
  round → the untold investigator may propose a Family 1 question over
  `internal/workflow` (the sweep shows `Engine.premises` under `Engine.mu`
  derives DERIVED and spans `premise.go`); if recorded, the future-only rule
  ends N1 cold or at the human; the anchor benefits the next encounter.
- N2: cold → closure → a Family 3 question over `internal/derived` may appear
  (the sweep shows `Result.{Outcome,Detail,Anchor}` DERIVED); same future-only
  consequence. A question about `Recipe.Provenance` derives REFUTED and is
  preserved as the court's answer.
- Either encounter may propose a question of another family, or none; each
  outcome is preserved and reported, not steered.

### N1 void 1 — provider quota (instrument), no plan produced

The architect provider (ChatGPT via Codex app-server) refused both attempts
with a usage limit: *You've hit your usage limit … try again at 11:00 PM.*
`workflow.failed: architect could not produce a bounded decision`. No plan,
no routing, no closure round, no investigator question: the run never
reached the thing the encounter measures. Recorded as
`runs/N1.void1-provider-quota.*`, entered in the corpus as an instrument
failure, and **not counted against N1's single invocation** — N1's invocation
is the first one that reaches the architect. N2 is not run against the same
exhausted provider. Nothing in the subject, the freeze, or the tasks changes.

### N1 — 03:01:08Z–03:09:31Z, exit 3, subject `7ae7236e`

- Round 1: plan `internal/workflow/premise.go` alone; `derived coverage:
  0 anchor(s) over 1 planned file(s); route bounded-knowledge-gap` (the
  subject's sole recipe is over `internal/event`). Closure round, receipt
  `gap-…-1`.
- Round 2: the re-plan added `internal/workflow/authority_test.go`. M2.2
  answered as designed — *no test-edit authority: … no planned file in its
  directory holds architectural coverage at the pinned world* — because
  `premise.go` has no anchor yet; `0 anchor(s) over 2 planned file(s)`. The
  architect asked to escalate; the engine closed instead (receipt `gap-…-2`,
  a distinct gap: the scope changed). **In this round the investigator,
  told nothing, proposed and recorded**
  `field_access_under_lock(internal/workflow Engine.premises mu)` — *"applyPremiseResolutions
  and premiseReceiptFor access the receipt collection while holding …"* — the
  proposition the sealed Family 1 sweep marks DERIVED across `engine.go`
  and `premise.go`. Identity
  `field_access_under_lock|internal/workflow|engine|premises|mu`, region
  `[premise.go, authority_test.go]`.
- Round 3: future-only — the recipe cannot cover the run that proposed it;
  the gap did not close within budget; escalated with the question
  preserved: *Should this change wait for Sensei to derive coverage over the
  proposed relationship, or proceed through an explicit authorization while
  the coverage gap remains open?* Options: wait / authorize open gap /
  require another design / stop. Exit 3.
- Predictions held: cold → closure → a Family 1 question over
  `internal/workflow` → recorded → future-only ends N1 at the human.
- `runs/N1.log` keeps the non-output events; untrimmed sha256 in
  `runs/N1.run`; receipts and recipes-after preserved. The recorded recipe
  is persisted in the subject as `209c5c6` (Go sources byte-identical to
  `7ae7236e`; only `docs/awareness/derived_recipes.json` differs) — the
  base for N2 and for any later encounter over `premise.go`.

Reading: the first self-grounding witness. Sensei, pointed at its own engine
with no control document in sight, asked the lock-discipline question about
the receipt map that S1 must preserve. The anchor it earns benefits the next
encounter over that region, by the future-only rule.

### N2 — 03:10:35Z–03:20:01Z, exit 3, subject `209c5c6` (N1's recipe persisted)

- Round 1: plan `internal/derived/derived.go` alone; `0 anchor(s) over 1
  planned file(s); route bounded-knowledge-gap` (the persisted recipes cover
  `internal/event` and `internal/workflow`, not `internal/derived`).
  Receipt `gap-…-1`.
- Round 2: the architect asked to escalate with no files in the plan
  (`0 anchor(s) over 0 planned file(s)`); the engine closed instead (receipt
  `gap-…-2`). **In this round the investigator, told nothing, proposed and
  recorded** `state_mutation_confined_to_owner(Result.Anchor in
  internal/derived, search .)` — *"CLI.Revalidate currently builds Result
  incrementally and assigns the authority-bearing Anchor only after a
  DERIVED…"* Identity
  `state_mutation_confined_to_owner|internal/derived|result|anchor|.`, region
  `[internal/derived/derived.go]`. The sealed Family 3 sweep marks
  `Result.Anchor` DERIVED with one write inside the owner.
- Round 3: future-only; the gap did not close; escalated with the question
  preserved: *Should Sensei Code retry this bounded constructor refactor
  after a later mechanical derivation, or leave the implementation
  unchanged?* Exit 3.
- Predictions held: cold → closure → a Family 3 question over
  `internal/derived` → recorded → future-only ends N2 at the human. The
  recipe is persisted in the subject as `6c36961` (three recipes: Bus.subs,
  Engine.premises, Result.Anchor; Go sources byte-identical to `7ae7236e`).

## N1 + N2 reading

Two encounters, two different epistemic species, both rediscovered by the
investigator against Sensei's own engine with no control document in its
world:

```
N1  premise.go   Family 1  field_access_under_lock(Engine.premises under Engine.mu)     RECORDED
N2  derived.go   Family 3  state_mutation_confined_to_owner(Result.Anchor in derived)   RECORDED
```

Each is a proposition the sealed sweeps had marked DERIVED over that
surface, and neither was shown to the investigator. Each run ended at the
human branch by the future-only rule, which is the rule doing its job: the
anchors these questions earn belong to the *next* encounter over
`premise.go` and `derived.go`, which — if the recipes derive DERIVED at the
persisted base — would read `1 anchor over 1 planned file` and route on
architectural authority, as Family 3's E3 did on golang/mod. That is the
next measurable step toward the B3 stopping rule for two of the five EMPTY
surfaces. Not established: whether the persisted recipes derive at
`6c36961` under the producer (the seal says they should; a run says so).
Also observed: M2.2 refused a test-edit grant in N1 for the right reason
(no covered sibling yet), and every escalation the architect asked for was
closed rather than raised.

### Mechanical check (not an encounter): do the persisted recipes derive at `6c36961`?

`sensei-f3 derive` at the subject HEAD, read-only, no run:

```
field_access_under_lock DERIVED | subjects: ['internal/workflow/engine.go', 'internal/workflow/premise.go'] | all 6 access(es) to Engine.premises occur while Engine.mu is held, across 2 subject file(s) (19 file(s) were r
state_mutation_confined_to_owner DERIVED | subjects: ['internal/derived/derived.go'] | all 1 observable write(s) to Result.Anchor under . originate from internal/derived, across 1 subject file(s) (
```
