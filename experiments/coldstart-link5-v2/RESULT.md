# cold-start-link5-v2 — result: link 5 passes; link 6 was never asked

```
run        02:34:30Z → 02:38:08Z   exit 3   base de8f24f   reader sensei@e4bb3fb0 (v2)
binding    HELD
coverage   derived coverage: 1 anchor(s) over 1 planned file(s) — semaphore/semaphore.go [lock discipline]
route      bounded-knowledge-gap (unchanged)
closure    RECORDED a NEW question: field_access_under_lock(Weighted.waiters under Weighted.mu)
```

## The sharp question

> Given the exact recipe the investigator produced, can the repaired substrate
> derive it correctly?

**Yes.** `DERIVED`, all 10 accesses, inside the governed run, one anchor over
the one planned file. Link 5 holds on the true question.

## Link 6 — not "no", but never asked

The route did not change. Reading the router: `derivationClosesGap` is
consulted only in the `coverageAbsent` branch (*"graph coverage is absent for
the planned files"*). This task's gap arrived as a coverage-type **blind spot**
(*"graph indexes this area but no anchored rules apply"*), and that branch never
consulted derived coverage. Two branches for one family of gap; one wired.

The relevance gate itself would have said **yes**: the gap is unqualified, and
`lock discipline` is a resolvable family. It was simply not called.

Fixed in sensei-code (blind-spot branch now asks the same question, falls
through to consequence assessment when closed, fails closed on an unrecognised
family). Tested. Measured next in `cold-start-link6-v1`.

## Two voids on the way, both recorded

1. `E2.void2-subjects-contract-mismatch` — the first v2 reader was built on a
   branch whose receipt lacks `subjects`; sensei-code's consumer refuses
   coverage that cannot name what the proposition is about. **This also means
   v1's treatment Encounter 2 had two independent blockers at link 4→5**, not
   one; `coldstart-v1/RESULT.md` is amended below.
2. `E2.void-dirty` — receipts appended after the base commit.

## The investigator, unprompted

With `cur` now covered, it asked about `waiters` — the other field the lock
protects. A second true question, arrived at by the same mechanism, on the
first encounter where the first question had become coverage.
