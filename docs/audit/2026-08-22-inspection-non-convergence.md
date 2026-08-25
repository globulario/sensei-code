# Why an inspection ran six cycles and never converged — 2026-08-22

A read-only governed run (`/run Investigate whether internal/admission has any
non-test consumer…`) produced **six** inspection reports across **two** workers
and was rejected every time. It was stopped by hand after ~50 minutes and
roughly $10 of provider spend, having neither succeeded nor failed.

Nothing here is a defect report. The loop behaved as designed at every step, and
the reviewers were right on the substance. It is a record of *how* the mechanism
behaves on a hard question, because that is not visible from reading the code.

## The measurements

| cycle | worker | report | reviewer | objections |
|---|---|---|---|---|
| 1 | claude | 28217 ch | Codex | 1 major |
| 2 | claude | 23329 ch | Codex | 2 major, 1 minor |
| 3 | claude | 26019 ch | Codex | 3 major, 1 minor |
| 4 | codex | 9710 ch | Claude | **1 blocking**, 3 major, 1 minor |
| 5 | codex | 8930 ch | Claude | 3 major, 2 minor |
| 6 | codex | 11537 ch | Claude | (stopped mid-review) |

Provider stream: `success`, `is_error: false`, 56–70 turns per cycle. Nothing was
throttled, rate-limited or refused. Every rejection was about content.

## Four things this establishes

### 1. Objections rise as reports shrink

1 → 3 → 4 → 5 → 5. Workers responded to criticism by writing *less* (28k → 9k
chars) and drew *more* objections, not fewer. Whatever the loop is converging
toward, it is not acceptance.

### 2. The reviewer rotates with the worker, so the standard moves

Reviewer independence is enforced by excluding the author, which is right. The
consequence is not obvious: when the worker hands off, the reviewer swaps too.

    cycles 1–3   worker claude   reviewer Codex
    cycles 4–6   worker codex    reviewer Claude

A worker that revised to satisfy reviewer A is then judged by reviewer B, which
has its own lens and its own objections. Every objection in cycles 4–6 is new;
none is a repeat of 1–3. The worker is not failing to hit a fixed standard, it
is chasing a moving one.

This is a structural property of "the reviewer is never the author" combined
with "an unconverged candidate hands off". Both rules are individually correct.

### 3. Some objections cannot be satisfied from the repository

Cycle 4 raised a **blocking** finding: *"the `synthesis-admit` premise is
unresolved and blocks safe wiring"*. That is true, and it is not the report's
fault — Sensei has no canonical operation for sealing an externally produced
candidate, so the premise is genuinely unresolved upstream. No revision of any
report can settle it.

A loop that requires satisfying such an objection cannot terminate by revising.
It terminates only by exhausting cycles.

### 4. Known-broken local state generates objections

Cycle 5: *"The task briefing MCP call failed exactly with `no task session
exists in this repository`"*. That is our stale task binding
(`sensei:task_binding` FAIL), which we had already found and deliberately not
repaired because the bound task is parked at `admitted`. The reviewer is
correctly objecting to a limitation of the environment, and the worker cannot
fix it from inside a candidate worktree.

Environment defects therefore surface as report defects, and cost cycles.

## What would change the outcome

Not more prompt guidance. A separate change (#61) names the one *recurring*
error — proving absence from a search — and should reduce that class. It does
not address any of the four findings above.

The candidates worth considering, none yet implemented:

- **A reviewer that stays fixed for a task.** Independence requires the reviewer
  not be the author; it does not require the reviewer to change when the author
  does. Pinning one reviewer per task would give the worker a fixed standard.
  Cost: that reviewer's blind spots persist for the whole task.
- **A terminal outcome for unsatisfiable objections.** A blocking finding that
  no revision can address should end the run and say so, rather than consuming
  the remaining cycles. This needs the reviewer to distinguish "the report is
  wrong" from "the question cannot be answered here", which it currently cannot
  express.
- **Refusing to start when the environment is degraded.** `doctor` reports the
  stale binding; a governed run does not consult it.

## What this cost

Roughly 50 minutes and $10 to learn the above, on a question whose answer was
partly established for free earlier in the day by reading the source. That ratio
is itself a finding about when to reach for a governed inspection.
