# prospective-312 — the first supplied plan handed to sensei-code itself

Instrument: sensei-code `main @ 1090792` (PR #92 merged: `run --plan`).
Subject: sensei#312, the prospective-surface authority gap named by
`experiments/confinement-v2`. This is the first time sensei-code is given a
bound authored outside a run and asked to carry it out under its own governance.

## What is frozen before the run

- Plan: `.sensei/plans/312.json`, sha256 `d147f5c3d3556d16076494b85d54eb09e8ed62639ad30dbea4984fc77ad2ed63`, committed at the revision
  this note is committed at. The run's PlanProposed record must carry that
  digest; a different digest is a different experiment.
- Task, verbatim: *Implement prospective authority for new surfaces from
  sensei#312 under the supplied plan.*
- Invocation, verbatim, from a clean checkout of `main`:

      sensei-code run --task "Implement prospective authority for new surfaces from sensei#312 under the supplied plan." \
        --plan .sensei/plans/312.json --json --timeout 45m

- Predicate under test (from the plan): PROSPECTIVE_CREATE_ADMISSIBLE(F) iff
  A. parent dir holds a covered surface S; B. declared package == package(S) at
  the pinned world; C. F matches exactly one governed role (only
  `go-regression-test`, `*_test.go`); D. declared deps ⊆ imports(S) ∪ role
  allowance ({testing}). Otherwise UNRESOLVED → uncovered. Post-creation
  inspection REFUTES a created file that does not match; a refutation ends
  the run, it is not retried.

## Preconditions observed before invocation

- \`sensei-code doctor\`: MCP surface PASS (24 tools); providers connected.
- FAIL \`sensei:task_binding\`: \`task.defect.b165d8e2088a\` is bound to
  \`b571bac2\`, not HEAD. This is a doctor finding, not part of the \`run\`
  readiness gate, so it does not refuse the invocation; its consequence is
  that Sensei refuses \`task_briefing\` during the run and the implementor
  and reviewer work from \`awareness_briefing\`/preflight only. Repairing it
  needs a \`.sensei/project/graph.nt\` this checkout does not hold. Recorded
  as an instrument limitation of this series rather than repaired out of
  band; any reviewer verdict is read with that in mind.

## Stopping rule (frozen before invocation)

One invocation. Whatever it does is the result:

| outcome | recorded as |
|---|---|
| exit 0, candidate admitted | the lane carried a supplied bound to admission; the candidate is then reviewed by a human before any merge — admission is not merge |
| plan refused at routing (cannot establish / gap / human condition) | a finding about coverage of this repository's own `internal/workflow` region under a supplied plan; the plan is NOT reworded |
| reviewer escalates → run fails closed (`supply a revised plan`) | expected behaviour of #92; the escalation text is preserved verbatim and becomes the input to plan revision 2, which is a new digest and a new series |
| implementor cannot honestly implement | its blocker is preserved verbatim |
| exit 3 (awaiting human authority) | the question is preserved as asked and answered by the human out of band; the run is resumed, not restarted |
| exit 5 (timeout) / crash | instrument finding, recorded; no re-run under the same digest without a note revision |

No second invocation under this digest for any reason other than a resume of
this run. Anything learned goes into a revised plan with a new digest and a
new NOTE section, never into a silent re-try.

## Predictions

1. Routing: the plan names three files in `internal/workflow`, a region the
   awareness graph governs directly. Expected: authority established by the
   graph (not by derived coverage), route granted or a Level-3 condition.
   The same gap this plan repairs — a nonexistent `prospective.go` /
   `prospective_test.go` — may itself refuse the plan. That would be the
   crack demonstrating on the tool that fixes it; it is recorded, not evaded.
2. The reviewer sees `ARCHITECTURAL PLAN (supplied with the task, not
   architect-produced; sha256 d147f5c3d3556d16076494b85d54eb09e8ed62639ad30dbea4984fc77ad2ed63)`.
3. The recorded decision, if any, names the supplied plan by digest as
   `decided_by` and `human_authorization: none established`.
4. The riskiest clause for the implementor is D (reading S's imports at the
   pinned world via `git show`); a shortcut that reads the working tree is
   a REVISE the reviewer should catch.

## Result

(appended after the run; nothing above this line changes)
