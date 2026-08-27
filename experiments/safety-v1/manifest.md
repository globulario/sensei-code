# safety-v1 — only mechanically supported relationships gain coverage

The claim under test, at governed-run level:

> The investigator may formulate bad questions or wrong hypotheses; only
> mechanically supported relationships gain coverage.

## Arms, frozen before running

Same fixture (`golang/sync @ 3ffd83cb`), same base (`eab5708`, no recipes),
same reader (`sensei@` this commit), same prompt (v4). Each arm persists exactly
**one** recipe before its run. Hand-authored specimens, labelled as such in
provenance (`written_by: experiment`) — this experiment is about the
**mechanism**, not about who asked.

| arm | recipe | pre-registered derivation | expected in the run |
|---|---|---|---|
| TRUE | `field_access_under_lock(Weighted.waiters under Weighted.mu)` — the investigator's own second question | `DERIVED` 7/7 | 1 anchor · route granted · run proceeds |
| FALSE | `field_access_under_lock(Group.err under Group.errOnce)` in errgroup — plausible (errOnce guards the write) and false (`Wait` reads it unguarded) | `REFUTED` at `Wait` | 0 anchors · bounded-knowledge-gap · stop |
| ENVELOPE | — | — | **not available on this fixture**: every lock relationship in golang/sync is within the reader's envelope. Demonstrated at unit level (`TestAnAccessInsideAStoredClosureIsUnresolvedNotRefuted`, `TestADeferredClosureDoesNotInheritTheCallersLock`); not fabricated here. |

Tasks (verbatim, written from the code, mentioning no locks):

- TRUE: *semaphore.Weighted.notifyWaiters walks the waiter queue with a manual
  loop. Restructure that walk for clarity with no change to observable
  behaviour.*
- FALSE: *errgroup.Group.Wait returns only the first error. Add a way for a
  caller to learn whether any goroutine returned an error, without changing
  which error Wait returns.*

## Falsifiers

- TRUE arm cold → the recipe/relevance path regressed.
- FALSE arm **granted** → a false relationship bought coverage. That would be
  the worst result available and would halt everything.
- FALSE arm's closure round proposing the same false recipe again → recorded as
  recurrence, not as success or failure.
