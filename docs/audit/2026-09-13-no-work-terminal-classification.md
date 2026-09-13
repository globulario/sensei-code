# A successful no-op is reported as an admitted change

**Date:** 2026-09-13
**Status:** ASSESSMENT ONLY. Nothing repaired. Deliberately **not** patched into
PR #168, which is the resume/authority repair.
**Observed on:** `task-1789271874329246617` / `session-20260913T035754.329087330Z`

## 0. The invariant under assessment

> **A successful no-op determination must not be reported as if a new change was
> admitted.**

It does not currently hold at the exit-code boundary. It does hold in the receipt.

## 1. What was observed

A governed run asked for work that was already done. The architect reached a
correct conclusion and said so:

> This change is already present: commit `9fb4dbb` separates git stdout from
> stderr in `internal/ghbridge/snapshot.go` … No new implementation work should
> start.

Verified independently: `9fb4dbb` exists, and `snapshot.go:45-46` sets
`cmd.Stdout` and `cmd.Stderr` separately.

The run then reported:

| fact | value |
|---|---|
| plan proposed | **0** |
| routing performed | **none** — no `plan.proposed`, so `routePlan` never ran |
| candidate | **none** (`candidate.changed` 0) |
| review | **none** (`review.started` 0) |
| receipt | **`INCOMPLETE / UNREVIEWED`** |
| terminal event | **`workflow.completed`** |
| exit code | **0** |

`exitCompleted` is documented in `cmd/sensei-code/run.go` as meaning a change was
admitted. Nothing was admitted.

## 2. Do the contracts distinguish the three cases?

| case | meaning | terminal event | receipt outcome | exit |
|---|---|---|---|---|
| **A** | work admitted successfully | `workflow.completed` | `ACCEPTED` | **0** |
| **B** | findings observed, nothing admitted | `workflow.observed` | — | **6** |
| **C** | requested work already satisfied / no work required | **`workflow.completed`** | `UNREVIEWED` | **0** |

**B is fully distinguished.** It has its own event kind, its own exit code, and
`run.go` explains why in as many words: *"An audit that finds three real defects
and admits nothing has succeeded, and a caller that cannot tell the two apart will
either treat every audit as a no-op or go looking for a change that was never
supposed to exist."*

**C is not.** It has **no terminal event of its own** and **no exit code of its
own**. It reuses A's, and the argument quoted above applies to it verbatim — a
caller will go looking for a change that was never supposed to exist.

The distinction survives only in the receipt (`UNREVIEWED` + `CandidateNone`),
which is one layer below what a process boundary can see.

## 3. Where C is produced

`internal/workflow/engine.go`, the `decision.Decision == "reply"` branch:

```go
if decision.Decision == "reply" {
    e.emit(... event.ArchitectSpoke, decision.Message, decision)
    e.notePlanAbsent(taskID)
    e.emitRunTerminal(taskID, event.WorkflowCompleted, event.SourceSystem,
        runreceipt.OutcomeUnreviewed, runreceipt.CandidateNone, "", nil)
    return
}
```

The branch is honest about itself — it calls `notePlanAbsent` and states
`CandidateNone`. The loss happens when that is projected onto `WorkflowCompleted`,
because `exitFor` maps that kind to `exitCompleted` unconditionally.

Note also that the branch is **wider than C**: `"reply"` covers every
conversational architect answer, including "here is the answer to your question".
So C is not merely unnamed, it is conflated with a second distinct case.

## 4. Consumers, and what each would be misled into

| consumer | depends on | what goes wrong |
|---|---|---|
| **`cmd/corpus`** (evidence corpus) | records `Terminal["kind"]` from `workflow.completed` and `Terminal["exit"]` from the run stamp | a no-work determination enters the campaign's own evidence as an admitted change. **This is the sharpest consequence: the measurements used to judge the system would overcount admissions.** |
| **external CI / automation** | exit 0 is the universal "success, proceed" | a pipeline proceeds to a step that expects a candidate branch, a diff, or a review verdict, and finds none. The failure surfaces later, attributed to the wrong step |
| **`audit-repair`** | branches on `code != exitObserved` | not affected today (it gates the observation phase), but it is the in-repo precedent showing exit codes are used as control flow, so the risk is structural rather than hypothetical |
| **`sensei-code resume --list`** | returns `exitCompleted` for a read-only listing | a **second instance of the same conflation**, introduced by this session's own work. A listing admits nothing either |

**Not currently affected:** this repository's CI. `phase1-codex-task.yml` and
`phase1-pr.yml` invoke `go run ./cmd/sensei-code context`, not `run`, so no CI
step branches on `run`'s exit code today. The exposure is latent here and live for
any external automation.

## 5. The gap, stated

C has no semantic terminal state. Specifically it lacks:

1. a terminal event kind distinct from `workflow.completed`;
2. an exit code distinct from `exitCompleted`;
3. a receipt outcome naming the determination rather than its absence —
   `UNREVIEWED` says *no review happened*, which is true of C but is also true of
   a run that failed before review. It describes a missing step, not a reached
   conclusion.

A fourth, smaller gap: `"reply"` conflates "no work is required" with "here is a
conversational answer". Those have different consequences for a caller and should
not share a terminal.

## 6. What a repair must NOT do

- **Must not report C as a failure.** The architect was right and the run
  succeeded; a non-zero code meaning "broken" would teach the behavioural record
  that this task shape breaks — the same error `terminateAuthorityOutcome` was
  written to stop for a human Stop.
- **Must not widen `exitObserved`.** B is the read-only audit lane, structurally
  fixed at submission time. C happens in the governed lane. Collapsing them would
  destroy a distinction that is currently correct.
- **Must not be inferred from the receipt at the boundary.** The exit code is the
  process's own claim and must be right on its own.

## 7. Recommendation

A separate, small change: one terminal kind and one exit code for "the requested
work was already satisfied", emitted from the `"reply"` branch when the architect
determines no work is required — and kept distinct from a conversational answer.
`cmd/corpus` then records it as its own terminal rather than as an admission.

Out of scope for PR #168, and not to be bundled with it.
