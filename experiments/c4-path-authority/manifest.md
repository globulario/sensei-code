# C4 — path identity as authority, under governor X (FROZEN before any run)

C3 established *when* authority binds and implemented it; its candidate was
refused because *what* it bound was incomplete — a path Git quotes was
invisible to confinement. C4 changes the identity set, not the timing.

## Three identities

```text
governor    sensei-code @ f01592b0f0828605ed254047fc064f41dacc78f2
            binary sensei-code-shw1, sha256 7c0bd86ba2030666f577c9d0ef4dae550eff77a9f6eec01828edf25509c5baea
base        f01592b0f0828605ed254047fc064f41dacc78f2
candidate   a commit whose FIRST PARENT is literally f01592b0f0828605ed254047fc064f41dacc78f2
```

Nothing on `main` — #118 `e1dd5456`, #119 `3ac57143`, #120 `ba5e8eed` — is
governor, base or candidate authority.

## Materialisation — measured mechanism, not incidental setup

```text
controller: git tag -f c4-boundary X
subject:    git clone --depth 1 --no-tags --single-branch --branch c4-boundary file://<controller> <subject>
controller: git tag -d c4-boundary
subject:    git remote remove origin
verify:     HEAD == X, tree == fe43d481424072f325a7f315f1a5cfa5ab70609f, X has 0 parents,
            0 remotes, alternates ABSENT, refs = refs/tags/c4-boundary only
```

`--revision` is unsupported by this Git, so the boundary is pinned by a
temporary controller tag that is deleted immediately; the subject keeps
`refs/tags/c4-boundary` naming X, which is the subject's own ref and part of
the mechanism. This is why C3's two identity caveats do not recur: the
subject **is** X rather than a re-materialisation of it, so a candidate
committed here has literal parent X.

## Isolation — REQUIRED instrument evidence, interpretation frozen

Captured into `runs/C4.isolation.start.txt` and `runs/C4.isolation.end.txt`:
alternates path and content, remotes, all refs, HEAD, tree, working-tree
state, and object probes.

```text
X = f01592b0f0828605ed254047fc064f41dacc78f2 resolves                     EXPECTED -- it is the subject
.git/shallow present                                                EXPECTED -- the mechanism
refs/tags/c4-boundary naming X                                      EXPECTED -- the subject's own ref
a controller-only object resolves (ba5e8eed, 3ac57143, e1dd5456)    isolation FALSIFIED
.git/objects/info/alternates present                                isolation FALSIFIED
an unexpected remote or ref                                         isolation FALSIFIED
either isolation record absent                                      witness VOID
```

## Required artifacts — the protocol is closed over its outputs

```text
runs/C4.run                      REQUIRED  governor commit + binary sha256 at start AND end;
                                           producer binary path + sha256 at start AND end;
                                           subject HEAD and tree at start and end; plan sha256; exit
runs/C4.isolation.start.txt      REQUIRED
runs/C4.isolation.end.txt        REQUIRED
runs/C4.log                      REQUIRED  untrimmed
runs/C4.receipts.jsonl           REQUIRED  present even when empty, with the reason
runs/C4.graph.metadata.pre.json  REQUIRED
runs/C4.candidate.diff           REQUIRED IFF a candidate exists
```

> **Missing expected evidence makes the witness VOID, not failed.** An empty
> receipts file is evidence; an absent one is an instrumentation defect.

## The candidate must be committed

C3's worker left four modified files and no commit, so its identity was
carried by bytes alone. **A candidate must be committed inside the subject.**
If none is committed, that fact is recorded as an instrument finding and no
commit is synthesised afterwards to repair the record.

And the ancestry falsifier, stated so tree equality cannot quietly stand in
for lineage:

> **If the candidate commit's first parent is anything other than exactly
> `f01592b0f0828605ed254047fc064f41dacc78f2`, the self-governance ancestry claim FAILS even if its tree is
> correct.**

## Producer identity

```text
producer binary  /tmp/claude-1000/-home-dave-Documents-github-com-globulario-sensei-code/14e477ec-1052-4b6a-b741-ba143440786d/scratchpad/sensei-f3
                 sha256 13d4bfada3a458b8ea92b550cc307338b4f542c81446d6b365daf35c01a64ac9  (recorded at start and end)
source revision  not self-reported by the binary; the run records what it can measure
```

C3 asserted a producer revision no artifact captured. C4 records the binary
it actually used.

## The plan

`experiments/c4-path-authority/plan.json`, sha256 `97e520945f0b3fb84104e042ff1423fd1cfaf7b4b37d3c2a1d497e58aa6a4694`, supplied byte
for byte via `run --plan`.

Per-file preflight before the freeze (`prefreeze/`, graph `42e6e12c…`):

```text
internal/gitx/git.go              OK        anchors 1  indexed 1/1   examined
internal/report/report.go         OK        anchors 1  indexed 1/1   examined
internal/workflow/engine.go       OK        anchors 3  indexed 1/1   examined
internal/workflow/testedit.go     OK        anchors 1  indexed 1/1   examined
internal/gitx/git_test.go         DEGRADED  anchors 0  indexed 1/1   examined
internal/report/report_test.go    DEGRADED  anchors 0  indexed 1/1   examined
internal/workflow/engine_test.go  DEGRADED  anchors 0  indexed 1/1   examined
```

## Bounded non-claim — C5

`internal/gitx/capture.go` still interprets pathnames textually, splitting
`--numstat` output on tabs after `TrimSpace`. It is unexamined by the graph
(`indexed 0/1`), so it cannot be governed by X today and is **outside C4**.

> **C4 does not establish the artifact boundary's pathname completeness. No
> conclusion from C4 extends to `CandidateCapture`.** That migration to
> `-z` is C5, once the graph has examined the file.

## Falsifier (M28) and the bootstrap rule

The governor may inspect, validate, audit and review candidate content, but
the executable governor and its authority-producing code remain pinned to X:
governor source commit and binary sha256 equal at start and end, and no
candidate-built executable or candidate governance logic participates in
governing this run.

Candidate-path equality with this frozen plan is an admission precondition,
checked by hand from the preserved diff. Any widening means retained and not
admitted; a revision is a new freeze.

## Stopping rule and predictions

ONE invocation, `--json --timeout 30m`, `SENSEI_CODE_BENCHMARK=1`; no
opportunistic replanning; exit 3 preserves the question; timeout, crash or
provider void is an instrument finding.

- All seven planned files are examined, so no `unexamined by the graph` line;
  route `architectural-authority-granted`.
- Level-1: **not routine** (critical invariants govern `engine.go`).
- M2.2: **no grant** for the three test files — coverage at the pinned world
  comes from the subject's derived recipes, and X carries none over
  `internal/workflow`, `internal/gitx` or `internal/report`. (C3's
  prediction 3 was written from the controller's view of the live graph and
  was wrong; this one is written from the subject's.)
- Governor and producer identity equal at start and end; isolation records
  present at both ends with no falsifier fired; the candidate is committed
  with first parent exactly X.
