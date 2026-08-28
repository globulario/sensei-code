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
