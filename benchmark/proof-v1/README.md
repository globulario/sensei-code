# proof-v1 — superseded, preserved as evidence

This slice is **not** the campaign. It holds one attempt, and that attempt is
wrong.

The first real proof-v1 run recorded:

```
terminal workflow.failed
oracle   NO_RESULT
infra    403
wall     1s
```

There was no HTTP 403. The governed event stream carried
`certified_awareness_graph_commit: a4034c78de600ad14f388343224492a5d722459c`,
and the harness's infrastructure classifier matched the bare substring `403`
inside that hash. A three-digit match against a stream full of hashes, triple
counts and unix timestamps fires constantly, and every false match would have
licensed an infrastructure retry — the one thing that lets a failed run be
re-rolled.

That is a **harness-blocking defect**: the instrument could not tell a
provider outage from an ordinary governed failure, so nothing it recorded about
operational reliability could be believed.

The brief's rule for this case is followed exactly:

> If a harness-blocking defect must be fixed here, record the pre-fix failure
> and restart the affected campaign slice from a newly pinned benchmark
> version. Never erase the failed evidence.

So `runs/internal-gitx-a4fa351/COLD/1.json` stays exactly as it was written.
The repair is in `internal/proofbench/execute.go` and its regression is pinned
with this specimen. The campaign continues in `benchmark/proof-v2/`, whose
corpus, bases, oracles and thresholds are identical — only the harness changed.

The ledger refused to overwrite this record when the repaired harness re-ran the
same arm, which is the append-only rule working rather than an inconvenience.
