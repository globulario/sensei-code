# mutation-v2 — Family 3: mutation confined to its owner package

Restart of `mutation-v1` (closed inconclusive: contradictory selection rule,
and an authority surface that measured method style rather than ownership).
Same pinned fixture corpus, same graphs, same substrate.

> Is owner-controlled state mutated only through surfaces that possess
> authority to mutate it?

## The relation, frozen before any fixture is opened

```
state_mutation_confined_to_owner(T.F)

  subject     exported struct type T declared in package P, exported field F,
              with at least one statically resolvable mutation site in the
              repository (non-test .go); a field nothing writes is true and
              useless and is not a subject
  owner       the declaring package P
  authority   package P -- any function, method, or constructor in P

  mutation    a write to <e>.F where the static type of <e> is T or *T:
              assignment, op-assignment, ++/--; and taking &<e>.F

  DERIVED     every resolvable mutation site lies inside P
  REFUTED     at least one resolvable mutation site lies in a package Q != P
              -- a COUNTEREXAMPLE TO CONFINEMENT, not by itself an
              architectural defect: an exported structure may be
              intentionally caller-mutable; the relation being false says
              nothing about whether the design is wrong
  UNRESOLVED  mutation authority cannot be bounded statically: a field
              address taken outside P (write authority handed across the
              boundary and not followed), reflection, unsafe, an interface
              path the reader cannot resolve, or a package that could not
              be loaded
```

## Selection, global and independent, frozen before the tool ran

```
fixtures, fixed name order:
  1  github.com/golang/groupcache  @ 7477803   graph :10191
  2  github.com/golang/mod         @ 9c7e562   graph :10193
  3  github.com/golang/sync        @ 2efa68e   graph :10190

subjects: stable path order over non-test .go, declaration order within a
file, field order within a type -- one global sequence across the corpus

violated  = first natural REFUTED   with >= 1 mutation in the sequence
positive  = first natural DERIVED   with >= 1 mutation in the sequence
envelope  = first natural UNRESOLVED with a named reason in the sequence

each role searches the entire ordered corpus independently;
NO_SUBJECT only if that role is absent across the entire corpus;
no fixture is edited; nothing observed in mutation-v1 pre-selects anything
```

The sealed tool is `selection/mutscan/main.go` (sha256 in `selection.json`),
built with the go1.26.0 toolchain the corpus requires. It is sealed by the
commit that carries this manifest, **before** it is run; the scan is a later
commit. Every verdict is recorded; the sealed verdicts are pre-registration
of the mechanism's answer and are never shown to the investigator.

## Gate

Family 3 succeeds only if the Sensei recipe, designed after sealing,
reproduces the sealed verdicts on the sealed subjects, and the full chain
(question → recipe → derivation → coverage → routing → execution
consequence) is then observed on a governed task. A recipe rewritten until
the violated subject derives is a campaign failure, recorded as such.

## The scan (one run, tool built with go1.26.0, sealed at `5f72ab8`)

| fixture | subjects | DERIVED | REFUTED | UNRESOLVED |
|---|---|---|---|---|
| groupcache @ 7477803 | 55 | 55 | 0 | 0 |
| mod @ 9c7e562 | 111 | 107 | 4 | 0 |
| sync @ 2efa68e | 3 | 3 | 0 | 0 |

Global independent selection over the ordered corpus:

```
violated   mod         module/module.go   Version.Path
           counterexample to confinement: its 1 mutation site is
           modfile/rule.go:216 (package modfile), outside owner package module
positive   groupcache  http.go            HTTPPoolOptions.BasePath
           all 1 mutation site(s) inside the owner package (http.go:105)
envelope   NO_SUBJECT
```

Read plainly:

- The v1 "violation" (`HTTPPoolOptions.BasePath`) is v2's first positive
  subject: the constructor default is the owner package exercising its own
  authority. That is the counterexample v1 closed on, now on the right side.
- `module.Version.Path` written from `modfile` is a counterexample to
  confinement and is not called a defect here: `module.Version` is a plain
  exported value type that `modfile` fills in while parsing a `go.mod`; it
  may well be intentionally caller-mutable. The relation is false for it;
  whether the design is wrong is a separate, human question.
- Envelope `NO_SUBJECT`: no field address is taken outside its owner package
  anywhere in the corpus, and all packages load. **Tool limit, stated:** the
  hand-derivation detects only address-escape-outside-owner and load failure
  as UNRESOLVED; it has no detector for reflection, `unsafe`, or writes
  reached through an interface. **A further limit, found in review after
  sealing (5046864821):** an address taken *inside* the owner and returned
  or stored — `func Expose(x *T) *string { return &x.F }` — is counted as an
  owner-side site, although write authority can escape from it afterwards;
  the scanner does not follow where the pointer goes. The sealed tool is not
  changed: that would rewrite the experiment. The selected verdicts do not
  depend on it — `HTTPPoolOptions.BasePath` and `module.Version.Path` are
  direct assignments — but `NO_SUBJECT` for the envelope is **relative to
  the scanner's detectors**, not evidence that no such escape exists in the
  corpus. The envelope side
  of the triangle therefore has no natural specimen in this corpus under
  this tool, and none is fabricated; the recipe must still declare those
  limits in its own `Limits()`.
- Every verdict, including the 165 passed over, is in `selection.json`.

The recipe is designed after this point.

## The recipe, designed after sealing, and the gate

Sensei `feat/derive-mutation-confinement` (`eebe3d3a`):
`state_mutation_confined_to_owner`
(`golang/architecture/derive/mutationconfinement.go`). No type checker — a
derivation reads pinned bytes — so a written selector's receiver is bound
syntactically (receiver, parameter, `var`/`:=` with a stated or literal
type, `new(T)`, field chains through struct declarations parsed in scope,
qualified names through the pinned `go.mod`); a receiver it cannot bind is
the completeness boundary and is UNRESOLVED by name.

Gate, `sensei derive` from that branch against the sealed subjects at their
pinned commits:

```
violated   module.Version.Path         @ mod 9c7e562        REFUTED
           counterexample to confinement: 1 of 1 observable write(s) originate
           outside module: modfile/rule.go:216 in modfile
           (bound through the field chain f.Module.Mod across files)
positive   HTTPPoolOptions.BasePath    @ groupcache 7477803  DERIVED
           all 1 observable write(s) originate from the owner; http.go:105
```

Both sealed verdicts reproduced by a recipe written after they were sealed.
Controls, not sealed: `modfile.File.Module` DERIVED (as the tool said);
`modfile.Position.Line` **UNRESOLVED** — five writes whose receiver (`stmt`,
a value from a type switch) the recipe cannot bind, two it can, no
counterexample. That is the recipe's own envelope appearing on a natural
control: wider than the `go/types` hand-tool's, exactly where a syntactic
binder stops. It is not promoted to the sealed envelope role — that role was
`NO_SUBJECT` under the frozen rule and stays so — but it is the first
natural UNRESOLVED of the family and is recorded as such.

## E-series, drafted from the code, NOT FROZEN

Frozen only once the instrument exists on `main` (sensei #313 + sensei-code
#99 merged); the instrument SHA, the recipes-at-start state, and the
stopping rule are recorded then. Drafted now so the task text is written
from the code and not from the results.

Same shape as confinement-v1/v2: the investigator receives only the cold
gap and the source; the sealed relation is never in its context; it must
decide on its own that ownership of mutation is worth asking about, or ask
something else, which is preserved and reported. Recipes at start: 0
(cold), so encounter 1 proposes and encounter 2 may benefit (future-only
rule). The plan must stay inside `modfile/` for one derivation to cover it.

- **E1** — *`AddModuleStmt` accepts any string as the module path, so a
  `go.mod` can be written with a path `module.CheckPath` would reject.
  Return an error for an invalid path and leave the file unchanged, with no
  change in behaviour for valid paths.* (Written from `modfile/rule.go:206`;
  `modfile/rule_test.go` exists, so no new file is needed.)
- **E2** — *`AddComment` on a `File` whose `Syntax` is nil allocates a
  `FileSyntax` and then appends; `AddModuleStmt` does the same allocation
  inline. Factor the nil-`Syntax` initialisation into one place used by
  both, with no change in behaviour.* (Written from `rule.go:206–225`.)

Predictions, to be finalised at freeze: the region is `modfile/rule.go`;
`File.Module` derives DERIVED there (control above), so a proposed recipe of
this family over `modfile` can yield `1 anchor over 1 planned file → route
granted`; a plan that reaches `module/` as well is `1 anchor over 2 files`
and cold — recorded as the architect's planning, not the family. The
violated subject `module.Version.Path` cannot cover anything (REFUTED earns
no coverage by design) and is not the E-series target; it is the court's
answer if the investigator asks about it.

## E-series, FROZEN

Instrument pair, published and exact:

```
Sensei producer      f79f96f9faf542b73d5053bcf5e48603a68e2c74   built as sensei-f3 (SENSEI_BIN)
sensei-code consumer 267e82297970c579152624be3021ab8d5d2f5ed7   built as sensei-code-f3
target               golang/mod E-base 7deaa1e = 9c7e562 with docs/awareness/derived_recipes.json
                     emptied; every Go source byte-identical to 9c7e562 (the sealed world)
graph                :10193, unchanged
recipes at start     0
env                  SENSEI_CODE_BENCHMARK=1, derive receipts moved aside before each invocation
```

Tasks: **E1** and **E2** exactly as drafted above, byte for byte. E1 runs
first; E2 runs only if E1 leaves the family's question unasked, and never
as a retry of E1.

> **Disclosure (review 5046864821, after the runs).** This sentence and
> prediction 3 below contradict each other: this says E2 runs only if E1
> left the question *unasked*; prediction 3 says E2 is the next bridge
> encounter if E1 *did* record the recipe. The contradiction was present in
> the freeze commit `69494ef` before E1 ran. E1 recorded the recipe and E2
> was run on the prediction-3 reading. E2 is therefore **strong observed
> evidence, not a clean preregistered confirmation** of the bridge. The
> text is left as frozen; this note is the correction.

Stopping rule (frozen before invocation): one invocation per task; whatever
it does is the result; nothing altered between E1 and E2; every plan,
proposed recipe, derivation receipt, coverage line, route and terminal event
preserved as `E1.*` / `E2.*`; exit 3 preserves the question; timeout or
crash is an instrument finding. The investigator is never shown
`selection.json` or this section.

Predictions, finalised:

1. Encounter 1 is cold: `0 anchor(s)` → `bounded-knowledge-gap` on graph
   coverage → closure round. The investigator, told only that three kinds
   are answerable, may propose a `state_mutation_confined_to_owner` question
   over `modfile` — or a confinement/lock question, or nothing; each is
   preserved.
2. If it proposes this family over `modfile` with `File.Module` (or any
   field the tool sealed DERIVED there) the derivation writes a receipt and
   the recipe is recorded; by the future-only rule it cannot cover the run
   that proposed it, so E1 still routes cold or to the human (exit 3).
3. The bridge is then observed on the NEXT encounter over the same region:
   `1 anchor over 1 planned file → route granted` → implementor → candidate
   → review. That encounter is E2 if E1 recorded a recipe; if E1 recorded
   none, E2 is a second cold encounter and the bridge is not reached in this
   series — recorded as such.
4. A plan that reaches `module/` as well is `1 anchor over 2 files` and
   cold, recorded as the architect's planning, not the family.
5. A proposed question about `module.Version.Path` derives REFUTED, records
   a receipt, earns no coverage, and is preserved as the court's answer.

### E1 — 00:20:37Z–00:25:21Z, exit 3, base `7deaa1e`

- Plan: `modfile/rule.go` + `modfile/rule_test.go`, every claim
  evidence-bearing. `derived coverage: 0 anchor(s) over 2 planned file(s);
  route bounded-knowledge-gap` (graph coverage: 1 of 2 files indexed).
- Closure round 1: the investigator, told only that three kinds are
  answerable and never shown `selection.json`, proposed
  `state_mutation_confined_to_owner(File.Module in modfile, search .)` —
  *"AddModuleStmt mutates File.Module, while the requested error path must
  preserve it; repository-wide mutation ownership is worth verifying
  mechanically."* Outcome `RECORDED`, identity
  `state_mutation_confined_to_owner|modfile|file|module|.`. The family
  chosen untold; the subject is one the sealed tool marks DERIVED in this
  region (a v2 control, not the sealed positive, which lives in groupcache).
- Future-only rule: the recipe cannot cover the run that proposed it. Second
  routing: still `0 anchor(s)`; the gap did not close; escalated to the
  human with the question preserved: *Should Sensei derive coverage for this
  region and then reconsider the bounded change, or should the change remain
  deferred?* Exit 3.
- Predictions 1 and 2 held exactly. `E1.log` keeps the non-output events;
  untrimmed sha256 recorded in `runs/E1.run`'s sibling note below;
  `E1.recipes-after.json` and `E1.receipts.jsonl` preserved.

Encounter 2's base: the recorded recipe committed into the fixture as
`989c621` (recipes: 1, the investigator's own; Go sources still byte-identical
to `9c7e562`). E2 runs next as the second encounter over `modfile`, per
prediction 3. Its plan's shape decides the bridge: the family's deriver reads
non-test files only, so a plan that touches `rule_test.go` again reads
`1 anchor over 2 files` and stays cold — recorded as the plan's shape if so.

### E2 — 00:26:15Z–00:32:13Z, exit 3, base `989c621` (recipes at start: 1, the investigator's own)

- Round 1 plan: `modfile/rule.go` alone. **`derived coverage: 1 anchor(s)
  over 1 planned file(s) — modfile/rule.go [mutation confinement]`.** The
  investigator's recipe from E1 derived DERIVED at this base, the consumer's
  relevance gate mapped the family to *mutation confinement*, and the
  planned file was covered: coverage 1/1 for the first time in Family 3.
  Route `bounded-knowledge-gap` — not for coverage, but for one `inference`
  claim: *the modfile package tests are proportionate regression proof for
  this local refactor.* Premise receipt `gap-…-1` issued.
- Round 2 (closure): the inference was **replaced by evidence** — the
  architect read `rule_test.go`'s `TestAddOnEmptyFile` and cited it; no
  `[NEEDS EVIDENCE]` claim remained. But the re-plan now named
  `modfile/rule_test.go` as well: `1 anchor over 2 planned file(s)`, route
  `bounded-knowledge-gap` on graph coverage — a genuinely distinct gap,
  receipt `gap-…-2` (kind coverage-absent), not a paraphrase of `-1`. The
  closure round proposed and recorded a second question of the family,
  `state_mutation_confined_to_owner(File.Syntax in modfile, search .)`.
- The coverage gap did not close within budget; escalated with the question
  preserved: *Should Sensei mechanically derive coverage for File.Syntax
  mutation ownership and then reconsider this refactor?* Exit 3.
- `E2.log` keeps 25 non-output events (191 dropped); untrimmed sha256 in
  `runs/E2.run`; `E2.recipes-after.json` holds both recipes;
  `E2.receipts.jsonl` the round-2 receipt.

## Result

Two invocations, nothing altered between them, rule frozen before E1.

**Established — the Family 3 bridge through coverage into routing.** On a
foreign repository, in a natural governed task, told nothing:

```text
investigator question        state_mutation_confined_to_owner(File.Module in modfile)   [E1, untold]
→ deterministic derivation   DERIVED at the pinned base                                  [E2 round 1]
→ relevance                  mapped to "mutation confinement" by the consumer's closed switch
→ architectural coverage     1 anchor over 1 planned file
→ routing consequence        the route no longer turned on coverage; it turned on one
                             inference premise, which one closure round replaced with evidence
```

That is question → recipe → derivation → coverage → routing, the chain
Family 2 showed, now shown for a composition family whose deriver relates
state identity, ownership, mutation sites and the authorised boundary. The
sealed verdicts held throughout; nothing was tuned.

**Not reached — execution.** The implementor never ran. When the closure
round answered the regression-proof premise it did so by adding the existing
test file to the plan, and the family's deriver reads non-test files only, so
`rule_test.go` can never be its subject: `1 anchor over 2 files` is not
coverage, by tested design. Family 2 met the same wall at `test.bash`; #312
opened it for *new* test files (prospective surfaces); an *existing* test
file edited alongside a covered source file is still uncovered by every
family. That is the finding this series produced about the instrument, and
it is structural, not a defect in the family: a governed change that
responsibly edits its own test cannot earn coverage from a source-only
derivation. It is recorded, not evaded, and it names the next piece.

**Also observed.** #98's premise receipts on a natural run: two distinct
gaps got two receipts, and no paraphrase bought a round. The investigator
proposed the family twice on its own (`File.Module`, then `File.Syntax`),
each recorded with a distinct scope-bearing identity (#99).

**Family 3 gate, as frozen** (question → recipe → derivation → coverage →
routing → execution consequence), classified link by link:

```text
sealed verdict reproduction        MET
natural question generation        OBSERVED   (E1, untold; E2 round 2 again)
future-only boundary               OBSERVED   (E1 could not cover its own run)
derivation                         OBSERVED   (DERIVED at 989c621)
coverage                           OBSERVED   (1 anchor over 1 planned file, mutation confinement)
routing consequence                OBSERVED   (coverage no longer blocked the route)
execution consequence              NOT OBSERVED (the implementor never ran)

FULL FROZEN FAMILY-3 GATE          OPEN
```

Not a Family 3 failure: the family's semantic mechanism works through
routing. What is open is the full system execution gate, and the reason is
the existing-test wall below. E2's trace is direct evidence of every link it
reached; it is not a preregistered confirmation (see the disclosure under
the frozen rule).

## E3, proposed — NOT FROZEN until M2.2 (#101) is merged

The witness for the full gate is E2's own task, byte for byte: it is the
task whose plan naturally reached `rule_test.go`, and re-running it on an
instrument carrying M2.2 asks exactly one new question — does the existing
test's edit grant let the plan route on `rule.go`'s architectural authority
alone, and does the implementor then run? Nothing else changes: fixture
base is E2's end state (E1's and E2's recipes both persisted, `File.Module`
and `File.Syntax`), graph `:10193`, same env. Instrument SHAs, the base
commit, and finalised predictions are written here at freeze time.

Predictions to finalise then: round 1 plan `rule.go` (+ `rule_test.go`) →
`derived coverage: 1 anchor over 1 architectural file [mutation confinement]`
+ `operational authority (existing-test edit): 1 file` → route granted (or a
closure round on an inference premise, then granted) → implementor →
post-edit inspection of `rule_test.go` against its grant → audit → review →
terminal. A refutation at inspection (novel import, package change) ends the
run and is the result; an inspection pass followed by review is the FULL
Family 3 gate.

## E3 — FROZEN

```
producer      sensei      f79f96f9faf542b73d5053bcf5e48603a68e2c74   sensei-f3 (unchanged from E1/E2)
consumer      sensei-code d6fcd11cfc6327e00e5feb84979dbce520554e35   sensei-code-e3 (#101 M2.2 merged)
fixture       golang/mod  3b6be68 = 9c7e562 + both investigator recipes persisted
                          (File.Module from E1, File.Syntax from E2); Go sources byte-identical to 9c7e562
recipes       2, both the investigator's own; nothing sealed, nothing authored
graph         :10193, unchanged · env SENSEI_CODE_BENCHMARK=1 · derive receipts moved aside before invocation
task          E2's text, byte for byte
```

Stopping rule: one invocation; whatever it does is the result; every plan,
grant, coverage line, route, candidate, inspection, review and terminal
event preserved as `E3.*`; exit 3 preserves the question; timeout or crash
is an instrument finding. Nothing is altered after this section is
committed. The investigator is never shown `selection.json` or this file.

Predictions, finalised before invocation:

1. Plan `modfile/rule.go` + `modfile/rule_test.go` (as E2's rounds ended)
   or `rule.go` alone. With both: `derived coverage: 1 anchor(s) over 2
   planned file(s) [mutation confinement]` **plus** `operational authority
   (existing-test edit): 1 file(s): modfile/rule_test.go` — the two kinds
   printed side by side, never summed — and the coverage question, asked
   over `rule.go` alone, is answered. Route is granted unless an inference
   premise opens a closure round first (E2 round 1's shape), in which case
   one round then granted.
2. `TestEditGranted` recorded with `rule_test.go`'s base hash, package
   `modfile`, its imports at `3b6be68`; the worker's prompt carries both
   grant kinds under separate headings.
3. Implementor runs (the first time for Family 3). Post-edit inspection of
   `rule_test.go`: passes if the edit stays inside its imports, package and
   constraints; a novel import (the M2.1-era shape) is `test edit refuted:`
   and terminal — recorded as the result, not retried.
4. If inspection passes: audit → validation → independent review →
   terminal. `workflow.completed` closes the FULL Family 3 gate; a REVISE
   cycle is ordinary; a refutation or refusal names the next structural
   boundary.
5. A plan that reaches `module/` reads `1 anchor over 2 architectural files`
   and is cold — the architect's planning, not the seam.

### E3 — 01:18:35Z–01:22:17Z, exit 0, ACCEPTED, base `3b6be68`

- Architect, one turn (01:18–01:19): plan **`modfile/rule.go` alone** —
  *Centralize lazy File.Syntax initialization for AddModuleStmt and
  AddComment without changing observable behavior.* Every claim
  evidence-bearing; no inference premise; no closure round.
- `derived coverage: 2 anchor(s) over 1 planned file(s); route
  architectural-authority-granted — modfile/rule.go [mutation confinement]
  ×2`: both investigator recipes (`File.Module` from E1, `File.Syntax`
  from E2) derived DERIVED over `rule.go` at `3b6be68` and were mapped to
  *mutation confinement*. Granted on the first route.
- Claude, 18 s: a 1,016-byte candidate, `rule.go` +9/−5 — an unexported
  `(*File).syntax()` lazy initialiser used by both methods; mutation order
  preserved. Broker validation: vet/build/test pass (`gofmt -l cmd internal`
  infrastructure-failure identical against base, as in every run on this
  fixture). Sensei diff audit `pass`, 1 file, 0 findings.
- Independent review (Codex, fresh session, bound to `bcfe2cac6b51`):
  **ACCEPT** — *"implements the planned single lazy File.Syntax initializer
  and preserves both public methods' mutation order and behavior … source
  inspection confirms equivalent initialization and subsequent mutations;
  validation includes passing go test ./..."*
- `decision.recorded`: not recorded (no governing invariant on
  `rule.go`) — correct. Candidate retained, unpublished, **not admitted**:
  landing it is the human's decision. `workflow.completed`.
- `E3.log` keeps 31 non-output events (1,173 dropped); untrimmed sha256 in
  `runs/E3.run`; `E3.candidate.patch` is the accepted diff.

## Family 3 gate — CLOSED

```text
question                 investigator, untold, proposed the family twice (E1: File.Module; E2: File.Syntax)
recipe                   both recorded with scope-bearing identities; neither sealed, neither authored
derivation               DERIVED at the pinned base, by the recipe written after sealing
coverage                 2 anchors over 1 planned file [mutation confinement]
routing consequence      architectural-authority-granted, first route, no closure round
execution consequence    implementor ran; candidate; validation; audit pass; independent review ACCEPT

FULL FROZEN FAMILY-3 GATE    MET   (E3, sensei f79f96f9 + sensei-code d6fcd11c, golang/mod 3b6be68)
```

Read plainly. The chain the frozen gate named ran end to end on a foreign
repository, on a natural task, with the investigator told nothing, and the
only architectural authority the route rested on was the family's own
derivation. The sealed verdicts were never touched. Prediction 1 held on
its second branch (`rule.go` alone); 3 and 4 held; 2 and 5 did not arise.

**What E3 did not witness.** The plan named no test file, so M2.2's
existing-test edit grant was never issued: `no test-edit authority` events
absent, no `testedit.granted`. The seam is implemented and falsified in
unit tests; its first natural witness is still owed — it arises whenever an
architect's plan reaches an existing test beside a covered subject, as E2's
did. The gate closed without it because the architect's plan this time did
not need it; that is the architect's variance, recorded, not a property of
the seam.

**Not established.** Correctness of the candidate beyond reviewer acceptance
and green tests; that the refactor is wanted by golang/mod's maintainers
(not asked); anything about compounding beyond two recipes.
