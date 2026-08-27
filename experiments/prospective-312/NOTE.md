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

- `sensei-code doctor`: MCP surface PASS (24 tools); providers connected.
- FAIL `sensei:task_binding`: `task.defect.b165d8e2088a` is bound to
  `b571bac2`, not HEAD. This is a doctor finding, not part of the `run`
  readiness gate, so it does not refuse the invocation; its consequence is
  that Sensei refuses `task_briefing` during the run and the implementor
  and reviewer work from `awareness_briefing`/preflight only. Repairing it
  needs a `.sensei/project/graph.nt` this checkout does not hold. Recorded
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

### B1 — operator precondition failure, not a governance result

19:58:06Z–19:58:16Z, exit 1. The run refused at the start gate: *the canonical
checkout has uncommitted changes*. The uncommitted file was the run's own
timestamp stamp, written into `experiments/prospective-312/runs/` by the
operator before invoking. No task identity was established, no routing ran,
the supplied plan was never read past the objective. The gate was right.
Preserved in `runs/B1.jsonl` (7 events). Not counted against the stopping
rule, which governs the first invocation that reaches routing; the plan and
its digest are unchanged. B2 writes its outputs outside the tree.

### B2 — the governed system refused the supplied bound at routing

19:58:42Z–19:58:53Z, exit 1. Preserved in `runs/B2.jsonl` (11 events).

What happened, in order:

1. Objective recorded as *submitted unattended with an externally supplied
   plan; no human presence was established* — prediction 3's provenance held.
2. Preflight on this repository: `PREFLIGHT_STATUS_EMPTY`, `UNKNOWN_IMPACT`,
   `anchors=0 files=0 indexed=0` — thin coverage, as predicted for a plan
   naming files that do not exist yet.
3. `the plan was supplied with the task (sha256 d147f5c3…); the architect is
   not consulted for it, and it is routed as any plan is` — the lane engaged
   exactly as #92 specified, with the frozen digest.
4. `derived coverage: 0 anchor(s) over 3 planned file(s); route
   bounded-knowledge-gap`.
5. Authority by lane: three claims `[evidence-bearing]` (source: repository);
   the fourth — *a single closed role with a single novel allowance is
   sufficient to let the confinement-v2 natural task reach the candidate
   boundary* — `[NEEDS EVIDENCE]` (source: inference). *The objective does not
   establish any of these. Evidence does.*
6. `workflow.failed`: *the supplied plan needs a revision the run cannot make:
   a bounded knowledge gap must be closed first: the plan rests on an
   unverified premise about gosumcheck/main_test.go … A supplied plan is not
   revised by the architect; supply a revised plan.*

**Reading.** Every refusal was correct. The plan's author (me) put a
*prediction* — that one role suffices — into `claims`, honestly labelled
`inference`. The router treated an inference the plan rests on as a bounded
knowledge gap, which for an architect's plan opens a closure round, and for a
supplied plan is terminal by #92's own design. So the first thing the
governed system did with a bound handed to it was to refuse the one sentence
in it that was a hope rather than a fact. Nothing about prospective authority
was reached; the refusal is upstream of it.

**Finding about the lane** (not a defect, a cost now observed): the supplied
lane cannot close a bounded gap, so a supplied plan must carry *no*
inference-sourced premise at all. Either the author establishes the premise
before supplying, or moves it out of `claims`. That constraint was implicit in
#92; B2 made it explicit. It is the right constraint — a supplied plan is a
bound, not a hypothesis — and it should be documented on `--plan`.

**Stopping rule applied.** Plan revision 1 is not re-run. Revision 2 (series
C) removes the inference from `claims` — it stays in this note's Predictions
where it belongs — and changes nothing else. New digest, new series.

## Series C — plan revision 2

Plan: `.sensei/plans/312.json`, sha256 `4e4165005627fb7847d0b165b50b81cad5774ef10c8d8c32663b3fe291b6b2f7`. Diff from revision 1: the
single `source: inference` claim removed from `claims`; every other byte of
the plan's text, steps, files, mode and invariants unchanged (the file is
re-serialised, so the digest covers formatting too). Same task, same
invocation, same stopping rule. Prediction added: with no inference premise,
the router's next answer is about coverage of three files that do not exist
in a region the graph holds thin — the crack this plan repairs, possibly
refusing the plan that repairs it.

### C1 — the supplied bound reached admission's doorstep and the reviewer sharpened the design

Started 19:59:54Z. Preserved in `runs/C1.jsonl` (worker/reviewer `output` events trimmed as in confinement-v2).

- Routing: `architectural-authority-granted`. Prediction 1's pessimistic branch did
  not occur: with no inference premise, the graph's direct invariants over
  `internal/workflow` certified the region although derived coverage was
  `0 anchor(s) over 3 planned file(s)` and preflight was thin. Candidate cut
  from `29322a2`, clean.
- Reviewer packet: the plan heading carried *supplied with the task, not
  architect-produced; sha256 4e416500…* (prediction 2 held).
- Cycle 1 (Claude → `6bbbdb4c21d4`): audit pass, validation pass (gofmt, vet,
  build, test — executed by the broker). Codex, fresh session: **REVISE**, one
  blocking finding — ordinary derived coverage was computed over absent
  planned files before the prospective rule, so an anchor naming an absent
  path could cover it *without* declaration, role, package, dependency or
  post-creation checks. "An authority bypass, not a test-only defect." This
  was a hole in the frozen plan's own wording: it said absent files are
  covered *only* prospectively but never said ordinary coverage must be
  denied to them first.
- Cycle 2 (Claude → `98524291c9e0`): the first finding resolved (existence
  partitioning). Codex: **REVISE**, two new blocking findings, both citing the
  Sensei principle `meta.silence_is_not_valid_for_unexpected`:
  1. a failed read of F was treated as proof F is absent — so a read failure
     could *grant* prospective authority. Existence must be tri-state; only a
     positively established MISSING may enter authorization.
  2. post-creation inspection re-read the authorization facts instead of
     inspecting against facts persisted at grant time, and nothing survived
     Resume. The prospective grant must be a durable receipt — F, covering S,
     package, dependency envelope, role, world identity — restored exactly on
     resume and inspected against.
- Cycle 3: in progress at the time of this draft.

**What this established.** The governed loop, given a bound authored outside
it, produced the next two semantics #312 needs on its own, and both are the
same constitutional pressure #92 just went through: absence is not safety
(read-failed ≠ missing) and a predicate survives restart (the grant is a
receipt, not a recomputation). #312 has moved from "authorize a nonexistent
file" to "issue a bounded, receipt-backed authorization to create a surface
under facts established in world W, then verify the created surface against
exactly that authorization."

**Cycle 3 (Claude → `2e35d0bee112`, 20:14:44): REVISE.** Both cycle-2 findings
resolved — tri-state existence (`present / errNotAtWorld / unclassified`) and
prospective grants recorded as a `ProspectiveGranted` event, carried on
`session.Interrupted`, restored on Resume. One new blocking finding: the
refutation was returned as an ordinary `runCandidate` error, and `implement`
treats every such error as a recoverable worker failure — so a refuted
candidate would be handed to the next implementor, violating the plan's
no-retry rule. Claude's cycle budget was spent by this REVISE and the ordinary
handoff ran: *Codex is continuing the existing candidate, not starting over.*

**Cycle 4 (Codex → `4bb26994d3d0`, 20:18:55): ACCEPT, by Claude as the
independent reviewer** (the provider swap kept the reviewer independent of the
implementor). The refutation now goes through `fail()` and stops;
`TestAProspectiveSurfaceRefutationStopsBeforeHandoff` proves a second
implementor is never invoked. The reviewer confirmed each clause of the
predicate against the plan by digest, and stated its own limits verbatim:
Sensei's evidence was *thin rather than contrary* (preflight EMPTY,
`anchors=0`, audit pass with 0 findings), and it could not re-run preflight
per file — MCP blocked in its session and the CLI refused on an endpoint
mismatch (`localhost:10120` vs configured `:10122`). Its three "minor findings"
are affirmations, not defects.

Terminal: `workflow.completed — candidate ready for governed admission`,
exit 0 at 20:18:57Z. Sensei diff audit `pass`, digest `1da3645c…`, 8 files,
0 findings. No pull request (push not granted). **Not admitted, not merged**:
the report itself says admission was not requested and reviewer acceptance
is not proof.

Candidate: worktree `.sensei-code-worktrees/task-1787860802380664460`,
branch `sensei-code/task-1787860802380664460`, base `29322a2`. Three commits
(`0b814ed`, `e4a8265`, `6a785a7` — cycles 1–3) plus Codex's cycle-4 change
**left uncommitted in the worktree** (`engine.go`, `engine_test.go`). The
accepted digest is over the diff, so the review is bound to the right bytes,
but the candidate is not a revision — the same custody gap
[[commit-the-candidate-before-handing-off]] names. 8 files, +941/−8:
`prospective.go` (311), `prospective_test.go` (415), `engine.go` (+173),
`event.go`, `store.go`, `suppliedplan.go`, `engine_test.go`,
`derivedcoverage_live_test.go`.

`runs/C1.log` keeps all 77 non-`output` events; 13,458 worker/reviewer
`output` events (5,033,561 bytes untrimmed, sha256
`6558f11be377a646c7d8590cf0e17e176568314d23aeced92b714633d8f1e168`) are
dropped, original preserved outside the repository.

## Result

- **Milestone 1 exercised for real.** A bound authored outside the run was
  validated, digested, routed, implemented, audited, reviewed across four
  cycles and two implementors, and accepted — with provenance `supplied` on
  every surface a reviewer or a resume reads. No architect was consulted at
  any point.
- **The governed loop sharpened #312 on its own.** Three successive review
  findings, each a constitutional law made executable: ordinary coverage must
  be *denied* to absent files before prospective evaluation; `read failed` is
  UNKNOWN, never MISSING; a prospective grant is a durable receipt restored
  exactly on resume; a refutation is terminal, not a worker failure. None of
  these were in the frozen plan. #312 is now: *issue a bounded,
  receipt-backed authorization to create a surface under facts established
  in world W, then verify the created surface against exactly that
  authorization.*
- **Predictions:** 1 wrong in the good direction (route granted, not
  refused); 2 and 3 held; 4 (working-tree shortcut for S) did not occur — the
  implementor read `git show <world>:S` from cycle 1.
- **Not established:** correctness of the candidate beyond reviewer
  acceptance and green tests; Sensei graph evidence for the new files
  (thin); the natural (architect-declared) path — no architect ran, so
  whether an architect will emit `prospective_surfaces` is untested and is
  what Series B of #89 will show.
- **Instrument findings:** the last implementor's work is left uncommitted
  in the candidate; the reviewer could not reach Sensei per-file (endpoint
  mismatch in its session); a supplied plan cannot carry an inference
  premise (B2).

Next: human review of the candidate diff, then admission as a PR from the
candidate branch — a human decision, not this run's.
