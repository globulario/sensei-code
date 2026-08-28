# Implementation brief: a planned file the graph never examined is not covered by its neighbour

Slice 3 of Phase B3, executing M25 §1 (mediation ledger, 2026-08-28):

> A neighbouring planned-file anchor does not establish coverage for another
> changed file unless a deterministic relation binds the governing law to that
> changed surface.

## Falsification before design

The law was stated from N3's record. Re-measured per file, N3 itself was not
the counterexample: `suppliedplan.go` alone is *examined* by the graph
(`indexed 1/1, no governing rule`), and under `Coverage.Proven` examined
silence is coverage — the graph looked and found no law. That reading of N3
is withdrawn in the B3 manifest.

The live counterexample is one step over. On graph `42e6e12c…`:

```text
files                                              status  sufficient  anchors  file_count  indexed
internal/workflow/zz_not_in_graph.go (nonexistent) —       —           —        —           —
internal/workflow/relevance.go (real, unindexed)   EMPTY   false       0        1           0
engine.go + zz_not_in_graph.go                     OK      true        3        2           1
engine.go + relevance.go                           OK      true        3        2           1
```

`Coverage.Proven()` is true on the last two: the region is proven by one
file, and the other — real or nonexistent — inherits it. The router read
coverage at the region (`coverageAbsent := !scoped.Coverage.Proven()`), so a
plan could carry any ungrounded file into an anchored region and launder the
region's authority onto it. Pinned as the failing specimen
`TestAnUnexaminedPlannedFileIsNotCoveredByItsNeighbour` before any fix.

A naive fix — require `indexed_file_count == file_count` — is wrong on N3's
own shape: its planned set read `file_count=3, indexed=2`, and the unexamined
file was the granted test, which is not asked to be covered (M2.2). Which
files are examined is graph state, not a file-type rule (`engine_test.go`
alone is examined; `suppliedplan_test.go` was not), so only a per-file answer
establishes it.

## The change

- `Action.Unexamined` — engine-owned, established per file by
  `unexaminedFiles`: whenever a grant is on the table (a route the region
  answer alone would grant, or a human-owned route the human has authorised),
  every architectural planned file is asked on its own, and a file is
  examined only when its **own** answer's `Coverage.Proven()` is true. There
  is no shortcut on the region's counts — a fully indexed region can still
  hold a file that alone publishes `sufficient=false` (found by the owner's
  review of `67f68f9`). A per-file answer that cannot be obtained, is not
  certifiable, names a different graph generation, is DEGRADED for a
  non-coverage reason, or does not describe exactly one file, is an error the
  run refuses on — never an unexamined file. Region counts that describe a
  different plan fail closed. Never read off the scoped answer, never
  supplied by a provider.
- Router (`authority.go`): unexamined architectural files are a coverage gap,
  `Kind: coverage-unexamined`, scope = those files, closed only by the existing
  relation `derivationClosesGap` over **every** architectural file. Files under
  an operational grant are ignored. Approval gate and consequence assessment
  still precede it; nothing here grants.
- The routing event names the unexamined files beside anchors and grants.

## Proof

- `TestAnUnexaminedPlannedFileIsNotCoveredByItsNeighbour` — the specimen: the
  region-level answer grants (live shape asserted first), the per-file fact
  refuses, the refusal names the file, the gap identity is the file.
- `TestAnUnexaminedPlannedFileIsCoveredOnlyByADerivationOverIt` — a recognised
  derivation over every planned file closes it; over the neighbour only, over
  the unexamined file only, or from an unrecognised family, it stays open.
- `TestAnUnexaminedFileUnderAnOperationalGrantOpensNoGap` — N3's shape keeps
  routing as it did.
- `TestUnexaminedFilesAgainstTheLiveGraph` (`-tags acceptance`) — the same
  region against the real deployment.

## Governance provenance

Hand-governed self-repair (M26): Sensei's evidence tools governed every
edit; sensei-code's execution loop did not own the change's lifecycle. Merged
at `f01592b0f082` after eleven exact-head Codex rounds, one owner-found P1,
and the owner's review of `b8349be` (synthetic merge into `549a583`, Go
tests/vet/build, Sensei enforcing gate: 0 blocking, 0 advisory).

## Not this slice

The Level-1 routine classifier (`routine.go`) reads the same region-level
`Proven()`. It runs only after a grant, so the router's refusal precedes it;
recorded as an adjacent surface, not repaired here. Preflight is not changed
to read derived recipes (M25 §3).
