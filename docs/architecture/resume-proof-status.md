# Governed resume loop — proof status

**As of 2026-09-13.** This record exists so the state cannot later be
misremembered as either *unproven* or *fully proven*. It is neither.

The implementation is complete enough for review and strongly proven at the unit
and mutation level. The **positive live path has never been observed.** That is a
missing observation, **not a failure** — nothing has refuted it, and nothing has
confirmed it either.

## Status table

| claim | state | evidence |
|---|---|---|
| unit / behavioural witnesses | **PASS** | 20 witnesses in `internal/workflow/authority_resume_identity_test.go` and `cmd/sensei-code/resume_test.go`; 45/45 packages; `go vet` clean |
| mutation proof | **PASS** | **25 relevant mutants killed, all compiling.** Four survived a first pass and are killed only because the tests they indicted were replaced |
| live legacy unsafe-resume refusal | **PASS** | On a constructed legacy fixture, `authorize` and `revise` refused with the typed error; durable log byte-identical afterwards, `0` `authority.resolved` appended; `stop` admitted |
| live positive durable-answer → same-task continuation | **NOT YET OBSERVED** | no fresh task has reached a human-owned boundary since the repair |
| W1 complete (durable answer, no process alive) | **NO** | the answer is still supplied at invocation; §4's `human_authority` exchange kind is unbuilt |

## What the repair consists of

| element | where |
|---|---|
| durable authority identity: `Scope`, `ScopeRecorded`, `TaskID`, `SessionID` | `DeferredAuthority`, written by `awaitChoice`, restored by `resumeAuthority` |
| `Covers` unchanged | it refused correctly; the defect was upstream of it |
| single classifier for all three authority endings | `terminateAuthorityOutcome`, used by `execute` and `resumeAuthority` |
| human Stop distinguishable | `errStoppedByHumanAuthority` sentinel; terminal `WorkflowStopped`, behavioural status `"stopped"` |
| unprovable record authorizes nothing | `UnprovenAuthorityScopeError`, gated at the rendezvous and again at the command; only `Stop` admitted, named positively |
| refusal preserves the question | `preserveQuestion`; a `WorkflowFailed` refusal would have set `done` and consumed the question by refusing it |

Published as **PR #168** at `9d0ec16`.

## Why the positive proof is absent

Not for want of trying, and not because anything failed.

| attempt | task / session | outcome |
|---|---|---|
| 1 | `task-1789268226865788944` / `session-20260913T025706.865642692Z` | completed with an independently reviewed candidate. **`0` `authority.required`** — routing did not require human authority |
| 2 | `task-1789271570536676368` / `session-20260913T035250.536530677Z` | **invalid proof attempt.** 7 events, refused at the workspace-cleanliness gate before architect or routing. Evidence about neither the presence nor the absence of a boundary |
| 3 | `task-1789271874329246617` / `session-20260913T035754.329087330Z` | architect **reached** (`agent.started` → `agent.finished` → `architect.spoke`), and correctly established the requested change already exists: commit `9fb4dbb` separates git stdout from stderr in `internal/ghbridge/snapshot.go`. No plan, so no routing, so **`0` `authority.required`** |

Attempt 3's objective was byte-for-byte the one that escalated on 2026-09-07
(`sha256 6391ab3472e3669d`). It could not escalate again because **the work had
since been done** — verified independently at `snapshot.go:45-46`.

**So the reason the positive proof remains absent is that no naturally
outstanding task has yet reached the repaired authority boundary.** The objective
that historically escalated is exhausted as a proof vehicle, and rephrasing it to
force an escalation would manufacture the condition the proof is supposed to
observe.

## The rule this record protects

> A demonstration is obtained by finding a task that genuinely needs doing and
> whose region genuinely escalates. It is never obtained by choosing a task
> *because* it would escalate. Constructing the condition destroys what the
> observation was for.

## What the escalating conditions actually are

Measured from every `workflow.awaiting_authority` payload recorded in this
repository:

| condition | occurrences |
|---|---|
| `blind spots in the planned region: file path under high-risk directory` | 2 |
| `a blind spot this router has no reading for: anchors present, no high-risk category fired` | 2 |
| `graph coverage is absent for the planned files` | 2 |
| `requires approval for this change class: human_approval_required (blast radius cluster)` | 1 |
| `requires approval for this change class: review_required (blast radius service)` | 1 |
| `requires approval for this change class: review_required (blast radius node)` | 1 |

Two families: an unreadable coverage/blind-spot condition, or a change class whose
blast radius demands approval. A task in a region reporting neither will not
escalate, however architecturally interesting it is — which is exactly what
happened to the `engine.go` retry task in attempt 1.

## When this record may be updated to PROVEN

Only on observing, on one governed task, all of:

```
scoped human-owned boundary reached
  -> durable deferral, question persisted with ScopeRecorded=true and a non-empty Scope
  -> no process alive
  -> a human supplies the answer
  -> the SAME task resumes
  -> the answer Covers the re-derived question
  -> the settled question is NOT asked again
  -> execution continues under the original task identity
```

and, separately, for Stop:

```
durable question -> human Stop -> authority.resolved -> WorkflowStopped
  -> no execution, provider, awareness proposal, worktree, candidate, branch or
     GitHub mutation
```

Until both are observed, this document says **NOT YET OBSERVED**, and that phrase
must not be rewritten as either "unproven" or "closed".
