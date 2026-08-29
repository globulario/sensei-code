# Confinement repair — the candidate-vs-plan gate, governed by X (brief)

M27 item 7, executed under M28's corrected falsifier and the owner's go of
2026-08-29. The defect: governor X has no deterministic gate confining a
candidate to the files its plan named; the only detector is Level-1
condition 8, a fast-path condition, and W1 reached "ready for governed
admission" with a candidate three files wider than its plan.

The change (frozen plan: `experiments/confinement-repair/plan.json`): after
the worker's diff is read and before validation, audit, review or admission,
every changed path must be one the plan named; any other path refuses the
candidate; the check takes no grant input, so M2.2 grants cannot enlarge
the set; a supplied plan is refused, not revised. Four regressions named in
M27. Governor, subject, plan hash, bootstrap rule, falsifier and predictions
are in `experiments/confinement-repair/manifest.md`.

If the candidate is scope-exact, passes audit and review, and the governor
stays pinned, it is the first candidate eligible to become X+1 (owner's
admission). Not called Witness 2.

## C1 RESULT — refused at routing, no candidate

The governor refused the frozen plan: two of its four files
(`testedit_test.go`, `suppliedplan_test.go`) are unexamined by the graph, so
`route bounded-knowledge-gap` with `unexamined by the graph: 2 file(s)` and
`workflow.failed: … supply a revised plan`. Governor stayed pinned; no
worker ran; no candidate exists; the bootstrap rule is not yet in play.

This is the first live firing of the rule #115 added — refusing to grant
authority over files the graph never examined, inside a real run. The repair
itself is not implemented. A revised plan is a new freeze; the options are
listed in the manifest and the choice is the owner's.

## C2 RESULT — scope-exact candidate, no convergence, no X+1

Governor stayed pinned (commit and binary sha256 equal at both ends);
candidate `be3cdbe` touched exactly the frozen four files; `EXIT 1 —
no bounded implementor produced an acceptable candidate`. The repair is not
established and `be3cdbe` is retained, never X+1.

Why: a confinement check placed only before `e.validate` does not bind the
candidate that reaches judgement, because validation's formatters may rewrite
it and the diff is deliberately re-read afterwards. The lesson is
**authority must bind the exact candidate state that reaches judgement**. The
worker's attempt to add an unplanned second check was refused by the
frozen-plan/review boundary — which proves the run did not improvise itself
into success, and does not prove X already had the confinement gate being
repaired. Preserved as evidence, not worked around. A
separate design finding (two representations of the same predicate,
`notPlanned` vs `confineToPlannedFiles`) is recorded, not fixed here. And the
reviewer was observed reading controller commits through the shared worktree
object store: C2 supports no blind-review claim.


`C2.log` is committed as ordered parts (`C2.log.part-001`, `-002`) because
the exact-head Sensei gate failed closed on a stream larger than the gRPC
message ceiling. Same bytes, same order; concatenation reproduces the
recorded sha256 `130e2fdf…` (6,359,622 B); the corpus generator reads parts
as one stream. The verifier was not modified and nothing was exempted.
