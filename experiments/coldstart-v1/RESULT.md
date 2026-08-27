# Cold-start A/B — result

```
fixture    golang/sync @ 3ffd83cb · semaphore/semaphore.go · 1498 triples · 0 governed identities
prompt     gap-closure-prompt/v4 · vocabulary frozen · 0 recipes at start
product    sensei-code @ 1249652 · graph by sensei @ 80392aaf · derive by sensei @ c87da8a3
```

## The four runs

| arm | encounter | terminal | derived coverage | closure outcome | recipe after |
|---|---|---|---|---|---|
| control | E1 | stop (exit 3) 224s | 0 anchors | **RECORDED** `Weighted.cur under mu` | withheld |
| control | E2 | stop (exit 3) 218s | 0 anchors | **RECORDED** (same question, empty view) | withheld |
| treatment | E1 | stop (exit 3) 278s | 0 anchors | **RECORDED** `Weighted.cur under mu` | persisted |
| treatment | E2 | stop (exit 3) 235s | **0 anchors** | **DUPLICATE** | unchanged |

Graph binding held in all four (0 divergence). Encounter 1 stopped in both arms,
as the future-only rule requires. Control Encounter 2 was cold, as predicted.

## The seven-link chain, treatment arm

| link | | verdict |
|---|---|---|
| 1 | gap identified | **yes** — bounded-knowledge-gap, both encounters |
| 2 | question proposed | **yes** — `field_access_under_lock(Weighted.cur under Weighted.mu)` |
| 3 | question admitted | **yes** — valid kind, in region, provenance stamped, receipt RECORDED |
| 4 | question executed later | **yes** — Encounter 2 ran the derivation against the pinned tree |
| 5 | DERIVED | **NO** — `NOT_DERIVED`: *"6 of 10 accesses are not under mu"* |
| 6 | coverage relevant to the gap | not reached |
| 7 | routing changed | not reached — Encounter 2 escalated exactly as Encounter 1 did |

**The loop is closed at link 5.** Everything upstream of the derivation worked
on a repository Sensei had never seen; the derivation returned no anchor; the
system stayed cold. No coverage was granted, no authority moved, and the
investigator's belief never entered the graph. That is the safety property,
observed rather than argued.

## Link 5, examined — the question was true

The derivation cites six accesses to `cur` outside `mu`. Read against the
source at the pinned commit:

```
Acquire   s.mu.Lock() @43 … s.cur @54, @59 … Unlock @60
          s.mu.Lock() @75 … s.cur @80, @86 … Unlock @90
Release   s.mu.Lock() @132 … s.cur @133, @134 … Unlock @139
notifyWaiters  @148, @162 — called only from @81, @87 and Release, all under mu
```

**Every access to `Weighted.cur` is under `Weighted.mu`.** The investigator's
question was true and worth asking. The derivation's static reader cannot follow
lock state across `select` branches and early-`Unlock` paths, nor into a callee
whose lock is held by its callers — and its own `completeness_scope` declares
exactly those limits. It returned `NOT_DERIVED` with a detail text that
*asserts* the accesses are unlocked.

So this is a **false negative of the deterministic substrate**, not a false
question from the investigator. The design failed **closed** — the correct
direction — but the answer-side mechanism has a precision limit on precisely the
code shape where lock-discipline questions matter most.

Two substrate findings follow, neither about the investigator:

1. **`NOT_DERIVED` conflates *false* with *not provable by this reader*.** The
   vocabulary says NOT_DERIVED means "checked and it is false"; the derivation's
   scope says it could not follow the lock state. A detail that asserts a
   negative it cannot establish is the §5.1 class — a claim supported only by the
   limits of the thing asserting it.
2. **The derivation's completeness is the ceiling of the loop.** Every question
   in this vocabulary about branchy lock discipline will stop here until the
   reader follows control flow and caller-held locks. That is Phase B work on
   the substrate: deterministic, testable, and now with a real specimen.

## What the investigator did — for the record, with credit this time

Under v4 the investigator proposed the same question **four times in four
independent runs**, from two different tasks, in both arms, with the recipe view
empty and with it present. Under v3 it proposed nothing twice while writing the
same relationship as a claim. The v4 change told it what kind of gap it faced;
it did not tell it what to ask. The recurrence is the strongest formulation
signal the receipts could have carried, and it is exactly the signal the
`DUPLICATE` outcome exists to preserve.

Its escalations moved too: v3 asked *"which API should expose the state?"*;
v4 asked *"should Sensei mechanically derive the Weighted field-lock
relationship and then rerun scoped planning?"* — the loop, requested by name,
by the participant that cannot run it.

## What is and is not established

**Established:**
- `unknown → investigation → safe durable question`, on a cold repository,
  reproducibly.
- The receipt and admission machinery, including dedup and future-only, under
  live conditions.
- The safety property: a question whose derivation fails yields nothing.

**Not established:**
- Compounding. No derived anchor existed, so no later encounter could benefit.
  Links 6–7 are untested, and this experiment cannot test them until link 5
  can pass on a true question.
- Anything about a *false* question — the planned "plausible but wrong"
  stage — because the derivation cannot yet distinguish false from unprovable.

## The next honest step

Not the investigator, and not the prompt. **The derivation reader.** A
deterministic, unit-testable improvement — follow lock state through `select`
branches and early unlocks; recognise a callee reached only from lock-holding
callers — with `semaphore.go @ 3ffd83cb` as the specimen and `DERIVED` as the
falsifier. Then re-run Encounter 2 in the treatment arm from `de8f24f`, where
the recipe already sits, and see whether links 6 and 7 follow.

That is substrate work, in `globulario/sensei`, and it is the first time this
campaign has pointed at the deterministic side rather than the model.

## Setup cost of this fixture, cumulative

Seven onboarding and product defects surfaced before a single link was
measured, four fixed upstream, two by `.gitignore`, one recorded. The eighth —
`derive` absent from the branch carrying the closure fix — cost one voided
launch and is recorded in `frozen.json`.
