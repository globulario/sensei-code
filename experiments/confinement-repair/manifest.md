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
