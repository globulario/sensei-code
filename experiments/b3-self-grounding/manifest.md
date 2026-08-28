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
surfaces. Not established by the encounters themselves: whether the persisted
recipes derive at `6c36961` under the producer. Subsequently established by
the read-only mechanical check below: both derive DERIVED, with subjects
spanning `premise.go` and `derived.go`.
Also observed: M2.2 refused a test-edit grant in N1 for the right reason
(no covered sibling yet), and every escalation the architect asked for was
closed rather than raised.

### Mechanical check (not an encounter): do the persisted recipes derive at `6c36961`?

`sensei-f3 derive` at the subject HEAD, read-only, no run:

```
field_access_under_lock DERIVED | subjects: ['internal/workflow/engine.go', 'internal/workflow/premise.go'] | all 6 access(es) to Engine.premises occur while Engine.mu is held, across 2 subject file(s) (19 file(s) were r
state_mutation_confined_to_owner DERIVED | subjects: ['internal/derived/derived.go'] | all 1 observable write(s) to Result.Anchor under . originate from internal/derived, across 1 subject file(s) (
```

## Chronology disclosure (review 5047711145)

The freeze that preceded N1 was committed as
`6e077f8d51285546ec6560082bb6bb52165bb2cc` at `2026-08-27T22:16:08-04:00`, before N1
began at 03:01:08Z. This branch was later rebased onto `main` after #106
(the provenance-schema change), which rewrote that commit as `93f7478`; the
frozen `manifest.md` blob is identical in both (`8e435d67c498…`), but the
original freeze commit is no longer an ancestor of this branch and its
committer timestamp was necessarily rewritten. No retroactive merge was
manufactured to make the graph look continuous: the pre-run freeze is
proven by the original commit's timestamp and the unchanged blob, and this
note is the record of the rewrite.

## N1b and N2b — FROZEN (cleared by the #107 merge, 4b92bc67)

The second encounters. Same tasks byte for byte, same producer, same
consumer, same graph, same env as N1/N2; the only variable is that each
region's relation is now a **persisted question that deterministically
derives DERIVED** (mechanical check above).

```
subjects     TWO independent detached copies of 6c36961 (= 7ae7236e + the three investigator
             recipes; Go sources byte-identical to 7ae7236e), one per encounter, so neither
             run's knowledge writes reach the other; each seeded only with .sensei-code/config.json
             (sha256 94ed3b17…); each clean at start
consumer     sensei-code f4038b76  (sensei-code-b3)      producer  sensei f79f96f9 (sensei-f3)
graph        :10122 (read by both; neither run rebuilds it)
recipes      3 at start in each copy: Bus.subs, Engine.premises, Result.Anchor
tasks        N1b = N1's text; N2b = N2's text (byte for byte)
order        N1b, then N2b, sequentially; nothing altered between
```

Stopping rule: one invocation each; whatever it does is the result; every
plan, coverage line, route, grant, candidate, validation, audit, review and
terminal preserved as `runs/N1b.*`, `runs/N2b.*` and entered in the B2
corpus; exit 3 preserves the question; timeout or crash is an instrument
finding. **No candidate is admitted or merged**: B3 measures whether Sensei
can govern a self-change to judgement, not whether it may land one.

Predictions:
- If the architect plans only `premise.go` (N1b) / `derived.go` (N2b):
  `derived coverage: 1 anchor(s) over 1 planned file(s); route
  architectural-authority-granted` on the first routing, unless an
  inference premise opens a closure round first, then granted.
- If the architect adds a file, the existing rules decide: an existing
  test beside the now-covered subject may receive an M2.2 grant (operational
  authority, printed beside coverage, never summed); any other file is a
  coverage gap and the run routes cold or to the human. Nothing is forced
  to 1/1.
- Granted → implementor → validation → Sensei audit → independent review →
  terminal. A refutation or a REVISE cycle is ordinary and recorded.

### N1b — 03:44:38Z–04:09:38Z, exit 5 (timeout), subject copy `6c36961` (n1b)

Eight architect turns, zero implementor turns; the 25-minute timeout and
the 8-round ceiling coincided. No candidate. Two product defects found, one
loop shape observed, the second encounter's authority confirmed on the way:

- **Authority carried.** Every routing read `internal/workflow/premise.go
  [lock discipline]` — the fact the investigator discovered in N1, untold,
  derived at this base and carried into routing. And M2.2 issued its **first
  natural grant**: `existing-test edit authority recorded for
  internal/workflow/authority_test.go beside internal/workflow/premise.go
  (operational, not coverage)`, printed beside coverage and never summed.
- **Defect (a): the blind-spot coverage branch ignores operational
  authority.** With `premise.go` covered and the test granted, the route
  read `1 anchor(s) over 2 planned file(s); route bounded-knowledge-gap` —
  the branch asked the derivation to cover the granted test file. M2.2's
  subtraction had been wired into the `coverageAbsent` branch only; the
  comment above this branch already described this exact defect from an
  earlier instance. Fix: #109(a) `architecturalFiles()` + a
  `coverage-blind-spot` gap identity.
- **Defect (b): an unidentified gap buys a fresh round every time.** The
  condition text was identical on every round; under the pre-#98 wording
  key the second attempt would have been refused and the run escalated.
  Under #98 an unidentified gap matched no receipt and was issued a new one
  — and a new budget — each round. #98 spent *more* budget than the rule it
  replaced on every branch it did not identify. Fix: #109(b) the wording
  floor for unidentified gaps.
- **The loop.** The architect alternated: plan `{premise.go}` → *escalate*
  → "Sensei certifies this region, so it is resolved architecturally" → the
  question handed back; plan `{premise.go, authority_test.go}` → *proceed*
  → defect (a) → cold → closure. Eight turns, then the timeout. The
  implementor never ran.
- Neither fix is in N2b's frozen binary. N2b runs unchanged on its own copy.
- `runs/N1b.log` keeps the non-output events; untrimmed sha256 in
  `runs/N1b.run`. Terminal is the CLI's timeout (exit 5), an instrument
  finding by the rule: the run was stopped, not judged.

Reading: the second encounter proved the point it was for — Sensei's own
knowledge about itself reached routing as architectural authority — and was
then denied execution by two defects in the machinery that carries that
authority, both found by the run itself and both reproduced exactly by the
regressions on #109. A fourth and fifth instance of the shape the proof-v7
hypothesis names (a class collapsed into the first plausible one; a rule
replaced by a stronger-looking rule that is weaker on the cases it did not
name), both excluded from proof-v7 evidence as discovery instances.

### N2b — 04:12:33Z–04:20:09Z, exit 0, subject copy `6c36961` (n2b)

Route `architectural-authority-granted`: `internal/derived/derived.go
[mutation confinement]` — the Family 3 recipe the untold investigator
proposed in N2, derived at this base, carried into routing as the whole
authority for a one-file plan. The implementor ran. Candidate
`8f58b4c0783b` (diff digest `cb7c47a4…`, 2768 bytes, `runs/N2b.candidate.diff`):
an unexported positional constructor `newResult(Recipe, Outcome, string,
*Anchor)` with all five `CLI.Revalidate` exits routed through it. Terminal:
ACCEPT, `retained: accepted by review and unpublished; landing it is the
human's decision`. **Not admitted.** The copy is left as it stands.

Findings, in order of weight:

- **The verdict flipped on identical bytes and identical evidence, and the
  reviewer changed underneath it.** Cycle 1 (04:17:07, `via codex`):
  REVISE — the plan required a scoped Sensei edit check, validation recorded
  only gofmt/vet/build/tests, and the diff audit is `input_trust:
  caller_supplied`. Cycle 2 (04:20:08, `Claude takes the reviewer role in a
  session that inherits nothing`): ACCEPT, on the same digest, the same
  three-times-identical validation block, the same caller-supplied audit,
  and still no edit check — the cycle-2 review does not mention the proof
  cycle 1 refused on. Nothing the revision did addressed the finding: the
  diff did not change, the evidence did not change; the reviewer did. The
  run reports this as a candidate ready for admission. The instrument
  recorded every fact needed to see it; nothing in the engine compared
  them.
- **The inner cycle counter reset.** `candidate.changed … cycle 2` at
  04:17:48, then `… cycle 1` at 04:19:25 for the same candidate: the
  revision's counter restarts, so a reader of the label alone would count
  two candidates where there was one.
- **A recipe can route a run it cannot let record a decision.** At the end:
  `decision not recorded: no governing invariant to link the decision to,
  so it was not recorded; govern these files first` — on a file whose
  derived recipe just served as the run's whole architectural authority.
  Coverage-by-recipe and decision-recording-by-invariant are two vocabularies
  for "governed", and this file is in one and not the other.
- `production_deploy` reported `declared but not mechanically enforced`
  every cycle; unchanged from earlier series.

Reading beside N1b: the two second encounters are the two sides of the same
fact. Sensei's own knowledge of itself reached routing as architectural
authority in both. On `premise.go` the machinery carrying that authority
denied execution (two defects, #109). On `derived.go` it granted execution
and then accepted a candidate on a reviewer substitution that no rule
noticed. Neither is a Family finding; both are findings about what the
authority is connected to once it exists. The proof-v7 hypothesis predicted
contradictions *before* review; N2b's is the case it does not name — a
contradiction between two reviews, mechanical in every input, not checked.
Excluded from proof-v7 evidence as a discovery instance; it becomes a
prediction to freeze.

### Knowledge publication — 2026-08-28, after #108 merged at `fbc8870`

Both B3 invariants live on `:10122` (digest `42e6e12c…`, 164,506 triples,
authoritative): FUTURE_ONLY raised on `internal/derived/write.go` and
REPEATED_RESUME_CANNOT_MINT on `internal/workflow/testedit.go`, each with its
two exact required tests attached. Verification transcript on #108. B3
knowledge publication is closed as an observed fact; S1 remains blocked on
the mediator's gate, not on coverage.

## N3 and N4 — FROZEN at f8686b6 (slice 2, `docs/work/b3-n3-n4-and-remeasure.md`)

Freeze block, tasks byte for byte, instrument change (consumer = main at
freeze, `sensei-code-b3c`), graph `42e6e12c…` (post-#108), and the
`testedit.go`-now-OK disclosure are in that brief; `runs/N3.graph.metadata.pre.json`
is the graph identity. Owner decision recorded as M24.

### N3 — 17:49:12Z–18:02:57Z, exit 1, subject copy `6c36961` (n3)

- Preflight at start: `suppliedplan.go` EMPTY. The architect planned three
  files: `suppliedplan.go`, `engine.go`, `suppliedplan_test.go`.
- Routing: `derived coverage: 1 anchor(s) over 3 planned file(s); route
  architectural-authority-granted — internal/workflow/engine.go [lock
  discipline]` (N1's persisted `Engine.premises` recipe), plus the first
  M2.2 grant of this slice: `suppliedplan_test.go` beside `engine.go`
  (operational, printed beside coverage). **Prediction "cold → closure"
  failed**: the neighbour the architect added carried the anchor. No closure
  round opened, so the investigator was never asked; **no question was
  recorded** (`grep -c "recorded a question"` = 0; recipes unchanged at 3;
  no `derived_receipts.jsonl` written).
- Implement → validation pass → audit pass → Codex REVISE (f1 blocking:
  `restorePlanBound` discarded the paired digest on resume — an invariant
  the reviewer cited by name, REPEATED_RESUME_CANNOT_MINT) → cycle 2 →
  Codex REVISE (f1 major: `gofmt -w` demanded, `gofmt -l` executed — a
  validation-wording finding, not a code one) → cycle 3 identical bytes →
  reviewer substituted to Claude → ACCEPT on the same digest `5acb9363b801`
  → `review.contradiction` fired (#111's guard: *nothing changed but the
  reviewer*) → re-routed, Claude produced the identical diff again →
  `workflow.failed: no bounded implementor produced an acceptable
  candidate`; handoff created; candidate retained resumable. Not admitted.
- Preserved: `runs/N3.log` (sha256 in `runs/N3.run`, 4,562,556 bytes),
  `runs/N3.recipes-after.json`, `runs/N3.candidate.diff` (two commits on the
  task branch, sha256 prefix `5acb9363b801` = the reviewed digest).

Reading, not adjudicated here: (a) the change to `suppliedplan.go` was
authorized by an anchor on a *neighbouring* planned file; whether that
satisfies "derived coverage must be relevant" is a question for the owner,
recorded as such. (b) The #111 guard did its job: an ACCEPT that appeared only
because the reviewer changed did not complete the run. (c) The loop then
could not converge on a REVISE whose only remaining finding was a
`gofmt -w`/`-l` wording — the same shape as N1b's eight-turn loop, on a
different trigger. Discovery instance; not proof-v7 evidence.
