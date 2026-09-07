# Historical generation — the defect specimen

**Label: `historical generation — authority incorrectly asserted`.**

Two statements, kept apart because only one of them is permanent.

```text
permanently true    the pre-#342 server asserted AUTHORITATIVE while the
                    certification predicate was not satisfied

open               under the repaired evaluator this generation MAY be
                   independently certifiable from evidence that already
                   existed at publication time -- see "What provenance does
                   exist" below. That would be retrospective VERIFICATION,
                   not a retrospective artifact.
```

This is the final record of the `:10122` graph generation that served
`github.com/globulario/sensei-code` up to 2026-09-04. It is evidence of **why
replacement was necessary**. Nothing in this file may be used to construct,
backfill, or infer a certification artifact for this generation. Reading
evidence that was already there is verification; writing evidence that was not
is fabrication, and the line between them is the date the bytes were produced.

Recorded after `globulario/sensei` PR #342 merged as `61605175f42a`, which
restored transaction certification as a conjunct of canonical graph authority.

## The generation

```text
live graph digest        579fc80963a65a5be1ba2d2eef383b7d4e3c6ba2b6fa6e89399167b7dd3cd4e2
triple count             34619
marker IRI               https://globular.io/awareness#seedBuild/sha256-579fc809...dd3cd4e2
marker file              <sensei-code>/.sensei/graph-authority.json
proof set                ~/.sensei/graph/8981c2aa48820b88   (store 127.0.0.1:7882)
published_unix           1788407181
```

## The propositions, as the running system reported them

```text
graph freshness          GRAPH_FRESHNESS_STATE_CURRENT
                         "live graph marker 579fc80963a6 and triple count 34619
                          match the expected artifact; store content not compared"
seed state               SEED_STATE_CURRENT
closure                  PROVEN for github.com/globulario/sensei-code
                         51/51 source identities projected, 0 missing,
                         0 unexpected foreign provenance, closure_proven: true
                         bound to marker digest 579fc809...
transaction              ABSENT
                         "runtime transaction stamp missing:
                          <sensei-code>/.sensei/graph-authority.transaction.tsv"
                         The file does not exist. The proof-set generation
                         directory for 579fc809... contains no transaction.tsv,
                         while an older generation (07e90a73...) does.
canonical authority      AUTHORITY_VERDICT_AUTHORITATIVE   <- asserted by the
                         serving binary, which predates the #342 repair
binary build stamp       BUILD_PROVENANCE_STATE_INCOMPLETE
coverage                 COVERAGE_STATE_SUFFICIENT
workspace composition    partial
reachability             stale — "10 authored corpus change(s) are admitted but
                         NOT reachable by the serving graph, built from 58c055fbd9d6"
```

**The contradiction this generation embodies:** it asserted AUTHORITATIVE while
holding no transaction certification at all. That is the exact defect #342
repaired, and it is why this generation is the specimen rather than the runway.

Classification as of 2026-09-04:

```text
publication/source provenance     partially and genuinely recorded by Receipt v2
canonical authority certification absent/incomplete
verdict                           NOT_AUTHORITATIVE
```

The v1 transaction stamp this generation lacks could not have certified it in
any case: `buildTransactionTSV` is hardwired around the awareness-graph/services
pair, so for this domain it emits both repository identities as `missing`. That
is the subject of `globulario/sensei` PR #343.

## The serving binary

```text
path                     /home/dave/.local/share/sensei/bin-sensei-code/awareness-graph
sha256                   1702b86a5a7cb0bfe80e2159716b15b0314664ff78ee8c89629cece727b57689
linked                   2026-09-02 23:46
ldflags                  -X main.Version=reflex-v2-frozen
                         -X main.BuildCommit=58c055fbd9d6
                         -X main.BuildTimeUnix=1788407139
main.SourceCommit        NOT SET   <- why build provenance reads INCOMPLETE
```

`58c055fbd9d6` is a `globulario/sensei` commit (merge of PR #339). It describes
when the **binary** was linked. It says nothing about which inputs produced the
graph — the distinction #342 made explicit.

## Process and unit identity

```text
pid                      2162, started 2026-09-03 10:45:35, ppid 1441
unit                     sensei-awareness-graph-sensei-code.service (systemd --user)
argv                     awareness-graph -addr :10122
                           -oxigraph-url http://127.0.0.1:7882/query
                           -no-seed
                           -graph-marker-file <sensei-code>/.sensei/graph-authority.json
                           -home-domain github.com/globulario/sensei-code
                           -awareness-dir <sensei-code>/docs/awareness
store                    sensei-oxigraph-sensei-code.service
                         oxigraph serve --location ~/.local/share/sensei/store-sensei-code
                         --bind 127.0.0.1:7882   (pid 1536)
```

`-no-seed` is the mechanism: the server adopts an existing store rather than
publishing one. Adoption is not publication, and it leaves nothing behind that
binds the served bytes to the inputs that produced them.

## What provenance does exist, and why it is not enough

A `publication.Receipt` v2 sits beside the marker and **is** bound to this
generation:

```text
domain                   github.com/globulario/sensei-code
revision                 ffad7d33fc9c1ec3d578cc923eb39905bab8b820
tree                     310a79c1800430cf1e46d8e7f9c3ff8cfc57d3ab
state                    CLEAN_EXACT
source path              docs/awareness
source digest            20d36dc62159b52b0cec53d998dfa85d1cb646c64607570b728b9097436e1baa
graph_generation         579fc80963a6...dd3cd4e2
```

This records an exact clean source revision. It is real provenance — and the
authority verdict does not read it. The conjunct reads
`seedmeta.RuntimeTransactionPath(markerFile)`, the v1 transaction TSV, which for
this generation does not exist.

Two records of the same publication, only one of which authority consults. That
gap is the subject of the contract report filed alongside this snapshot; it is
not repaired by writing a stamp now.

## Standing prohibition, and what it does not prohibit

Do not write `<sensei-code>/.sensei/graph-authority.transaction.tsv` for digest
`579fc809...`, and do not author any other certification artifact for it after
the fact. A stamp written today would assert that a publication which recorded
no such thing had certified it.

That prohibition is about **writing**, not about reading. The Receipt v2 above
was produced by the publication act itself, is bound to this generation by being
contained in it, and authenticates on its own recomputed identity. If the
repaired evaluator reads that already-existing evidence and every corrected
conjunct passes, this generation becomes certifiable without a single new byte
of provenance — a legitimate retrospective verification of a publication whose
contemporaneous record was always adequate, judged by an evaluator that was not.

If any required certification fact was not durably captured at publication time,
no reading can supply it and this generation stays uncertifiable.

Either way it is not A's runway. The live integration experiment gets a new
generation published under the repaired evaluator, so the boundary stays clean:

```text
old generation    the specimen that proves the defect
new generation    born certified under the repaired law
```
