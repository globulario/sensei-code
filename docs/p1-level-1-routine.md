# P1: Level-1 routine — proportional ceremony without lost authority

## The problem

A one-file test change currently travels the whole governed path: architect →
claims → authority router → Level-3 escalation → candidate →
worker → diff audit → reviewer. That is correct for architecture-sensitive work
and disproportionate for a typo, and disproportionate ceremony is not a
cosmetic complaint. It trains a person to approve without reading, which
converts every prompt in the product into noise and quietly removes the
protection the prompts existed to provide.

The architecture already implies the missing tier — it says routine execution
should be autonomous — but nothing computes it. Every change is treated as
potentially architectural because no code can tell the difference.

## The rule this slice must not break

**Relax interruptions, never evidence.**

Ceremony has two costs. Machine work — preflight, edit-check, candidate
isolation, diff audit, reviewer — is cheap, and all of it still runs on a
routine change. Human attention is expensive, and it should be spent only where
authority is genuinely needed.

A Level-1 change therefore skips the *Level-3 escalation*. It skips nothing
else. A typo is still isolated in a candidate, still audited by Sensei, still
reviewed, and still cannot be accepted over a refusal. What changes is that
nobody is woken up about it.

**Half of this landed ahead of the slice.** The plan-approval prompt this
document also proposed skipping has since been removed for *all* work, not only
routine work: `/run` is the authorization, and the prompt produced no evidence,
made no decision the router had not already made, and recorded nothing durable
(`sensei_code.workflow.execution_is_authorized_once_at_run`). What remains for
Level-1 is the escalation — the harder and more valuable half, because it is the
one that requires telling a routine change from an architectural one.

Anything that reduces verification is out of scope for this slice and should be
refused if proposed as part of it.

## Smallness is computed, never claimed

The moment "this is just a typo" becomes a judgement any agent asserts, we have
reinstated model-controlled authority in friendlier clothing — the exact defect
P0.1 removed. Level-1 is therefore derived from Sensei's own structured
evidence, by a pure function, with no model input.

### Qualifying conditions

All must hold. Any one absent means the change is not routine.

| # | condition | why |
|---|---|---|
| 1 | `Authority.Certifiable()` — authoritative, graph current, seed current | a stale graph will call anything routine, fluently |
| 2 | file-scoped preflight `PREFLIGHT_STATUS_OK` | the graph actually answered about *these* files |
| 3 | coverage is `EmptyProven`, **not** `Absent` | see "The load-bearing distinction" |
| 4 | no `direct_invariants` of severity critical or high | nothing governing is in scope |
| 5 | `blind_spots` empty | Sensei is not reporting that it cannot see |
| 6 | `blast=local` and `approval=none` | Sensei's own risk classification, not ours |
| 7 | `awareness_edit_check` returns no forbidden-fix match | the shape is not a known-broken repair |
| 8 | every changed path is one the plan named | scope did not widen after the decision |
| 9 | no claim carries `source: "inference"` | the architect itself reported an unverified premise |

Conditions 1–7 are read from Sensei. Condition 8 is read from the candidate
diff. Condition 9 is read from the architect's claims, and is the one case
where a model's own statement *restricts* rather than grants — which is safe in
the direction it operates.

### The load-bearing distinction

"No invariants apply" has two meanings, and P0.7 already gave us the types to
tell them apart:

- **`EmptyProven`** — the graph covers this region and affirmatively reports
  nothing governing it. That is evidence.
- **`Absent`** — the graph has never heard of this file. That is ignorance.

A fast path that fired on `Absent` would fast-path precisely the code nobody has
ever analysed. It must require `EmptyProven`.

The consequence has to be stated plainly rather than discovered later: with this
repository's coverage as measured on 2026-08-16 — `anchors=0 files=0 indexed=0
sufficient=false` — almost nothing qualifies today. That is the correct
outcome. Level-1 becomes available as the graph earns it, which puts the
incentive where it belongs.

## Categorical exclusions

Never routine, regardless of every measurement above:

- any file listed in `docs/awareness/high_risk_files.yaml`
- any diff matching a forbidden-fix shape
- deletion or weakening of an existing test
- changes to the governance path itself (`internal/sensei/contracts.go`,
  `internal/workflow/gate.go`, `internal/workflow/authority.go`,
  `internal/broker`, `internal/candidate`, `internal/authority`)
- any diff that touches a file the plan did not name

The governance-path exclusion matters most and is the easiest to forget: a
routine tier that could fast-path an edit to its own qualifying conditions is a
tier that can widen itself.

## Design

A fourth route beside the existing three, computed by a pure function next to
`routeAuthority`:

```text
internal/workflow/routine.go
  RouteRoutine Route = "level-1-routine"

  type RoutineDecision struct {
      Routine    bool
      Qualifying []string  // every condition that held, in order
      Blocking   string    // the first condition that did not, when Routine is false
  }

  func classifyRoutine(scoped sensei.PreflightDecision, claims []Claim,
                       edit sensei.EditCheckResult, planned, changed []string) RoutineDecision
```

`routeAuthority` consults it only after it has already decided the change is
not human-owned; Level-1 is a narrowing of architectural authority, never a way
around a human boundary. A change that would otherwise escalate cannot become
routine.

## Rollout: dark run first

The slice ships in two stages, and the first stage grants nothing.

**Stage 1 — observe.** `classifyRoutine` is computed on every governed run and
emitted as an event with its qualifying and blocking conditions. Nothing is
skipped. `/report` gains a line: how many runs would have qualified, and which
condition blocked the rest.

This answers the question that decides whether the tier is worth having, using
real runs rather than intuition. If nothing qualifies for a month, the answer is
to invest in graph coverage, not to loosen the conditions.

**Stage 2 — act.** Only after stage 1 shows the classifier firing on changes a
human agrees were routine, and only then, does `RouteRoutine` skip the Level-3
escalation.

Shipping stage 2 without stage 1 would mean granting a privilege before knowing
how often it fires or on what.

## Safeguards

**Auditable.** Every routine-classified change records the exact conditions that
qualified it. `/report` must be able to answer "show me everything that skipped
escalation, and why." A privilege that cannot be enumerated afterwards is not
governed.

**Not a configuration flag.** There must be no setting that enables Level-1
generally. The same reasoning as P0.3's mode: a switch that makes things faster
is flipped once and never revisited, and it would decide policy at a distance,
in a file nobody re-reads. Computed per change or not at all.

**Revocable by evidence.** When a change that took the routine path is later
implicated in a failure, that becomes a `failure_mode` proposed against the
conditions that let it through. The tier tightens from experience instead of
being trusted permanently.

**Promoted knowledge only.** The classifier reads promoted graph knowledge and
never the candidates queue. Otherwise an agent could propose an invariant that
makes its own change look routine, and proposals made during a task must not
affect that task's routing.

## Acceptance tests

Written to fail first, as with the P0 slices.

**Evidence, not assertion**

- a change the architect describes as trivial, on an uncovered region, is not
  routine — model prose has no effect on classification;
- classification is unchanged when the architect's summary, claims text and
  recommendation vary, given identical Sensei evidence;
- a claim with `source: "inference"` disqualifies, even when every Sensei
  condition holds.

**The absence distinction**

- `EmptyProven` coverage with no critical invariants qualifies;
- `Absent` coverage with otherwise identical evidence does **not** qualify, and
  the blocking condition names coverage;
- a stale or unauthoritative graph does not qualify however small the diff.

**Categorical exclusions**

- a diff touching a file in `high_risk_files.yaml` is never routine;
- a diff deleting a test is never routine;
- a diff touching `internal/workflow/gate.go` is never routine — the tier
  cannot fast-path its own gates;
- a diff touching a path the plan did not name is never routine.

**Interruptions relaxed, evidence intact**

- a routine-classified run still creates a candidate worktree;
- a routine-classified run still calls `awareness_audit_diff`;
- a routine-classified run still runs the reviewer;
- a reviewer `accept` over a Sensei `block` is still refused on the routine
  path — the gate is unchanged;
- a routine-classified run emits no `authority.required` event.

**Authority is never widened**

- a change that routes to `human-authority-required` cannot be reclassified as
  routine;
- a change on a graph that cannot vouch for itself routes to
  `cannot-establish-authority`, never to routine;
- there is no configuration key that enables Level-1 (asserted structurally, as
  P0.3's mode test does).

**Dark run**

- stage 1 emits the classification without skipping any interruption;
- the emitted decision names every qualifying condition and, when blocked, the
  first condition that failed;
- `/report` reports the counts and the blocking-condition breakdown.

## Non-goals

This slice does not touch admission, apply or verify; does not change what the
audit checks; does not introduce a way to skip the reviewer; and does not make
any capability enforceable that `docs/open-items.md` currently records as
unenforceable.

## Sequencing

After the P0 branch merges and the governed acceptance run passes. Adding a path
that skips steps while the full path has never completed once would make the
next failure substantially harder to attribute.
