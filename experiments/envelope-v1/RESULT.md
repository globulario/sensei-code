# envelope-v1 — result: beyond the proof envelope → UNRESOLVED → nothing

```
fixture    golang/groupcache @ 2c02b82 · 2156 triples · closure PROVEN 0/0 · authoritative (no workaround)
specimen   cache.nbytes under cache.mu — first relation in stable order the reader itself called UNRESOLVED
           true by inspection: OnEvicted callback stored in lru.Cache, invoked under the caller's lock

E1  03:15:36Z → 03:20:12Z  exit 3   0 anchors  cold   closure: RECORDED cache.lru under cache.mu
E2  03:20:38Z → 03:26:15Z  exit 3   0 anchors  cold   closure: RECORDED cache.lru under cache.mu (again)
    recipe present at E2: the UNRESOLVED specimen, alone
    consumer reading:     outcome=UNRESOLVED anchor=false, boundary named in the detail
```

## The third side

A true relationship the deterministic reader cannot settle earned **no
coverage**. Route stayed cold. The reader's detail says why in its own words
(*"inside a closure that is stored, deferred, or run as a goroutine, and the
closure does not acquire the lock itself"*), and neither an optimistic
`DERIVED` nor a wrong `REFUTED` was produced.

With safety-v1 this closes the triangle, at governed-run level, on natural code:

```
TRUE     Weighted.waiters under mu   → DERIVED    → anchor → granted → task ran
FALSE    Group.err under errOnce     → REFUTED    → nothing → cold
ENVELOPE cache.nbytes under mu       → UNRESOLVED → nothing → cold
```

> The investigator may be right, wrong, or beyond the deterministic
> mechanism's reach; only mechanically established truth earns architectural
> coverage.

Observed on two unfamiliar repositories, each pinned, each onboarded by
deterministic extraction alone.

## Selection integrity

The eligible `UNRESOLVED` was the **first** relation scanned; the full ten-verdict
sequence (3 `UNRESOLVED`, 5 `DERIVED`, 2 `REFUTED`) is in `selection.json`.
Nothing about vocabulary, reader, predicate or fixture changed in response to
what was found. Had none qualified, the record would say `NO_SUBJECT`.

## The investigator, unprompted, twice

E1 and E2 both proposed `cache.lru under cache.mu` — a true question (the
selection scan reads it `DERIVED`), formulated independently on a repository the
investigator had never seen, and **not** the specimen. It was withheld from E2
so the `UNRESOLVED` contribution could be isolated, and is preserved in
`runs/E1.recipe-investigator-withheld.json`. That makes five distinct true
questions across two repositories, every one derivable, none of them told.

## Restart, disclosed

E1's first task asked for something already implemented (`CacheStats.Items`);
the architect replied so in 70s and never reached the region. My error, from an
incomplete read. Voided as `E1.void1-task-already-implemented`, reworded from the
code, and recorded before relaunch.

## Still not established

Generality beyond two fixtures (§8.3); anything about relationships outside the
one vocabulary family exercised end to end (`command_invocation_confined_to`
has not been run); the implementor/reviewer beyond three accepted candidates.
