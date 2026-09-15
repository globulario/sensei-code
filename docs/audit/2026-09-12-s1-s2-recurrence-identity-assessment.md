# S1 and S2 assessed — what identity makes a finding "the same defect again"

**Date:** 2026-09-12
**Subject:** `docs/architecture/reviewer-teaches.md` slices S1 and S2.
**Status:** ASSESSMENT ONLY. Nothing implemented. S3 deliberately untouched.
**Reproduction case for S2:** sensei#352, whose repair is PR #351.

## 0. Result

Both slices as written rest on a premise that measurement contradicts.

| slice | as planned | measured |
|---|---|---|
| S1 — findings outlive their run | "persist the structured findings … cheap" | **already true.** 54 findings are durably persisted across 5 sessions. The gap is that nothing reads them back. |
| S2 — recurrence detection | "`finding.ID` … is too specific: the same defect gets a new ID each cycle" | **the opposite failure.** The review path's id is a per-verdict index: `f1` is shared by 18 distinct findings. It collides; it does not diverge. |

S1 is smaller than planned. S2 is not a clustering function at all.

## 1. S1 — the findings already survive

`Engine.emit` appends to the session store before publishing to the bus
(`internal/workflow/engine.go`), and review findings are emitted through it
(`engine.go:2501`, one event per finding carrying the whole struct). So a
finding is written to `.sensei-code/sessions/<id>/events.jsonl` at the moment it
is produced, and survives the process unconditionally.

Measured in this repository:

```
54 review.finding events, across 5 sessions
```

Each carries `id`, `severity`, `claim`, `reference`, `reason`, `correction`,
`proof_gap` — not a summary line.

**So `reviewer-teaches.md`'s S1 rationale is wrong**: *"today a finding dies with
the run that produced it"* and *"on its own it would have preserved #352's four
findings when the run failed."* Neither holds. The findings of a failed run are
on disk. #352's own findings are in GitHub review threads, which are also
durable — they were never in danger.

What is actually missing is a **reader**. Fifty-four findings are persisted and
nothing in the product consults them: no aggregation, no per-task retrieval, no
cross-run query. They are durable and unread.

That reclassifies S1 from "add persistence" to "read what is already recorded",
which is cheaper, and it moves the interesting work to S2 — because a reader is
only useful if it can say *these are the same defect*.

### One real defect S1 should fix

`reviewer-teaches.md` names `finding.Finding` as the type to reuse. That is the
wrong type. There are two:

| type | fields | what it is |
|---|---|---|
| `internal/finding.Finding` | ID, ObservationTask, World, Objective, Statement, About, Files, Source, ReadFiles | an **observation** that may become repair WORK; `Eligible()` gates it on evidence-bearing provenance |
| `internal/roles.Finding` | ID, Severity, Claim, Reference, Reason, Correction, ProofGap | a **reviewer's** objection to a candidate |

The 54 persisted findings are the second. The plan describes the first's fields
while proposing to solve the second's problem. Reusing `finding.Finding` here
would route reviewer objections through a type whose `Eligible()` contract exists
to decide whether an *observation* may open autonomous work — a different
question with a different authority answer.

## 2. S2 — every computable key was measured, and each fails differently

The question is not deduplication. **Zero of the 54 claims repeat verbatim.** A
text-equality key detects nothing, 0/54. So every candidate key below is a
*coarsening*, and the risk is the one the brief names: collapsing materially
different findings together.

### Key 1 — the finding id: an 18-way false merge

`roles.Finding.ID` is assigned per verdict, `f1`, `f2`, `f3`. Measured over the
54 persisted findings:

```
'f1'  shared by 18 distinct findings
'f2'  shared by 13
'f3'  shared by 10
'f4'  shared by  8
'f5'  shared by  5
```

Used as a class key this reports 18 recurrences of one class where there are 18
unrelated findings. The plan's diagnosis — "too specific, a new id each cycle" —
describes `finding.Finding.ID` and is inverted for the type that actually carries
reviews.

### Key 2 — the file or `Reference`: false-merges and false-splits at once

Measured on PR #351's 17 findings across 6 rounds. Hand-classified by the
predicate each asserts, then compared with a file key:

| file | findings | actual distinct predicates |
|---|---|---|
| `tasksession/governance.go` | 5 | **3** — receipt-identity binding, routing-to-owner, expiry |
| `tasksession/control.go` | 4 | 2 |
| `tasksession/session.go` | 2 | 1 |
| `taskcontrol/project.go` | 2 | 2 |
| `lifecycleaction.go` | 2 | 1 |
| `g3_round2_repro_test.go` | 2 | 2 |

Two failures in one key:

- **False merge.** `governance.go`'s five findings become one class. They are
  three different defects, and a "the contract or the suite is suspect" report
  fired on them would be wrong.
- **False split.** The clearest real recurrence on #351 is three findings that
  assert one predicate — *two fields of one published projection are computed
  from different evaluations, so the output simultaneously reports "no
  consumable capability" and "perform the mutation"*:

  | round | file | title |
  |---|---|---|
  | 1 | `control.go` | Recompute the cached projection after changing permission |
  | 3 | `session.go` | Recompute Prepare's next action with its permission |
  | 6 | `session.go` | Reconcile consumed capabilities before suggesting another apply |

  A file key splits this across two files and never reports it — while reporting
  the false merge above. It is wrong in both directions simultaneously, which is
  worse than being merely blunt.

### Key 3 — normalized statement similarity: unsafe at every threshold

Those same three titles share "Recompute" and "permission" and little else.
A threshold loose enough to cluster them also clusters the routing findings with
the receipt-binding findings, because both are dense in *consumption*,
*decision*, *admit*, *capability* — the vocabulary of the subsystem, not of the
defect. On this corpus there is no threshold that finds the real class without
merging two unrelated ones.

## 3. What identity actually is stable

The recurrence is at the level of the **predicate the finding asserts should
hold**. All three Key-2 findings assert one predicate. None of them share an id,
a file, or a phrase.

That predicate is not in the payload. It is, however, **already in the
reviewer's prose, and thrown away**:

> "Fresh evidence beyond the prior comment is this ungoverned positive-decision
> case and its contradictory `Next`"

> "Fresh evidence beyond the earlier Prepare finding is the supported same-task
> replay path"

Measured across both PRs:

| PR | findings | rounds | findings that explicitly declare they extend a prior finding |
|---|---|---|---|
| sensei#350 | 23 | 5 | 4 |
| sensei#351 | 17 | 6 | 4 |
| **total** | **40** | **11** | **8 (20%)** |

Twenty percent of findings carry the reviewer's own judgment that this is the
same thing again — a judgment strictly better than any inference we could
compute, because the reviewer read both findings. It reaches us as prose inside
`Reason`, is never parsed, and is discarded.

**So the answer to "what identity is stable enough" is: one the reviewer states,
not one we derive.** The smallest correct S2 is therefore not a clustering
function over existing fields. It is one field added to the review response
contract:

- `predicate` — the contract or property the reviewer believes is violated, in
  its own words, one line. This is the class key.
- optionally `extends` — the prior finding id this one continues, which the
  reviewer already supplies in prose 20% of the time.

Then recurrence is **counted, not guessed**: N findings naming one predicate
against one task means the contract or the suite is suspect, and that report is
as trustworthy as the reviewer, rather than as trustworthy as a similarity
threshold.

### Why this ordering matters

`reviewer-teaches.md` says S2 has the higher measured value, and that stands —
on #351, a declared-recurrence counter fires at round 3 and again at round 6. But
S2 cannot be built first: it needs the field, and the field needs a review
request that asks for it. The dependency the plan does not name is the **review
contract**, not S1.

## 4. Recommended revision to `reviewer-teaches.md`

1. **S1 shrinks** to "read the findings already persisted", and targets
   `roles.Finding`, not `finding.Finding`. Delete the claim that findings die
   with their run.
2. **S2 splits.** S2a: ask the reviewer to name the violated predicate and any
   finding it extends. S2b: count per predicate per task and report a suspect
   contract. S2a is a contract change; S2b is then trivial.
3. **No clustering function.** Every computable key was measured on 54 persisted
   findings and 40 PR findings. Each fails, and two fail in both directions at
   once. A heuristic here would fabricate recurrence and hide it.
4. **S3 unchanged and still not authorized.** Nothing above admits anything to
   the graph.

## 5. Provenance

- 54 findings counted from `.sensei-code/sessions/*/events.jsonl`, kind
  `review.finding`, on 2026-09-12; id distribution and verbatim-claim count
  computed over the same set.
- #350 and #351 finding counts, rounds and recurrence declarations read from the
  GitHub GraphQL `reviewThreads` API, first comment of each thread.
- Predicate classification of #351's 17 findings is the author's, from the full
  body of each finding. It is a judgment, and it is the one claim here that a
  reader should re-derive rather than accept.
