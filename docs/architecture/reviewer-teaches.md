# The reviewer as teacher, not only as gate

**Question asked:** must the durable-exchange refactor land first, or can the
reviewer be enhanced now?

**Answer: no dependency. Build this now.** The loop is entirely DOWNSTREAM of a
consumed review response. Nothing in it waits on an external actor, so it adds
no waiter, no exchange, no wake. It is local, synchronous work that happens
after `review.completed` has already been received.

It also *reduces* exposure to the gap the refactor closes: today a finding dies
with the run that produced it. sensei#352 burned four cycles rediscovering one
predicate, then the run failed and the findings evaporated.

## What already exists

| piece | state |
|---|---|
| `event.ReviewFinding`, emitted per finding with the struct | ✅ |
| `finding.Finding` — `ID` stable per claim/place/revision, `Statement`, `About`, `Files`, `Source`, `ReadFiles` | ✅ |
| `roles.Finding`, `taskstate.Finding` — persisted with the task | ✅ |
| `sensei propose --kind failure_mode\|forbidden_fix\|invariant` — writes YAML, stages, stops | ✅ |
| **anything connecting findings to propose** | ❌ |

One missing edge, not a subsystem.

## The authority boundary, first

A review verdict is recorded as `advisory ... (session unverified): satisfies no
adversarial-review obligation`. An advisory finding must NOT become a governing
rule by itself. Promotion into the graph is a knowledge-admission act and stays
human-authorized.

So the target is:

```
finding  ->  DRAFT awareness entry, staged, never committed
         ->  human reviews and commits
```

`sensei propose` already has exactly this shape: it appends, rebuilds, reloads,
stages, and stops. Nothing new is needed to keep the boundary; it is inherited.

## Three slices, smallest first

### S1 — findings outlive their run
Persist the structured findings of a terminated run into the task's durable
record, with their bindings (task, request, candidate digest, reviewer provider).
Cheap, no authority questions, and on its own it would have preserved #352's four
findings when the run failed.

### S2 — recurrence detection
`finding.ID` is stable "at the same revision", which is too specific: every cycle
has a new candidate, so the same defect gets a new ID each time. Needs a coarser
CLASS key — `About` plus a normalized statement, or an explicit class the
reviewer names.

Then: N findings of one class against one task means the CONTRACT or the SUITE is
suspect, not the candidate. Report it; do not spend another cycle.

On #352 this fires at cycle 2. It would have saved most of a day.

### S3 — propose the law
For a recurring class, emit a DRAFT `failure_mode` (or `forbidden_fix`) bound to
the finding's files, carrying its statement as the contract and the review
request ids as evidence. Staged for a human, exactly like every other proposal.

## What NOT to build

- No autonomous admission. A finding never becomes an active invariant unaided.
- No new waiter, exchange kind, or wake path. This is downstream of a response.
- No new Finding type. Three exist; reuse `finding.Finding`.
- No cross-repository or cross-campaign learning yet. One task, one class, one
  draft. Widen only after the narrow case earns it.

## Ordering against the durable-exchange work

Independent, and S1 makes the refactor's job easier by removing one more thing
that dies with a process. If only one ships, S2 has the higher measured value:
today's cost was not the findings being wrong — every one was correct — it was
nothing noticing they were the same finding four times.
