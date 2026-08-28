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
  hand-derivation detects only address-escape and load failure as
  UNRESOLVED; it has no detector for reflection, `unsafe`, or writes reached
  through an interface. Their absence from the scan is the absence of a
  detector, not evidence of their absence in the corpus. The envelope side
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
- `E2.log` keeps 20+ non-output events (191 dropped); untrimmed sha256 in
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

**Family 3 gate:** recipe reproduced the sealed verdicts (REFUTED /
DERIVED) — met. Full chain to execution consequence — met through coverage
and routing, not through execution; the missing link is the existing-test
coverage wall, not the family.
