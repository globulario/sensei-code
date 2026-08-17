# P1: Repair knowledge — learning the fix that actually held

## The problem

The awareness graph can record that something broke (`failure_mode`), what
proves it is fixed (`required_test`), and what must never be tried again
(`forbidden_fix`). It has no way to record **what actually worked**.

That asymmetry is why repair knowledge does not accumulate. Every scar teaches
the project one negative lesson and discards the positive one, so the next agent
facing the same failure mode re-derives the repair from scratch — and derives it
slightly differently, which is how two correct-looking fixes for one problem end
up in the same codebase.

The storage, validation and promotion machinery already exists. What is missing
is a kind, a signal, and a place to read it.

## What must not be built

**A library of accepted fixes.**

"Accepted" and "works" are different claims, and the difference is not
theoretical. Three fixes made while building the P0 slices were accepted — build
green, tests green, reviewer satisfied — and were wrong:

- a sandbox constant unified across two endpoints: every local signal passed,
  and it broke `turn/start`, which had been correct before the change;
- a capability guard installed inside the candidate worktree: passed until a
  force-push test happened to reset history, which deleted the guard;
- an authority-escalation path: passed every unit test, and looped a live run
  thirteen times without ever reaching a candidate.

Reviewer acceptance is not admission. That is the system's own law, and a repair
library that learned from acceptance would learn from precisely the signal the
architecture declares insufficient — then reproduce plausible-but-wrong repairs
at speed, carrying provenance that makes them look vetted. That is worse than
having no repair memory at all, because it is wrong in a way that reads as
authority.

## The signal that is admissible: survival

A repair is worth recording when the failure it addressed **did not recur**.

That is a property of time and subsequent runs, not of a review. Concretely, a
repair becomes eligible when all of:

1. it is linked to a specific `failure_mode` in the graph;
2. the `required_test` that proves the failure is closed exists and passes;
3. the candidate carrying it was accepted **and** the diff audit did not return
   `cannot_verify` — an unverified acceptance is not evidence of anything;
4. a survival window has elapsed with no recurrence: N subsequent governed runs
   touching the same region completed without that failure mode being
   re-observed;
5. no `forbidden_fix` for the same region matches the repair's shape.

Condition 3 matters more than it looks. Half the acceptance events in a real run
sit behind an audit that could not be performed, and treating those as evidence
would fill the store with repairs nobody verified.

Condition 4 is the one nothing currently computes. `behavioral_record_outcome`
already records what happened; nothing closes the loop from "recorded" to "held
up".

## Where each half belongs

Two memories, two jobs, and conflating them is the main design risk here.

| | awareness graph | behavioral memory |
|---|---|---|
| answers | what is true about **this repository** | what an agent may **do** |
| scope | repository / domain corpus | behavioural domain |
| a repair is | a fact about a failure in this code | not its business |
| relevant tool | `awareness_propose`, `awareness_impact` | `behavioral_check_action` |

**A repair is repository knowledge and belongs in the graph**, beside the
failure it closed. Behavioral memory answering "may I merge my own pull request"
and behavioral memory answering "how do I fix a stale seed" would be two
different systems sharing a word.

Behavioral memory still contributes to efficiency, but by a different route:
when `check_action` answers `governed: true, allowed`, an agent knows it need
not ask a human. Today it answers `governed: false` for nearly everything, so
nothing can be skipped. That is the conduct half, tracked with the Level-1
slice, and it is out of scope here.

## Design

### Upstream (Sensei): a positive repair kind

`awareness_propose` accepts `failure_mode | invariant | required_test |
forbidden_fix | contract_unknown`. It needs a positive counterpart to
`forbidden_fix` — provisionally `applied_repair` — carrying:

Filed upstream as globulario/sensei#172. This slice is blocked on it.

```text
applied_repair
  related_failure     the failure_mode it closed          (required)
  required_tests      the tests that prove it             (required)
  source_files        where it was applied                (required)
  description         what the repair actually did        (required)
  survival_evidence   runs observed without recurrence    (required)
  domain              repository scope                    (required)
```

Every field is required deliberately. A repair without a linked failure is an
anecdote; without a test it is unproven; without survival evidence it is an
acceptance wearing a better name.

It must go through the same review queue and promotion gate as every other
proposed entry. Nothing here writes to the live graph.

### sensei-code: compute survival, then propose

```text
internal/repair/
  Eligibility   the five conditions above, as a pure function over typed evidence
  Window        how many subsequent runs without recurrence are required
  Propose       submits an applied_repair once eligible, and never before
```

The propose call reuses the P0.6 machinery: typed, provenance-bound, submitted
to the review queue, `Durable()` only on an accepted status plus a candidate
path.

### The worker surface: both directions, always

When a worker begins on a region, it receives:

```text
REPAIRS PREVIOUSLY RECORDED FOR THIS FAILURE MODE
  <repair> — applied to <files>, proven by <test>, survived <n> runs
  This is evidence of what worked once, in that context. It is not an
  instruction, and it may be wrong here.

FORBIDDEN FIXES FOR THIS REGION
  <forbidden fix> — <why it is forbidden>
```

The pairing is not presentational. "Here is what worked" becomes "do this"
almost immediately, and a repair applied in a context where it does not hold is
exactly how a `forbidden_fix` is born. Showing both directions at once keeps the
agent reading rather than pattern-matching.

## Acceptance tests

**Survival, not acceptance**

- a repair whose candidate was accepted is **not** eligible before the survival
  window has elapsed;
- a repair whose audit returned `cannot_verify` is never eligible, however many
  runs pass without recurrence;
- a repair is disqualified when the failure mode recurs inside the window, and
  the disqualification records which run re-observed it;
- eligibility is unchanged by reviewer prose, summaries, or confidence language.

**Links are required, not optional**

- a proposed repair with no `related_failure` is refused before it reaches
  Sensei;
- a proposed repair with no `required_test` is refused;
- a proposed repair whose named test does not exist is refused;
- a repair matching an existing `forbidden_fix` for the same region is refused,
  and the refusal names the forbidden fix.

**Nothing becomes canonical here**

- an eligible repair is submitted to the review queue and reports itself as
  proposed, never as project knowledge;
- an unavailable or refusing propose surface leaves the repair not-durable and
  says the knowledge was not recorded;
- no code path writes a repair into the live graph.

**Consultation cannot become instruction**

- a worker prompt containing a past repair also contains the forbidden fixes for
  the same region;
- the repair is labelled as evidence with its original context, and the prompt
  does not instruct the worker to apply it;
- when a region has forbidden fixes and no recorded repairs, the forbidden fixes
  are still shown — the surface is not suppressed for being one-sided;
- when a region has neither, the prompt says so explicitly rather than omitting
  the section, so absence is visible rather than looking like truncation.

**Memory boundaries hold**

- repair knowledge is proposed to the awareness graph, never to behavioral
  memory;
- `behavioral_check_action` is not consulted to decide whether a repair is
  eligible;
- the repair surface reads promoted graph knowledge only, never the candidates
  queue, so a repair proposed during a task cannot influence that same task.

## Risks

**The survival window is a guess.** Too short and it certifies luck; too long
and nothing is ever learned. It should start deliberately long, be recorded with
each repair, and be tightened only from evidence — never widened to make the
feature look more active.

**A recurrence may be attributed to the wrong repair.** When several repairs
touch a region and the failure returns, the naive reading blames the most recent.
Attribution should be reported as uncertain rather than guessed, and a repair
should be disqualified rather than silently kept when attribution is ambiguous.

**This makes the graph an attack surface for efficiency pressure.** An agent
that wanted its own work to look routine could propose repairs that make a
region appear well-understood. Reading only promoted knowledge is the mitigation,
and it is the same one Level-1 relies on.

## Sequencing

After the P0 merge. It depends on the upstream repair kind, which should be
filed first, and it shares the propose plumbing with P0.6 which is already in
place. It is independent of Level-1 and can land in either order, though both
become more useful once graph coverage improves.
