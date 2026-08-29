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
