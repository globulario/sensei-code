# envelope-v1 — the third side of the triangle, on a natural specimen

> The investigator may be right, wrong, or beyond the deterministic mechanism's
> reach; only mechanically established truth earns architectural coverage.

## Fixture

```
repository   github.com/golang/groupcache (the original)
pinned SHA   2c02b8208cf8c02a3e358cb1d9b60950647543fc   (2024-11-29)
graph        isolated, :10191 · deterministic import (basic) · closure PROVEN · authoritative
reader       sensei-derive-v3 = sensei@0c95dc08 reader v2 (the same reader as safety-v1)
prompt       gap-closure-prompt/v4 · recipes at start: 0
```

## Selection, frozen before this file existed

Stable path order; frozen expressibility predicate; the current reader
evaluates each relation; select the **first** `UNRESOLVED` whose own detail
names an eligible proof-envelope boundary; `NO_SUBJECT` otherwise; nothing
changed in response. The full sequence, in order (`selection.json`):

```
UNRESOLVED  groupcache.go   cache.nbytes under mu        ← SELECTED (first relation scanned)
DERIVED     groupcache.go   cache.lru under mu
UNRESOLVED  groupcache.go   cache.nevict under mu
REFUTED     http.go         HTTPPool.Context under mu
DERIVED     http.go         HTTPPool.Transport under mu
DERIVED     http.go         HTTPPool.self under mu
REFUTED     http.go         HTTPPool.opts under mu
UNRESOLVED  http.go         HTTPPool.peers under mu
DERIVED     http.go         HTTPPool.httpGetters under mu
DERIVED     singleflight/   Group.m under mu
```

The reader's own words for the selected relation:

> *could not establish the lock state for 1 of 4 access(es); no counterexample
> found: add accesses c.nbytes … (groupcache.go:427): inside a closure that is
> stored, deferred, or run as a goroutine, and the closure does not acquire the
> lock itself*

## What is actually true there, by inspection

The closure is the `OnEvicted` callback stored in `lru.Cache`. It is invoked
synchronously by `lru.Add` / `lru.RemoveOldest → removeElement`, and groupcache
calls those only from `cache.add` / `cache.removeOldest`, both under `c.mu`
(`defer c.mu.Unlock()`). **The discipline holds in practice** — through a
callback stored in another package's type and invoked by that type's methods
while the caller holds the lock. That is outside this reader's envelope, and the
reader says so rather than guessing either way. Selected precisely because it
is true *and* unprovable here.

## Encounters, verbatim, written from the code, mentioning no locks

- **E1** — *groupcache's cache reports bytes, hits, gets and evictions in its
  statistics. Add the current item count to the reported statistics without
  changing eviction behaviour.*
- **E2** — *cache.add constructs the LRU lazily on first insert. Make LRU
  construction happen once, in one place, so add and get no longer need to
  check for a nil LRU, with no change to observable behaviour.*

## Arms and predictions

- **E1** (no recipes): cold → closure round → `RECORDED` or `NO_PROPOSAL` →
  stop. Recorded either way. Whether the investigator's question is
  `cache.nbytes under mu` is reported, not required.
- **E2** with the selected specimen persisted — the investigator's own if it
  matches, otherwise hand-authored and labelled `written_by: experiment`:
  derivation **`UNRESOLVED`** → **0 anchors** → `bounded-knowledge-gap` → stop.

Falsifiers: `1 anchor` on E2 → the reader granted coverage past its envelope
(worst outcome; halts everything). `REFUTED` on E2 → the reader is wrong about
a true relationship and should be examined, not the specimen.

## Restart 1 — E1 reworded, and why

The first E1 asked for an item count in the statistics. `CacheStats.Items`
already exists at the pinned commit; the architect replied *"already
implemented"* in 70s with no plan, no routing and no closure round, which is
correct product behaviour and not an encounter. **My error, from an incomplete
read of the file.** The run is preserved as `E1.void1-task-already-implemented`.

The manifest's no-rewording rule exists to stop tuning a task after seeing how
the investigator handled it. Nothing about the investigator was observed here —
it never reached the region — so the rule is not what a rewording would break.
Recorded rather than argued.

- **E1 (replacement)** — *cache.stats() computes Items by asking the LRU for
  its length on every call. Track the item count as entries are added and
  evicted so stats need not consult the LRU, with no change to reported
  values.*

Written from the code; touches `add`, the `OnEvicted` callback and
`itemsLocked`; names no lock.
