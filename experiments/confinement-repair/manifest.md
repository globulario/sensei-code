# Confinement repair under governor X — FROZEN (before any run)

Decisions: M27 (the repair), M28 (the corrected falsifier; "not Witness 2").
Owner's go (2026-08-29): governor stays X = `f01592b`; `ba519f7` is the
evidence record, not a newly admitted executable governor.

```text
governor source     f01592b0f0828605ed254047fc064f41dacc78f2  (main at the #115 merge; admitted)
governor binary     sensei-code-shw1, sha256 7c0bd86ba2030666f577c9d0ef4dae550eff77a9f6eec01828edf25509c5baea  (same binary as W1; built from a clean worktree at X, go1.25.0)
subject / base      f01592b0f0828605ed254047fc064f41dacc78f2, detached clean worktree, seeded ONLY with .sensei-code/config.json (sha256 94ed3b1723e3f9bd…)
plan                experiments/confinement-repair/plan.json, sha256 42c8b8784b3c7bac87924f42d15c1775994a700ae82e7ce1db3e9e0e59f04dad, supplied byte for byte via run --plan
task text           the plan's summary, verbatim
Sensei              github.com/globulario/sensei-code at localhost:10122; producer sensei f79f96f9 (SENSEI_BIN); graph digest recorded per run
worker              Claude, invoked by the governor, confined to the candidate worktree
reviewer            the configured independent reviewer (Codex); a provider failure is an instrument outcome, never a substitute verdict
env                 SENSEI_CODE_BENCHMARK=1; derive receipts moved aside; --json --timeout 30m
stopping rule       ONE invocation; no opportunistic replanning; exit 3 preserves the question; timeout / crash / provider void is an
                    instrument finding; the candidate is retained, never admitted or merged by the run
```

## Bootstrap rule (X lacks the gate this run creates)

Candidate-path equality with the frozen plan is an explicit admission
precondition for THIS run, checked by hand from the preserved diff: if the
worker touches even one path the plan did not name, the candidate is
retained and not admitted, a revised plan is frozen, and the run repeats.
The known defect of X is not used to admit the repair of that defect.

## Falsifier (M28, corrected)

The governor may inspect, validate, audit and review candidate content, but
the executable governor and its authority-producing code remain pinned to X
for the entire run: governor source commit and binary sha256 recorded at
start and end must be equal; no candidate-built `sensei-code` executable and
no candidate implementation of governance logic participates in governing
this run (the candidate lives only in the worktree the governor creates and
is never built or invoked as a governor).

## Predictions

- Route `architectural-authority-granted` on graph anchors; all four planned
  files examined (none is newer than the graph), so no `unexamined` line.
- One worker cycle; validation, audit, Level-1 **not routine** (engine.go
  carries critical invariants; the candidate touches the governance path),
  independent review; ACCEPT or REVISE ordinary; `ready for governed
  admission` offered, not taken.
- The candidate touches exactly the four named files. If it does not, the
  bootstrap rule applies and the record says so.
- Governor commit and binary sha256 at end equal those at start; candidate
  commit ≠ X.

## C1 — 00:32:39Z–00:32:46Z, exit 1, refused at routing. No candidate, no worker.

```text
governor binary sha256  7c0bd86b…  at start and end   EQUAL
governor commit         f01592b…   at start and end   EQUAL
subject HEAD            f01592b…   at start and end; tree clean; no candidate worktree created
plan                    sha256 42c8b878… quoted by the governor
graph                   42e6e12c…, CURRENT
```

The governor refused the plan:

```text
derived coverage: 0 anchor(s) over 4 planned file(s); route bounded-knowledge-gap
  unexamined by the graph: 2 file(s): internal/workflow/testedit_test.go, internal/workflow/suppliedplan_test.go
workflow.failed: the supplied plan needs a revision the run cannot make: a bounded knowledge gap
  must be closed first: graph coverage is absent for planned file(s) the graph has not examined:
  internal/workflow/testedit_test.go, internal/workflow/suppliedplan_test.go.
  A supplied plan is not revised by the architect; supply a revised plan
```

**This is #115's rule firing for the first time in a real run**, and it did
exactly what it was built to do: the plan named two files the graph has
never examined, and the run refused rather than granting authority over
them on their neighbours' anchors. Verified against the live graph
(`42e6e12c…`) at the same moment:

```text
internal/workflow/testedit.go          OK        1 anchor          (REPEATED_RESUME_CANNOT_MINT)
internal/workflow/engine.go            OK        3 anchors
internal/workflow/routine_test.go      EMPTY     indexed 1/1       examined, no rule -> covered
internal/workflow/engine_test.go       DEGRADED  indexed 1/1       examined, no rule -> covered
internal/workflow/testedit_test.go     DEGRADED  indexed 0/1       "no files examined"  -> UNEXAMINED
internal/workflow/suppliedplan_test.go EMPTY     indexed 0/1       "no files examined"  -> UNEXAMINED
```

Which test files the graph has examined is graph state, not a rule: two of
these four are examined, two are not. The frozen plan put its regressions in
the two unexamined ones.

M2.2 also refused a grant for both, for its own reason ("no planned file in
its directory holds architectural coverage at the pinned world"), before the
coverage question was asked.

### Reading

Three facts, and none of them is "the loop is broken":

1. The confinement repair was **not implemented**; the plan was refused
   before any worker ran. The bootstrap rule is not yet in play — there is
   no candidate to check for scope-exactness.
2. The refusal is the **first live firing of the rule #115 added**, on a
   governed run, refusing a real plan. That is the observation W1 predicted
   but did not produce (all four of its files were examined).
3. The supplied-plan lane behaved as designed: it does not revise a frozen
   plan, it refuses and asks for a revised one.

### What a revised plan must satisfy — the owner's call, not taken here

Options, stated without choosing:

- **(a) Move the regressions into examined test files.** `routine_test.go`
  and `engine_test.go` are both examined at this graph; the four regressions
  M27 names could live there. Cheapest, and the plan's substance is
  unchanged — but it lets the graph's current coverage decide where tests go.
- **(b) Derive coverage for the two files first**, then re-freeze the same
  plan. Honest, and slow: it needs a family applicable to a test file.
- **(c) Publish an invariant naming those files** (as #108 did for
  `testedit.go`), which is a human-committed act, then re-freeze.
- **(d) Accept the refusal as the run's result** and leave the repair for a
  world where those files are covered.

Recorded, not decided. Whichever is chosen, the revised plan is a NEW freeze
with its own hash; this one stays as it is.
