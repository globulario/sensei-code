# T01 — Whole-Transition Architectural Assessment: Governed Task Abandonment

**Date:** 2026-09-10
**Subject:** `globulario/sensei` PR #350 (`fix/governed-abandon-transition`)
**Reviewed head:** `42475333dd22165c12f144491fcb1bc22760096c`
**Status:** ASSESSMENT ONLY — uncommitted, no repair performed, no PR state changed.

## 0. Scope and boundaries

This document records an architectural assessment triggered by the owner's
standing stop condition. It is a judgment for owner ruling, not a work product
of a repair round.

Explicitly NOT done, and not authorized by this document:

- No modification to PR #350, its branch, or its head.
- No reply to, or resolution of, the four new review threads.
- No merge, no rebase, no force-push, no branch change in either repository.
- No awareness-graph publication.
- No change to F5, to sensei-code#167, or to any previously recorded T01 artifact.

The reviewed head is not present in the owner's working clone
(`/home/dave/Documents/github.com/globulario/sensei`, at `3b6e710b`). Every code
fact below was read from a clone that holds `42475333` exactly, verified clean at
that revision. Line references are to `42475333`.

## 1. The reviewed head and its verdict

The verdict is finding-bearing. All three owner-specified facts were read
together at one moment:

| Fact | Reading |
|---|---|
| 👍 reaction on trigger comment `5611543948` | **absent** (`total_count: 0`) |
| Trigger issued while head was `42475333` | **yes** — comment at `02:02:00Z` |
| Head still `42475333` at read time | **yes** — `mergeable: true`, `state: open` |

Connector review `5161924047`, bound to `42475333dd`, submitted `02:09:48Z`,
carrying **4 findings: 2×P1, 2×P2**.

| # | Sev | Location | Title |
|---|---|---|---|
| R5-1 | P1 | `completion/inspect.go:150` | Reject result transitions after abandonment |
| R5-2 | P1 | `completion/abandon.go:517` | Validate the abandoned event payload before trusting it |
| R5-3 | P2 | `completion/abandon.go:398` | Reconcile HEAD after a durable append |
| R5-4 | P2 | `tasksession/pointerlock.go:70` | Reclaim locks left by dead pointer writers |

Each was independently verified against the source at the reviewed head. None is
a false positive.

## 2. Cumulative review history

**23 findings across five finding-bearing reviews, following four repair rounds.**

The owner's framing was "four rounds, 23 findings." The measured decomposition
is five *reviews* and four *repair rounds*: R1 was the initial review, R2–R5 each
followed a repair round, and R5's findings are unrepaired. Both statements
describe the same 23 findings; the distinction matters only because it names
which review has no repair behind it.

| Review | Head | Findings | Severity | Repaired? |
|---|---|---|---|---|
| R1 `5156919704` | `c96aced7` | 5 | 4×P1, 1×P2 | yes (round 1) |
| R2 `5158236350` | `29a12987` | 5 | 3×P1, 2×P2 | yes (round 2) |
| R3 `5159371057` | `b804a1e9` | 5 | 3×P1, 2×P2 | yes (round 3) |
| R4 `5161676163` | `25f3869b` | 4 | 3×P1, 1×P2 | yes (round 4) |
| R5 `5161924047` | `42475333` | 4 | 2×P1, 2×P2 | **no** |
| **Total** | | **23** | **15×P1, 8×P2** | |

Findings did not converge. P1 density across rounds: 4, 3, 3, 3, 2. Four repair
rounds reduced the P1 rate by roughly one third and never reached zero.

## 3. Why the stop condition fired, and why round five is forbidden

The owner's standing condition: *a P1 at the next exact head ends iterative
repair and triggers a whole-transition architectural assessment — not a fifth
round.* Two P1s landed at the exact head. The condition is met without
interpretation.

The condition is also correct on its merits, for a reason visible only in
aggregate:

- **R4-4** (P2) was "Reconcile a durable append before returning failure."
- **R5-3** (P2) is "Reconcile HEAD after a durable append."

These are the same branch, found twice, by two consecutive reviews. Round four
repaired that branch in response to R4-4 and the repair introduced a second,
independently discovered defect in the same code. That is the signature of
repair without a driving test, and it is the empirical case against round five:
another local patch to `abandon.go` would be the fifth attempt to fix a
distributed protocol at one of its call sites.

A fifth round is forbidden here not because four is a policy limit, but because
two of the four surviving findings are defects of *agreement between components*
(§4.1, §4.2) that cannot be repaired inside `abandon.go` at all, and a third
(§4.4) is a defect in a dependency `abandon.go` merely consumes.

## 4. Six-dimensional analysis

### 4.1 Lifecycle — broken at the exit, not the entrance

`AbandonTask` refuses to abandon a session that recorded a result transition
(`abandon.go:206-214`, the tri-state guard added in round 3). Nothing enforces
the converse:

- `resultrecording.RecordTransition` verifies chain identity and performs a CAS
  on the expected head (`record.go:88-151`) and **never asks whether the chain is
  already abandoned**. A caller supplying the post-abandonment head passes CAS
  and appends `result_transition_recorded` after a terminal state. The package
  contains zero references to abandonment or to the transition table.
- `InspectTerminalState` classifies abandoned+completed and abandoned+abandoned
  as `TerminalContradictoryHistory`, but abandoned+result_transition as a clean
  `TerminalAbandoned` (`inspect.go:143-152`, `classifyAbandoned` at `:170`) —
  even though `classifyTerminalFacts` already computes `resultTransitionCount`
  and `classifyAbandoned` receives the whole `terminalFacts` struct.

The resulting projection claims abandonment (no result produced) while carrying
a current result binding. The exclusion is known in exactly one of the three
places that must agree on it.

A terminal state that defends only the edge *into* itself is a label, not a
state.

### 4.2 Receipt binding — asymmetric between the two terminals

`loadAbandonmentReceipt` (`abandon.go:507-...`) is rigorous about the *artifact*:
it checks the artifact reference exists, re-reads the file, recomputes and
compares the digest, unmarshals, calls `ValidateAbandonmentReceipt`, requires a
self-declared receipt digest, and recomputes that digest.

It is silent about the *event payload*. `ParseTaskEventPayload` accepts the
shape; only `entry.Entry.Task` is matched afterward. A payload whose `task_id`,
`session_id`, `task_phase`, or `status` disagrees with the ledger entry it sits
in, or with the receipt hanging off it, is accepted and its artifact reference
trusted.

The completed path has `completedEventMatches(event, receipt, ref)`
(`inspect.go:196`). The abandoned path has no sibling. A second terminal was
added beside an existing one without lifting the shared binder to cover both.

### 4.3 Durability and reconciliation — the wrong API, over a permissive gate, on an undriven path

`ledger/reconcile.go:19-23` states in its own doc comment that
`ReconcileDerivedState` *"is the correct recovery for a durable-entry-but-stale-HEAD
condition (RebuildProjections alone does not repair HEAD)."*

The `ErrEntryDurable` branch (`abandon.go:375-402`) calls `RebuildProjections`.
`VerifyCtx` then treats the resulting head mismatch as a warning rather than an
invalidity, so `final.Valid` stays true, the path proceeds to retire the active
pointer, and returns `OutcomeCommitted` while `HEAD.yaml` remains stale.

The codebase names the correct API, in a comment, at the call site's own
dependency, and the code selects the other one.

**M4b connection.** During round four this branch was disclosed as carrying a
surviving mutation: removing `verifyDurableAbandonment` from the `ErrEntryDurable`
path changes no test outcome. That disclosure identified the branch as
unprotected by the suite. R5-3 then found an independent recovery defect in that
same branch. The disclosure was not a cosmetic coverage gap — it correctly
predicted that code added there would not be checked by anything. Two rounds
have now added logic to a path no test drives end to end.

This is the strongest single argument for decomposition: the durable-recovery
condition needs an owner with a fixture that produces it, before any further
logic is written into it.

### 4.4 Projection — not consulted for the abandoned terminal

`report.ProjectionState` is passed to `classifyUniqueCompleted` and is not passed
to `classifyAbandoned`. An abandoned task's projection state is never consulted
when classifying its terminal. Combined with §4.3 — where a stale HEAD is
reachable and reported as committed — the abandoned terminal's derived view is
the least verified of the three terminals.

### 4.5 Pointer ownership — cannot survive process death

`tasksession/pointerlock.go` implements a directory-as-mutex: `os.Mkdir` to
acquire, `os.RemoveAll` in a deferred release, `pointerLockWait = 5s` bound.

If a process exits between the mkdir and its deferred release — kill, panic
past recover, power loss — the directory persists with no owner. Every later
`Prepare`, `WriteActivePointer`, and abandonment replay then waits five seconds
and returns `ErrPointerLockHeld`, permanently, until a human deletes repository
state.

The comment at `pointerlock.go:39-42` anticipates precisely this: *"a stale lock
directory left by a killed process must not hang every later task forever."* The
bound satisfies only the first half of that sentence. It converts an unbounded
hang into a permanent failure; it is not recovery.

The consequence compounds with §4.3. `abandon.go`'s residue message advertises
*"rerun to finish — the durable record is idempotent."* That rerun reaches
`clearPointer` → `RetireActivePointer` → `acquirePointerLock`. In the scenario
that produces residue — a process dying mid-transition — the advertised
idempotent recovery is the one operation that cannot run.

### 4.6 Authority — sound, with two standing caveats

`resolveAbandonmentAuthority` and the awareness triple (grant, domain, mutation
path) are correct as written, and R4-1's domain/mutation-path binding defect was
repaired.

Two caveats, neither a new finding:

1. The authority model governs *who may abandon*. It says nothing about *what
   may follow abandonment*, which is §4.1's blind spot expressed one layer up.
2. The awareness entries remain authored-but-unpublished. No run can reach them.
   Policy that is present but unreachable does not govern.

## 5. Measured enforcement fact: the nine lifecycle append sites

`closureprotocol.AllowedTaskTransitions` declares the lifecycle law, including
`PhaseAbandoned: {}`, `PhaseStale: {}`, `PhaseUncertifiable: {}`,
`PhaseRefused: {}`, and `PhaseCompleted: {PhaseRevoked}`.

`ValidateTaskTransition` is its only enforcing reader. It has **exactly one
non-test caller in the production tree**: `completion/abandon.go:293` — the
guard added by this PR.

Nine packages call `store.Append` with a task lifecycle event. The raw count is
not the finding; the classification is. Each site is classified below by whether
it *declares a task phase* in its `TaskEventPayload`, since that is what makes an
append a lifecycle transition rather than an annotation of one.

**Group A — phase-bearing producers. These MUST pass through a common transition
authority. Four sites; one complies.**

| Site | Event type | Phase written | Uses gateway |
|---|---|---|---|
| `completion/abandon.go:350` | `abandoned` | `PhaseAbandoned` | **yes** (`:293`) |
| `completion/complete.go:227` | `completed` | `PhaseCompleted` | no |
| `certification/ledger.go:155` | `certified` | `PhaseCertified` | no |
| `resultrecording/record.go:130,192,322` | `result_transition_recorded` | `next.TaskPhase` (variable) | no |

`resultrecording` is the site R5-1 indicts. It writes a *computed* phase from
`ClassifyNextState` and consults neither the transition table nor the current
phase. It is the highest-risk non-complying producer because its target phase is
data-dependent.

**Group B — non-phase-bearing appenders. Justified exceptions, conditionally.
Five sites.**

| Site | Event types | Phase written | Exception justified? |
|---|---|---|---|
| `ledger/legacy.go:60,68` | `legacy_import` | none | **yes** — imports pre-protocol history; by definition precedes the lifecycle |
| `admission/admission_ledger.go:36,45` | parameterized (`admission_decided`, `authority_resolved`, `admission_consumed`, …) | none | **conditional** — these annotate a phase without advancing it; the exception holds only while that remains true |
| `questiondisposition/record.go:107,127` | `question_disposition_recorded` | none | **yes** — records a question's disposition, not a phase change |
| `tasksession/session.go:897-907` | parameterized (`task_prepared`, …) | none | **NO** — see §6 |
| `tasksession/control.go:853-861` | `convergence_advanced`, `closure_assessed`, `task_control_projected` | none | **NO** — see §6 |

The classification, not the count, is the finding: **the lifecycle table governs
one of four phase-bearing producers, and two of the five "exceptions" are not
exceptions at all but unrecorded transitions (§6).**

## 6. Assessment observation A-1 — the folded phases are never durably recorded

*Discovered during this assessment, not by any review. Not repaired. Recorded
here for owner ruling.*

`latestTaskPhase` (`abandon.go:479-495`) scans the chain backward and returns the
first payload with a non-empty `TaskPhase`, or `("", false, nil)` if none has
one. The guard at `:292` is conditional on that boolean:

```go
if haveFrom {
    if terr := closureprotocol.ValidateTaskTransition(from, closureprotocol.PhaseAbandoned); terr != nil {
```

Measured: **no non-test site anywhere in `golang/architecture` assigns
`closureprotocol.PhaseStale`, `PhaseUncertifiable`, or `PhaseRefused` to a
`TaskEventPayload.TaskPhase`.** The only payload phase assignments in the tree
are the four in Group A above, and `ClassifyNextState` never yields a folded
phase.

Where those phases do live:

- `tasksession/session.go:55-56` defines its **own** untyped constants
  `PhaseStale = "stale"` and `PhaseUncertifiable = "uncertifiable"`, parallel to
  and separate from the `closureprotocol.TaskPhase` vocabulary. They are assigned
  to in-memory report fields at `session.go:565` and `:1543`.
- `tasksession/governance.go:150` returns `closureprotocol.PhaseRefused` into an
  in-memory `governanceState`, never into a ledger payload.

Consequences:

1. `latestTaskPhase` can never return a folded phase, so the round-three guard
   can never refuse abandonment of a stale, uncertifiable, or refused task.
2. The comment justifying that guard (`abandon.go:281-285`) names exactly that
   hazard: *"A task already folded to refused, stale or uncertifiable carries no
   completed, revoked, abandoned or result-transition event, so every guard above
   passes — and AllowedTaskTransitions gives those phases NO outgoing transition.
   Appending here would move an already-final task to abandoned and have terminal
   inspection report it as a clean stop."* The comment correctly identifies the
   hazard. The code cannot close it, because the phase it must read is never
   written.
3. The guard's only effective refusals — `completed → abandoned`,
   `abandoned → abandoned` — are already covered by the `completedCount` and
   `abandonedCount` guards above it and by the replay branch. **Its unique
   contribution is exactly the case that cannot fire.**
4. `tasksession` maintains a divergent, untyped duplicate of a closed vocabulary
   that `closureprotocol` owns.

This is a self-assessment of a repair made in round three. It was accepted by
review R4 and R5 and is nonetheless inert for its stated purpose. It is further
evidence that the defect class is not reachable by local repair: the guard is
correct, its table is correct, and it does nothing, because a *different package*
never records the fact it needs.

## 7. The architectural invariant

> **Every lifecycle fact is admitted through one transition authority;
> terminal/result contradictions, receipt-event identity, durable reconstruction,
> derived projection, and pointer retirement are validated as one governed
> transition — not independently rediscovered by callers.**

All 23 findings, and observation A-1, are instances of its violation. The
abandonment terminal was added as a new value in closed vocabularies
(`LedgerEventType`, `TaskPhase`, `AllowedTaskTransitions`) without sweeping every
site that reads those vocabularies by membership:

- `classifyTerminalFacts` was swept — correctly, and its comment says why (R2).
- `InspectTerminalState`'s contradiction switch was swept for completed and
  revoked, not for result-transition (R5-1).
- `resultrecording` was not swept at all (R5-1).
- `AllowedTaskTransitions` was extended for the inbound edge only, and its
  enforcement reaches one of four phase-bearing producers (§5).
- The folded phases were never recorded, so the inbound guard is inert for its
  stated case (§6).

This is the V3 closure law applied to the transition itself:
**PRESENT != GOVERNING.**

## 8. Recommended decomposition

Not an enlargement of #350. Six separately reviewable units, each with a single
authoritative owner, in dependency order:

1. **Central lifecycle admission and enforcement.** One gateway through which
   every phase-bearing event producer admits a transition. Must be used by all
   four Group A sites, not only abandonment. Must make Group B's exceptions
   explicit and checked rather than incidental. Must resolve §6: either the
   folded phases are durably recorded through the gateway, or they are removed
   from `AllowedTaskTransitions` — the vocabulary must not declare a law over
   states no producer can create. Must eliminate `tasksession`'s duplicate phase
   constants.

2. **Generic terminal event↔receipt binder**, shared by completion and
   abandonment. One implementation validating payload identity (task, session,
   phase, status) against the ledger entry and against the receipt, before any
   artifact reference is trusted. `completedEventMatches` is the seed; it must be
   generalized rather than copied.

3. **Durable-entry reconciliation.** A single recovery owner for
   `ErrEntryDurable` that restores HEAD *and* projections via
   `ReconcileDerivedState`, verifies the restoration under a gate that treats a
   head mismatch as invalid on this path, and refuses to report success or retire
   any pointer until it holds. **Ships with a fixture that produces the condition**
   — this is the M4b remedy and the precondition for any further logic in that
   branch.

4. **Crash-recoverable pointer serialization.** Requirements in §9.

5. **Abandonment rebuilt as a thin producer** on owners 1–4. On this decomposition
   `abandon.go` becomes a short function: resolve authority, build the receipt,
   admit the transition through owner 1, append, reconcile through owner 3,
   retire through owner 4. Most of the 23 findings become unrepresentable rather
   than repaired.

6. **Authority publication and commissioning probes**, after admission. Positive
   and negative controls per the standing requirement: suppression and repair
   look identical from the happy path alone.

Units 1–4 are independently valuable and independently reviewable. The
attribution below applies one rule: a finding is assigned to a unit only if that
owner's existence would have made the finding **unrepresentable**, not merely
easier to fix. Finding ids are defined in §13.

| Unit | Findings absorbed | Count | P1s |
|---|---|---|---|
| 1 — central lifecycle admission | R1-1, R1-2, R2-1, R2-2, R3-2, R3-4, R4-2, R5-1 | 8 | 7 |
| 2 — terminal event↔receipt binder | R2-4, R2-5, R4-3, R5-2 | 4 | 2 |
| 3 — durable-entry reconciliation | R4-4, R5-3 | 2 | 0 |
| 4 — pointer serialization | R2-3, R3-3, R5-4 | 3 | 2 |
| **Units 1–4 subtotal** | | **17** | **11** |
| 5/6 — producer and authority publication | R1-3, R1-4, R1-5, R3-1, R3-5, R4-1 | 6 | 4 |
| **Total** | | **23** | **15** |

**Seventeen of the twenty-three findings — eleven of the fifteen P1s — fall
inside four missing owners.** Observation A-1 (§6) also falls inside unit 1 and
is not counted above, since it was not a review finding.

Unit 1 is the dominant owner by a wide margin, which is consistent with §5: it is
the only one of the six whose absence affects every phase-bearing producer rather
than one code path.

## 9. Pointer mechanism requirements

Requirements, not an implementation choice. Any candidate must satisfy all six:

1. **Atomic read/decide/mutate.** The identity check and the unlink are one
   indivisible operation. A second read before the unlink shortens the window and
   does not close it — this was R2-3 and it must not regress.
2. **Automatic release on process death**, or safe reclamation of a demonstrably
   stale owner. A bound on waiting is not recovery (§4.5).
3. **Bounded waiting.** A live contending writer must be told it cannot proceed
   rather than blocking indefinitely.
4. **PID-reuse resistance.** Owner identity must not be a bare PID; a recycled
   PID must not be mistaken for a live owner, and a live owner must not be
   reclaimed as stale.
5. **Cross-platform: Linux, Darwin, Windows.** Advisory-lock semantics differ
   materially across the three, including whether a lock is released on process
   exit and whether an open descriptor blocks unlink. The requirement is
   equivalent *behavior*, not an identical mechanism.
6. **No new lifecycle protocol.** One mutex over one file. No state, no
   vocabulary, no receipt — the existing design's stated scope, preserved.

Candidate mechanisms (flock/LOCKFILE-style OS locks, owner-recorded lock files
with liveness probes, or a hybrid) should be evaluated against these six in unit
4's own review, not selected here.

## 10. Recommendation

**Park PR #350 at `42475333`.** Do not merge, do not close, do not extend.

Build units 1–4 as separately reviewable changes, then rebuild or replace the
abandonment producer on top as unit 5.

The evidence for splitting rather than continuing: 23 findings across five
reviews, with P1 density falling only from 4 to 2 across four repair rounds; two
consecutive reviews finding independent defects in the same undriven branch; and
a round-three guard that passed two subsequent reviews while being inert for its
stated purpose. The PR is integrating a distributed protocol as local patches.
Each round repaired the call site and left the protocol unchanged, so the next
review found the next call site.

Splitting by authoritative owner is safer, and — given that four repair rounds
have not converged — likely faster than round five.

## 11. Open blockers, unchanged

- **B1** — no human reviewer identity is available; merge remains blocked
  regardless of verdict. Unaffected by this assessment.
- **B2–B5** — open, as recorded in
  `2026-09-09-t01-addendum-transaction-certification.md`.
- The awareness entries for abandonment authority remain authored and
  unpublished (§4.6).

## 12. Provenance

- Verdict facts read from the GitHub API against `globulario/sensei`, 2026-09-10.
- Finding counts computed from `repos/globulario/sensei/pulls/350/comments`
  grouped by `pull_request_review_id`; 23 inline findings across the five
  connector review ids listed in §2.
- Code facts read from a clone verified clean at `42475333`. The owner's working
  clone does not contain this revision.
- §5 classification derived from `TaskPhase:` payload assignments and
  `store.Append` call sites across `golang/architecture`.
- §6 derived from the absence of any non-test assignment of a folded
  `closureprotocol` phase to a payload, cross-checked against `tasksession`'s
  local constants.

## 13. Appendix — finding index

Ids assigned by this document for cross-reference. Severities and locations are
as recorded by the reviewer; `—` means the reviewer attached no line.

| Id | Sev | Location | Title |
|---|---|---|---|
| R1-1 | P1 | `completion/abandon.go:317` | Reject abandonment when the session already has a result |
| R1-2 | P1 | `completion/abandon.go:357` | Register abandonment in the canonical terminal classifier |
| R1-3 | P1 | `completion/abandon.go` — | Verify authority before mutating during a replay |
| R1-4 | P1 | `completion/abandon.go` — | Preserve the pointer when the abandonment receipt is invalid |
| R1-5 | P2 | `completion/abandon.go` — | Propagate unreadable active-pointer errors |
| R2-1 | P1 | `completion/abandon.go` — | Reject every recorded result-transition event |
| R2-2 | P1 | `completion/inspect.go:35` | Include abandonment in the canonical state vocabulary |
| R2-3 | P1 | `tasksession/session.go` — | Make pointer deletion atomic with the identity check |
| R2-4 | P2 | `completion/abandon.go` — | Persist the stamped abandonment receipt |
| R2-5 | P2 | `completion/abandon.go:555` | Bind the abandonment receipt to its ledger event |
| R3-1 | P1 | `completion/abandon.go` — | Authorize the abandonment operation itself |
| R3-2 | P1 | `completion/abandon.go:356` | Refuse abandonment from already-terminal task phases |
| R3-3 | P1 | `completion/abandon.go` — | Keep the pointer mismatch decision under the lock |
| R3-4 | P2 | `completion/inspect.go:179` | Classify abandonment in the closure verifier |
| R3-5 | P2 | `completion/abandon.go` — | Return the receipt path on abandonment replay |
| R4-1 | P1 | `docs/awareness/authority_domains.yaml` — | Point the abandonment domain at its own mutation path |
| R4-2 | P1 | `completion/abandon.go` — | Fail closed when the latest lifecycle payload is malformed |
| R4-3 | P1 | `closureprotocol/validate.go:456` | Require the receipt task to match its base binding |
| R4-4 | P2 | `completion/abandon.go` — | Reconcile a durable append before returning failure |
| R5-1 | P1 | `completion/inspect.go:150` | Reject result transitions after abandonment |
| R5-2 | P1 | `completion/abandon.go:517` | Validate the abandoned event payload before trusting it |
| R5-3 | P2 | `completion/abandon.go:398` | Reconcile HEAD after a durable append |
| R5-4 | P2 | `tasksession/pointerlock.go:70` | Reclaim locks left by dead pointer writers |

R1-1 through R4-4 are repaired at `42475333`. R5-1 through R5-4 are unrepaired.
