# Self-Hosting Witness 1 — a known sensei-code governs the creation of its successor (brief, not evidence)

Per M26 (2026-08-28). Draft PR until the run's records are on the branch.

## What #115 was, stated so the transition is visible

`fix/unexamined-planned-file-is-not-covered` (#115) is **hand-governed
self-repair**. Sensei was the court throughout — `awareness_preflight`,
`awareness_briefing`, `awareness_edit_check`, `sensei propose`, `sensei
check` — and the repair cycle was adversarial (eleven exact-head Codex rounds
plus an owner-found P1, each reproduced before its fix). But sensei-code did
not own the change's lifecycle: no governed plan, no routing record, no
candidate worktree, no audit, no admission, no task session came from the
loop. The change that fixed the loop's authority routing was not routed by
it. Reason, not excuse: the consumer carried the defect being fixed, and a
judge does not certify the repair to its own gavel.

## Claim

One governed run in which an already-admitted `sensei-code` (main @ X)
consumes a frozen plan for a change to sensei-code, creates the candidate,
routes authority on Sensei's evidence, has the candidate implemented by a
worker confined to the candidate worktree, audited, independently reviewed
and brought to governed admission — **with the governor pinned at X for the
whole run**, never executing the candidate it is producing — and every step
leaves an immutable task/session record.

Falsifier: any point at which the running governor's binary, module path or
code reads from the candidate worktree; any authority in the record that
the candidate's own text or code supplied.

## Protocol (frozen at the freeze commit, not here)

```text
governor        sensei-code main @ X, built to a named binary, sha256 recorded; X is admitted (post-#115 merge)
subject         the same repository, at X, in a clean checkout the governor owns
plan            supplied, byte for byte, via the supplied-plan lane (run --plan, #92) -- no architect provider
                names the files, the change, and no relation; frozen and hashed
worker          Claude, invoked by the governor inside the candidate worktree only (CLAUDE.md worker rules)
evidence        Sensei at :10122, graph digest recorded; per-file probes as #115 defines
reviewer        Codex, session inheriting nothing, bound to the candidate digest
records         events, routing, coverage line (unexamined files named), grants, audit, review, admission,
                receipts (empty preserved), corpus entry with graph identity and BOTH shas: governor X and candidate
stopping rule   one invocation; exit 3 preserves the question; timeout/crash/provider void = instrument finding
```

Governor pinning is proven, not asserted: the governor binary's sha256 is
recorded at start and end; the candidate worktree path is recorded; the
run's `graph binding` and `candidate … from base` lines name X.

## What the change is

Chosen to be small, real, and already argued for: the Level-1 routine
classifier (`internal/workflow/routine.go`) still reads region-level
`Coverage.Proven()`; #115 recorded it as adjacent and unrepaired. The
supplied plan asks the loop to make it read the same per-file fact the
router reads. It touches the loop, it is bounded, and its falsifier already
exists in shape.

## Predictions (written at freeze)

- Routing on X: `routine.go` is examined; the plan's files carry anchors;
  route granted or, if a file is unexamined, `coverage-unexamined` with the
  file named — the first live observation of #115's rule inside a run.
- The worker's candidate is audited and reviewed; ACCEPT or REVISE is
  ordinary. Admission is offered, not taken by the run.
- The record answers: which version governed, which version was produced,
  and that they are different commits.

## After

X+1 (the admitted successor) becomes the governor of Witness 2. The chain
is the deliverable; one link is the witness.
