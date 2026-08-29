# C3 — the confinement repair under governor X, isolated (FROZEN before any run)

Decisions: M27 (the repair), M28 (the corrected falsifier), M29 (measure the
exact planned set before freezing), and the owner's sequencing of
2026-08-29: option (b) identities, closed expected outputs, *void ≠ failed*.
C1 and C2 (PR #118) stand unchanged; `be3cdbe` is a retained specimen and
never X+1.

## Three identities, recorded separately

```text
governor    sensei-code @ f01592b0f0828605ed254047fc064f41dacc78f2
            binary sensei-code-shw1, sha256 7c0bd86ba2030666f577c9d0ef4dae550eff77a9f6eec01828edf25509c5baea   (the W1/C1/C2 binary, unchanged)
base        f01592b0f0828605ed254047fc064f41dacc78f2  -- the subject is materialised from X ALONE
candidate   a fresh descendant of X, produced in the worktree the governor creates
```

Nothing landing on `main` is any of these. #118 (`e1dd5456`), #119
(`3ac57143`) and any manifest/digest work advance the repository; they do
not become governor, base, or candidate authority. If the candidate is
admitted it becomes X+1 whose executable lineage is **X plus the governed
repair**, and the intervening non-governor commits are merged afterwards
into regions the governed diff does not touch.

## Isolation — materialised from X, not shared with the controller

```text
git archive f01592b0f0828605ed254047fc064f41dacc78f2 | tar -x   ->  fresh repository, git init, one commit
subject HEAD  63d4bd5f2f2aefc7bb715b59fc1a338c2609e844  (tree fe43d481424072f325a7f315f1a5cfa5ab70609f == X's tree)
remotes                     0
shared object database      none (no .git/objects/info/alternates)
refs                        refs/heads/main only -- no controller branch is reachable
controller documents        absent (X predates them: no docs/work/self-hosting-witness-1.md,
                            no docs/work/confinement-repair.md, no experiments/confinement-repair,
                            no experiments/c3-confinement)
seeded                      .sensei-code/config.json only (sha256 94ed3b1723e3f9bd…), untracked
```

This is the repair for C2's observed isolation failure, where the in-run
reviewer read controller commits through a shared worktree object store.
Corpus tooling stays external observation machinery and never enters the
subject.

## The plan

`experiments/c3-confinement/plan.json`, sha256 `50df0097b52a5611ec3b4a22a3fa64b74e50362982fa2477c7ec6bcd025a6934`, supplied byte
for byte via `run --plan`; no architect provider is consulted for it.

It differs from C2's in exactly the way C2's terminal record demanded:
**the confinement is re-bound to the candidate that reaches judgement.**
C2's reviewer refused, three times, a check placed only before
`e.validate` — validation runs formatters that may rewrite the candidate
and deliberately re-reads the diff, so a pre-validation check binds the
worker's bytes and not the reviewed ones. The plan therefore authorises
both call sites, and C2's fourth finding (the worker's *unplanned* second
check being refused) does not recur, because this plan names it.

Per-file preflight of the exact planned set, before the freeze
(`prefreeze/`, graph `42e6e12c…`, CURRENT):

```text
internal/workflow/engine.go        OK        anchors 3  indexed 1/1   examined
internal/workflow/testedit.go      OK        anchors 1  indexed 1/1   examined
internal/workflow/engine_test.go   DEGRADED  anchors 0  indexed 1/1   examined (coverage blind spots only)
internal/workflow/routine_test.go  EMPTY     anchors 0  indexed 1/1   examined (no governing rule)
```

## Expected artifacts — the protocol is closed over its own outputs

```text
runs/C3.run                       REQUIRED  identity stamp: governor commit and binary sha256 at start AND end,
                                            subject HEAD at start and end, plan sha256, config sha256, exit
runs/C3.log                       REQUIRED  the event stream, UNTRIMMED
runs/C3.receipts.jsonl            REQUIRED  preserved even when empty, with the reason it is empty
runs/C3.graph.metadata.pre.json   REQUIRED  graph identity at run time
runs/C3.candidate.diff            REQUIRED IFF a candidate was produced
```

> **Missing expected evidence makes the witness VOID, not failed.** A
> semantic failure inside a complete run is evidence — C1 and C2 are exactly
> that. An incomplete instrument record means the run cannot be adjudicated
> at all, and is recorded as a void alongside `W1.void1-operator` and
> `N1.void1-provider-quota`, not as an outcome of the experiment.
>
> **An empty receipts file is evidence. An absent receipts file is an
> instrumentation defect.** They never collapse into the same state.

## Falsifier (M28)

The governor may inspect, validate, audit and review candidate content, but
the executable governor and its authority-producing code remain pinned to X
for the entire run: governor source commit and binary sha256 recorded at
start and end must be equal, and no candidate-built `sensei-code` executable
and no candidate implementation of governance logic participates in
governing this run.

## Bootstrap rule (X still lacks the gate this run creates)

Candidate-path equality with this frozen plan is an admission precondition
for THIS run, checked by hand from the preserved diff. If the worker touches
even one path the plan did not name, the candidate is retained and **not
admitted**, and a revised plan is a new freeze. The known defect of X is
never used to admit the repair of that defect.

## Stopping rule and predictions

ONE invocation, `--json --timeout 30m`, `SENSEI_CODE_BENCHMARK=1`, derive
receipts moved aside; no opportunistic replanning; exit 3 preserves the
question; timeout, crash or provider void is an instrument finding.

- All four planned files are examined, so no `unexamined by the graph` line
  and no `bounded-knowledge-gap` on that account; route
  `architectural-authority-granted`.
- Level-1: **not routine** (a critical invariant governs `engine.go`).
- M2.2 may grant `engine_test.go` and `routine_test.go` beside their covered
  subjects; a grant is not a plan entry, so the candidate must still touch
  only the four named paths.
- Governor commit and binary sha256 at end equal those at start; the
  candidate commit differs from X.
