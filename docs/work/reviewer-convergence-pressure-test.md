# Implementation brief: make independent review converge without moving the standard

## Why this exists

PR #62 recorded a six-cycle read-only governed run in which the worker and reviewer rotated. Each handoff preserved the rule "reviewer is not the author", but changing the author also changed the reviewer, so the standard moved. The run consumed roughly 50 minutes and never converged.

The important defect is compositional:

```text
reviewer must not be author
+
unconverged candidate hands off
=
reviewer can rotate with the worker
```

Neither rule is wrong alone. Together they can create a moving target.

Do not immediately implement "pin one reviewer" as doctrine. First pressure-test the alternatives on the recorded non-convergence specimen.

## Required experiment

Build a deterministic/replayable harness around the six-cycle specimen or a reduced equivalent that preserves:

- at least two providers capable of worker/reviewer roles;
- handoff after non-convergence;
- blocking findings;
- one objection that cannot be resolved from inside the candidate worktree;
- one environment/degraded-state objection outside the candidate's control.

Measure at minimum:

- reviewer identity per cycle;
- whether each blocking objection is new, repeated, resolved, or impossible to resolve from the candidate;
- report size / revision delta;
- cycles to terminal outcome;
- provider cost/time when available.

## Candidate policies to compare

Evaluate at least these without choosing by taste:

1. **Reviewer pinned per task**: choose one independent reviewer at task start; worker may rotate but reviewer stays fixed unless unavailable.
2. **Reviewer rotates with author**: current behavior, kept as control.
3. **Stable review standard with reviewer rotation**: objections become structured review requirements/receipts that later reviewers must evaluate against rather than restarting from their own lens.
4. **Unsatisfiable-blocker terminal**: reviewer can distinguish `candidate_wrong` from `cannot_be_resolved_in_candidate`, allowing the engine to stop rather than spend cycles rewriting an answer that cannot satisfy the objection.

If the existing architecture cannot express option 3 or 4 without inventing new truth/authority semantics, record that and test only what can be represented honestly.

## Required invariants

- Reviewer is never the author of the candidate under review.
- Reviewer acceptance is not Sensei admission.
- Switching workers must not erase already-established blocking objections.
- A new reviewer may add a genuinely new defect, but the system must make the standard movement visible rather than pretending it is convergence on the same checklist.
- An objection impossible to satisfy inside the candidate must not trigger unlimited rewrite cycles.
- Known degraded environment state must not be laundered into a candidate defect.

## Required attacks

- worker rotates, reviewer remains independent;
- reviewer unavailable mid-task;
- reviewer raises a blocking issue outside candidate authority;
- second reviewer disagrees with first reviewer's non-blocking preference;
- same objection repeated with different prose -> should be recognisable as same requirement if the mechanism claims stability;
- provider quota/auth failure -> distinguish infrastructure failure from review non-convergence.

## Relationship to #61 and #62

#61 is prompt guidance for one repeated inspection mistake. #62 is empirical evidence about convergence. Do not close either by assertion. This PR should report whether the chosen mechanism actually reduces review cycles on a live/replay specimen without weakening independence.

## Non-goals

- Do not suppress legitimate new findings merely to make the loop converge.
- Do not choose a reviewer because it is more permissive.
- Do not cap cycles and call timeout "convergence".
- Do not make the worker self-review.

## Success criterion

On the preserved non-convergence specimen, the review loop reaches a stable terminal outcome with a fixed/traceable standard, or terminates explicitly because a blocking objection is outside candidate authority. It must do so without weakening reviewer independence or hiding newly discovered defects.
