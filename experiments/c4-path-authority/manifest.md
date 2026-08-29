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

## C4 — 16:12:25Z–16:29:00Z, exit 0. Witness FAILS under its own frozen protocol.

### The claims, each against the rule as frozen

```text
governor pinned (source + binary, start = end)                       PASS
producer pinned (path + sha256, start = end)                         PASS
subject is LITERALLY X (HEAD f01592b…, tree fe43d481…, 0 parents)     PASS
controller objects unreachable (start and end)                       PASS
alternates absent, remotes 0 (start and end)                         PASS
frozen isolation predicate                                           FAIL
instrument complete (all seven required artifacts)                   PASS
candidate scope exact (the frozen seven paths)                       PASS
candidate repair independently accepted (Codex ACCEPT b4f471f096d1)  PASS
candidate committed                                                  FAIL
literal candidate parent == X                                        NOT ESTABLISHED
candidate admissible                                                 NO
C4 as a self-governance witness                                      FAIL, not VOID
```

### Isolation — FAIL, applied literally

```text
                          start                      end
refs                      main, c4-boundary          main, c4-boundary, sensei-code/task-1788019945987928256
X resolves                commit                     commit                    EXPECTED
ba5e8eed / 3ac57143 /
e1dd5456 resolve          no                         no                        falsifier not fired
alternates                ABSENT                     ABSENT                    falsifier not fired
remotes                   0                          0
.git/shallow              PRESENT                    PRESENT                   EXPECTED
```

The freeze said *an unexpected remote or ref falsifies isolation*, and
predeclared only `refs/tags/c4-boundary`. At end the subject carried a
`sensei-code/task-…` branch the governor created. **The predicate fires, so
the frozen isolation claim fails.** It is not called harmless here, and the
clause is not reinterpreted after the result — the tension was named in
writing before the run, and the rule is applied as written.

What the same capture shows is that the *controller-reachability* half
passed perfectly at both ends. So C4's finding is that the frozen predicate
was **too coarse**: it conflated a subject-owned ref created by the governed
workflow with a ref exposing controller state. That is a protocol-design
finding for the next witness, not a retroactive repair of this one.

### Candidate lineage — FAIL

```text
candidate worktree   .c4-subject-worktrees/task-1788019945987928256
candidate HEAD       f01592b…  (= X)      commits beyond X: 0
first parent         none — no commit exists
uncommitted          7 paths
```

The requirement was an actual commit whose first parent is X, precisely so
tree equality could not stand in for lineage. There is no commit, so the
proof is impossible rather than failed-by-comparison. **No commit was minted
afterwards.**

This exposes a distinction worth keeping:

> **candidate custody ≠ candidate lineage.** C4 had custody of the state —
> reviewed, audited, accepted — and no durable Git object whose parent
> relationship could be certified.

And it is a fact about the governor, not only about this experiment: X can
reach `workflow.completed: candidate ready for governed admission` while the
accepted candidate remains uncommitted. If a witness requires a committed
candidate, the workflow needs a governed step that produces the commit
*before* terminal completion, or terminal completion must refuse.

### What C4 did establish

**Literal ancestry-capable isolation is demonstrated.** The subject was
`f01592b…` itself, with prior history cut by the shallow graft and no
controller object reachable at either end. C3 could prove neither. That
mechanism is now measured rather than described, and it is reusable.

The repair was also independently accepted: validation passed, the audit
found nothing over 7 files, Level-1 refused the fast path, and Codex
accepted `b4f471f096d1` — *"establishes a NUL-parsed Git ChangeSet,
reconciles renderer output before both…"*. Reviewer acceptance is not
admission, and here two frozen witness falsifiers block it.

### Recorded disposition

> **Literal ancestry-capable isolation mechanism demonstrated; repair
> accepted; overall witness FAILED because the frozen ref-isolation
> predicate fired and the required candidate commit was absent. Candidate
> retained and unadmitted.**

### Owed to the next witness — and `capture.go` does not jump the queue

1. **Isolation must classify refs by authority, not appearance.** Frozen in
   advance: start refs are `main` and the boundary tag; end refs may
   additionally contain exactly the candidate ref this run created, which
   must be subject-owned, resolvable only inside the isolated object store,
   descended from X, and expose no controller-only object. Everything else
   still falsifies.
2. **Candidate commitment must be a workflow obligation**, not an
   experimental expectation — a governed step before terminal completion, or
   a refusal at it.

The `internal/gitx/capture.go` `--numstat` seam remains owed as C5 and does
**not** leapfrog these two: the self-governance chain closes its own witness
defects first.

### A third failure, and the frozen plan is its author

`75955b1d`, 2026-08-29 16:55:32Z, P1 against the candidate's code:

> a legal Git pathname with leading or trailing whitespace — ` internal/workflow/engine.go` — is collapsed by `TrimSpace` onto the plan's unspaced path, so an out-of-plan change is accepted. Reproduced against a real repository: `CandidateChanges` and `reconcileRenderedDiff` agreed on the distinct leading-space path, and `confineToPlan` still returned success.

Reproduced here against the preserved candidate, probe removed afterwards
(worktree unchanged at 7 modified paths):

```text
confineToPlan([" internal/workflow/engine.go"], planned=["internal/workflow/engine.go"]) -> nil
FAIL: a whitespace-distinct out-of-plan path was accepted: confinement failed OPEN
```

**The defect was specified by the frozen plan.** C4's plan required the check
to canonicalise "with `path.Clean(strings.TrimSpace(f))` exactly as
`architecturalFiles` does". The worker followed it faithfully. `TrimSpace`
appears at four sites in X's `testedit.go`, so the plan propagated a
pre-existing repository pattern into the one place where path identity is
the authority — and thereby reproduced, in a different alphabet, exactly the
class C4 existed to close: a legal Git pathname the confinement cannot see.

C3 lost quoted paths at the *renderer*. C4 gained Git-owned paths and then
**normalised them away at the comparison**. Authoritative acquisition is not
authoritative use.

It does not change C4's disposition — the candidate was already refused and
unadmitted on two other grounds — but it adds a third, and it relocates the
blame: this one is the freeze's, not the loop's.

Owed to C5, alongside the two witness defects:

> **A Git path is compared as Git reports it.** No trimming, no case
> folding, no separator rewriting; `path.Clean` only where a plan's own
> spelling must be canonicalised, and never applied to the Git-reported side
> in a way that can map two distinct paths onto one. The falsifiers are
> leading space, trailing space, and a path differing from a planned one only
> by surrounding whitespace.

### Correction — the isolation predicate was already true at STARTUP, and the deviation is the controller's

`151da20f`, 2026-08-29 17:01:18Z. My adjudication said the predicate "fires
at end on the candidate ref the governor created". That is **wrong about
when and about whom**:

```text
frozen verify step   refs = refs/tags/c4-boundary only
C4.isolation.start   refs = refs/heads/main refs/tags/c4-boundary
```

`refs/heads/main` existed **before the governor ran**. It was created by me,
at materialisation, with a step the freeze does not contain —
`git checkout -B main HEAD` — added so the subject would have a branch.
The frozen procedure is tag → clone → delete tag → remove origin → verify;
that extra checkout is not in it.

So the corrected finding is:

- The isolation predicate was violated at **startup**, by the **controller's
  deviation from its own frozen materialisation procedure**, not first by
  the governor's candidate ref at end.
- The earlier attribution ("fires at end on the candidate ref") stands
  corrected. It made a controller error look like a governor artefact, and
  it let me claim the freeze had been applied literally while I had
  literally departed from it before the run began.

**Consequence for the mechanism claim.** The manifest said C4 demonstrated
"literal ancestry-capable isolation". Narrowed to what the evidence supports:

> The **pre-freeze validation** produced a conforming subject — HEAD == X,
> tree `fe43d481…`, X with 0 parents, 0 remotes, alternates absent, refs =
> the boundary tag only, no controller object resolvable. That is where the
> mechanism was demonstrated.
>
> **C4's own run did not use a conforming subject.** Its subject carried an
> extra ref from the start, so C4 did not demonstrate the mechanism; it
> demonstrated a subject that was literally X with an unfrozen ref added.

The controller-reachability results (no controller object resolvable,
alternates absent, remotes 0, at both ends) are unaffected and remain PASS —
they were measured on the subject as it actually existed.

This is the third instance in this record of the same shape, now committed
by the adjudicator rather than the loop: **a claim asserted about a property
the record itself contradicts.** C5 owes a fourth item —

> **The run must verify the frozen materialisation procedure produced what
> the freeze says it produces, before the governor is invoked, and refuse if
> it did not.** A setup step outside the frozen procedure is a deviation
> whether or not it looks harmless, and the start capture is where it is
> caught.
