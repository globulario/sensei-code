# cold-start-link5-v2 — can the repaired substrate derive the investigator's question?

A **new** experiment, not a continuation of `coldstart-v1`, whose result stands
as recorded (loop closed at link 5, compounding unmeasured). The derivation
substrate changed, which under the frozen rule is a restart — labelled as one.

## The one sharp question

> Given the exact recipe the investigator produced independently (five times),
> can the repaired deterministic substrate derive it correctly — and if so, do
> links 6 and 7 follow?

## Fixed

```
fixture     golang/sync @ 3ffd83cb · semaphore/semaphore.go
state       treatment arm at de8f24f — Encounter 1 recipe persisted, receipt RECORDED
recipe      field_access_under_lock(Weighted.cur under Weighted.mu) in semaphore
            written by closure_round, origin task-1787796232363441216
derivation  sensei @ 7db7c3c8 (fix/derive-tristate-and-flow-sensitive-lock-reader), v2
graph       sensei @ 80392aaf, unchanged · sensei-code @ fec4586
task        Encounter 2, verbatim
prompt      gap-closure-prompt/v4, unchanged
```

Nothing about the investigator changed. The model stays still this round.

## Pre-registered falsifier, from the manual run

`derive` on the pinned tree, by hand, before this run: **DERIVED, all 10
accesses.** So the expected reading inside the governed run is
`derived coverage: 1 anchor(s) over 1 planned file(s)`.

| observed | reading |
|---|---|
| `UNRESOLVED` / 0 anchors | the reader is still incomplete, or the engine's world differs from the hand-run's |
| `REFUTED` | another deterministic bug, or the manual adjudication was wrong |
| `DERIVED`, 1 anchor, route unchanged | link 5 passes, link 6 fails — a true and *irrelevant* fact |
| `DERIVED`, 1 anchor, route changes | links 5–7 — compounding, first observation |

Link 6 is decided by `derivationClosesGap`: whether the anchor's requirement
answers the router's condition. The condition for this task's region was
*"graph indexes this area but no anchored rules apply"*. Whether a lock
discipline anchor closes that is precisely what the relevance gate exists to
decide, and it must be allowed to say no.
