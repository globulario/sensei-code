# Defects 1 and 2 — live proof, recorded before the graph was refreshed

**Date:** 2026-09-13
**Status:** IMMUTABLE EVIDENCE. Written before any graph refresh, so the
measurements below describe a graph state that no longer exists after it. This
record is not to be rewritten once the graph changes.

## The run

| | |
|---|---|
| task | `task-1789312504879458635` |
| session | `session-20260913T151504.879304458Z` |
| effective code base | `f304cfec6f4384c6d286251a465604864f3dcd79` |
| domain-rendering fix applied after | `9cb4e0f414c0e0fac18c33b54c09a909028993c0` |
| stack | #168 `9d0ec16` ✓ · #171 `185d849` ✓ · #172 ✓ · #169/#170 out |
| objective | byte-identical to every prior W3 attempt, `sha256 4b38691f5025bc38` |
| events | 216 |
| terminal | `workflow.failed`, receipt `COMPLETE / FAILED`, exit 1 |

## Defect 1 — PASS

`coverage-unexamined` no longer reaches a human.

```
15:19:11  files=8   route=bounded-knowledge-gap     <- the one allowed narrowing round
15:24:12  files=10  route=bounded-knowledge-gap     <- round returned; coverage still absent
15:24:12  knowledge-limited: no actor reachable from a governed run can examine …
15:24:12  workflow.failed
```

| measurement | value |
|---|---|
| `authority.required` | **0** |
| `workflow.awaiting_authority` | **0** |
| human question created | **none** |
| final `Basis` | **`2` = `BasisLacksKnowledge`** (iota: 0 unclassified, 1 protects-value, 2 lacks-knowledge) |
| final `Route` | `bounded-knowledge-gap` — never `RouteHuman` |
| candidate / worktree / validation / review / PR | 0 / 0 / 0 / 0 / 0 |

Missing files named by the stop:

- `internal/ghbridge/exchange.go`
- `cmd/sensei-code/control_test.go`
- `docs/architecture/durable-exchange-migration.md`

Remedy reported (`Routing.Closes`), verbatim as emitted:

```
graph examination of internal/ghbridge/exchange.go, cmd/sensei-code/control_test.go,
docs/architecture/durable-exchange-migration.md; the supported operator action is
`sensei import --refresh /home/dave/Documents/github.com/globulario/sensei-code
 --domain f304cfec6f4384c6d286251a465604864f3dcd79` (add --store-url and
--graph-marker-file to reload the served store). This is reported, not performed:
a governed run must not change the graph it is governed by, and extraction writes
candidates for review rather than promoting them.
```

**The `--domain` argument in that emitted string is WRONG** and is preserved here
exactly as it was produced. `disposeUnclosedGap` passed `routing.Gap.World` — the
world the gap was measured in, a commit sha — so the command could not have run.
Repaired in `9cb4e0f`: the domain now comes from `start.Domain()`, and
`knowledgeLimitRemedy` refuses to emit a bare hex string as `--domain` at all.
The test that let it through asserted only that `--domain` appeared, not that what
followed was a domain — the same shape as the mutant written to catch exactly this,
aimed at the wrong half of the argument.

## Defect 2 / #171 — PASS

| measurement | value |
|---|---|
| closure receipts issued | **1** — `gap-1789312504879458635-1` |
| opening gap scope | 2 files |
| closure rounds granted | **1** (`closureBudget = 1`) |
| plan scope across the episode | **8 → 10** |
| receipt after the scope change | **unchanged** — no second id exists anywhere in the log |

The scope change it bound against was a **widening**. The narrowing direction
remains unit-proven only; this run did not exercise it.

## Graph untouched throughout

Triples **35268** before and after. Marker digest
`c0b660fc42a50c4be4741592de178dfff3216c9b873d455c37cc864e27f01705`, triple_count
35268, both unchanged by the run. The only repository writes were the derived
artifacts `docs/awareness/derived_receipts.jsonl` and `derived_recipes.json`. No
branch, no remote ref, no comment on mailbox PR #157.

## What this proof does NOT establish

- **positive live resume proof: NOT YET OBSERVED.** This run is not resume
  commissioning and must not be read as it. `Covers` has still never been
  exercised on a re-derived condition matching one already settled.
- W3 itself did not advance: no plan event, no candidate, no code.
- Coverage is still not acquirable from inside a governed run. This proof is that
  the impossibility is now reported honestly, not that it was removed.

## Two follow-ups recorded, deliberately not repaired here

1. **Stale authority-question lifecycle.** No truthful state exists for
   "superseded / no longer actionable". `AuthorityResolved` claims a human
   answered; `WorkflowCompleted`/`WorkflowFailed` claim the task finished or
   failed; `WorkflowStopped` attributes human withdrawal and is not terminal to
   `FindInterrupted`; `rendezvous.Abandon` is control-plane role turns, unreachable
   from the authority path; sensei's governed-abandon is PR #350, parked. The
   question from `session-20260913T041020.292922276Z` therefore still stands.
2. **Knowledge-limit terminal semantics.** `BasisLacksKnowledge` currently produces
   `workflow.failed` / `FAILED` and exit 1. Nothing failed — the graph could not
   see. Whether a knowledge limit deserves its own non-failure terminal, especially
   for the behavioural record and for automation, is an open assessment. It is the
   same family as the no-work/`exitCompleted` gap in
   `2026-09-13-no-work-terminal-classification.md`.
