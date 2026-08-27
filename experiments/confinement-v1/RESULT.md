# confinement-v1 — result: the second family works at the mechanism, and the investigator found it untold

```
fixture   golang/mod @ d0a27b2 · 4384 triples · PROVEN 0/0 · authoritative · substrate sensei@e81d7bed (main)
family    command_invocation_confined_to — first governed-run exercise ever

E1    15:21:20Z  exit 3  cold  RECORDED   "gosumcheck.exe" confined to gosumcheck, searched under gosumcheck
E2a   15:27:28Z  exit 3  cold  DUPLICATE  (investigator's recipe alone)   0 anchors  — pre-derived UNKNOWN
E2b   15:30:56Z  exit 3  cold  DUPLICATE  (sealed specimen alone)         1 anchor   gosumcheck/main.go [invocation confinement]
```

## What the investigator did, untold

Given a cold gap over `gosumcheck/main.go` and a task about a log prefix, it
chose **executable ownership** as the architectural question — the second
family, never named to it — and grounded it in a real invocation:
`gosumcheck/test.bash` builds and runs `gosumcheck.exe`. A sensible question
about the uncovered test path. Then in E2b, with the same region, it proposed
`"go" confined to gosumcheck, searched repository-wide` — **the sealed specimen,
exactly**, `DUPLICATE` against it. The closure prompt carries the gap and the
planned files, never the recipe file.

Three proposals, all in the right family, two of them distinct real relations.

## The chain, per arm

**E2a — the investigator's question.** Pre-derived and sealed `UNKNOWN`: this
reader reads Go source for literal `exec.Command`, and a shell-script invocation
is outside its envelope. Inside the run: `0 anchors`, cold. A grounded question
the family cannot read earned nothing. Fail-closed, and correctly labelled
*unknown* rather than false.

**E2b — the sealed specimen.** `DERIVED` inside the governed run: **1 anchor,
requirement `invocation confinement`**, mapped by the relevance gate. Link 5
and the family-to-requirement mapping both hold for a family that is not lock
discipline. The route stayed cold for a reason the gate is *supposed* to
enforce: the plan spans two files, the anchor covers one, and partial coverage
is not coverage (`TestDerivedCoverageClosesAGapOnlyWhereItWasDerived`). The
second file is `test.bash`, which no derivation family reads.

## What this establishes

- The mechanism is **not a lock analyzer**. Recipe → derive → anchor →
  requirement-mapped coverage works for a structurally different relation
  (invocation sites and their owning package), on a third foreign repository.
- The investigator formulates questions in this family without being told the
  family exists in the vocabulary, and converges on the true relation.
- Fail-closed holds at a new edge: a question outside the family's envelope
  (shell script) is `UNKNOWN`, earns nothing, and says why.

## What it does not establish, and the two envelope findings

- Links 6–7 for this family were not observed. The blocker was the plan's
  second file, not the anchor — a fair rule producing an honest stop. A task
  whose plan stays inside `main.go` would test them; not run here, and not
  reworded after the fact.
- **Envelope finding 1 (family):** the confinement reader sees only Go-literal
  invocations. Build/test harnesses in shell are where a project's real
  process-spawning often lives, and the investigator went straight to one. The
  family's `Limits()` should say this in so many words.
- **Envelope finding 2 (onboarding):** the graph must be built under the
  repository's declared identity (`github.com/golang/mod`), not the module
  path (`golang.org/x/mod`); the start gate refuses a mismatch as *composition
  partial*. Recorded as `E1.void1-domain-mismatch`.

## Selection integrity

Two literal sites exist; the frozen rule (stable path order) picked
`gosumcheck/main.go` ahead of `zip/zip.go`. Both hand-derive `DERIVED`,
sealed in `selection.json`, never shown to the investigator. The earlier
read-only scan had named `zip.go`; the rule was not adjusted to match it.
