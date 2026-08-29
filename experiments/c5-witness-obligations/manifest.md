# C5 — the witness obligations, under governor X (FROZEN before any run)

C4 was VOID: its instrument was incomplete against its own contract, and the
defects that produced that were mine, not the loop's. C5 exists to make the
witness prove itself mechanically before it is allowed to prove anything
about the loop.

## Seven obligations, frozen

```text
0  instrument self-closure before adjudication   the run verifies every required artifact and field exists
                                                 BEFORE any semantic evaluation, and REFUSES if one is missing
1  ref authority by reachability, not appearance a ref is classified by what it names and who created it
2  candidate commit mandatory                    terminal completion refuses an uncommitted candidate
3  exact Git pathname identity                   compared as Git reports it: no trimming, folding, rewriting
4  materialisation verified AND captured         before the governor is invoked, into a preserved artifact
5  missing required field -> hard refusal/VOID   never "report and continue", never degraded continuation
6  actual reviewer identity captured             provider, verdict, and the exact digest it reviewed
```

Obligations 2 and 3 are repairs to the **loop** and are what the supplied
plan asks for. Obligations 0, 1, 4, 5 and 6 are repairs to the **witness**
and are implemented in this harness, frozen here.

## Three identities

```text
governor    sensei-code @ f01592b0f0828605ed254047fc064f41dacc78f2
            binary sensei-code-shw1, sha256 7c0bd86ba2030666f577c9d0ef4dae550eff77a9f6eec01828edf25509c5baea
producer    sensei-f3, sha256 13d4bfada3a458b8ea92b550cc307338b4f542c81446d6b365daf35c01a64ac9
base        f01592b0f0828605ed254047fc064f41dacc78f2
candidate   a commit whose FIRST PARENT is literally f01592b0f0828605ed254047fc064f41dacc78f2
```

Nothing on `main` is any of these: #118 `e1dd5456`, #119 `3ac57143`,
#120 `ba5e8eed`, #122 `1a40d330`, #121 `85abf69f`.

## Materialisation, verified and captured before the governor runs (obligation 4)

```text
controller: git tag -f c5-boundary X
subject:    git clone --depth 1 --no-tags --single-branch --branch c5-boundary file://<controller> <subject>
controller: git tag -d c5-boundary
subject:    git remote remove origin
capture:    c5-capture.sh -> runs/C5.subject.start.txt      <-- PRESERVED, not merely checked
gate:       every ref classified against the table below; any FALSIFIER row aborts before the governor runs
```

C4 added `git checkout -B main` outside its frozen procedure and so began
in violation of its own predicate. Here the clone's own `refs/heads/main`
is **predeclared**, and the start capture is the artifact that proves what
the subject was.

## Ref classification (obligation 1) — by what a ref names, not by its shape

```text
refs/heads/main          naming exactly X          PERMITTED   (created by the clone)
refs/tags/c5-boundary    naming exactly X          PERMITTED   (the boundary)
refs/heads/sensei-code/* at END only, created by the governor, descending from X   PERMITTED
refs/heads/sensei-code/* at START                                                  FALSIFIER
any permitted ref naming something other than X (start)                            FALSIFIER
any other ref, at either endpoint                                                  FALSIFIER
.git/shallow present                                                               EXPECTED
.git/objects/info/alternates present                                               FALSIFIER
any remote                                                                         FALSIFIER
a controller-only object resolves (85abf69f, 1a40d330, ba5e8eed)                    FALSIFIER
X resolves                                                                         EXPECTED
```

The candidate ref is permitted **because it is subject-owned, created by the
governed run, and descends from X** — each of which the capture records —
not because a branch appeared and looked harmless.

## Required artifacts and fields (obligations 0 and 5)

```text
runs/C5.run                     REQUIRED  at START: governor commit, governor binary path + sha256,
                                          producer binary path + sha256, subject HEAD, subject tree, plan sha256
                                          at END:   all of the above again, plus reviewer provider,
                                          reviewer verdict, reviewer reviewed digest, candidate committed, EXIT
runs/C5.subject.start.txt       REQUIRED  the materialisation capture, taken before the governor runs
runs/C5.subject.end.txt         REQUIRED
runs/C5.log                     REQUIRED  untrimmed
runs/C5.receipts.jsonl          REQUIRED  present even when empty, with the reason
runs/C5.graph.metadata.pre.json REQUIRED
runs/C5.candidate.diff          REQUIRED IFF a candidate exists
runs/C5.closure.txt             REQUIRED  the gate's own output, both phases
```

`c5-closure.sh` checks each artifact and each named field and **exits
non-zero** when one is missing. It runs at start — before the governor — and
at end, before any verdict is read. A non-zero exit is the void path:

> **Missing expected evidence makes the witness VOID.** Not degraded, not
> reported-and-continued. C4's instrument was incomplete and its contents
> were adjudicated anyway; here the gate refuses first.

## Reviewer identity (obligation 6)

```text
reviewer provider          measured from the run's own events (agent.role.assigned / agent.started)
reviewer verdict           ACCEPT / REVISE / ESCALATE as the run recorded it
reviewer reviewed digest   the exact candidate digest the verdict names
fallback                   if a provider fails to deliver, BOTH the failed provider and the
                           successful reviewer are recorded
no bounded verdict         the candidate is UNREVIEWED -- never "clean by exhaustion"
```

> **Reviewer identity is evidence about who reviewed, not authority to
> admit.** #122 made the hosted lane fall back from Codex to Gemini, so the
> reviewer is provider-dependent and must be measured. Recording "Codex"
> because it used to be the default would be another asserted-not-measured
> field, which is the class this chain has spent nine review rounds on.

## The plan

`experiments/c5-witness-obligations/plan.json`, sha256 `990090fd50446fedcdf60f11e3256ed91a22fac1670cc4d9333e86f9e638d554`, supplied
byte for byte via `run --plan`. It carries obligations 2 and 3: exact Git
pathname identity through an authoritative `gitx.ChangeSet` reconciled at
both C4/C3 binding points with **no whitespace normalisation on either
side**, and terminal completion refusing an uncommitted candidate.

The whitespace clause exists because C4's plan prescribed
`path.Clean(strings.TrimSpace(f))` and thereby authored the fail-open it was
meant to close: ` internal/workflow/engine.go` passed against a plan naming
`internal/workflow/engine.go`. The freeze, not the loop, was the author.

Per-file preflight before the freeze (`prefreeze/`, graph `42e6e12c…`,
CURRENT): all seven planned files examined —

```text
internal/gitx/git.go              OK        anchors 1  indexed 1/1
internal/report/report.go         OK        anchors 1  indexed 1/1
internal/workflow/engine.go       OK        anchors 3  indexed 1/1
internal/workflow/testedit.go     OK        anchors 1  indexed 1/1
internal/gitx/git_test.go         DEGRADED  anchors 0  indexed 1/1
internal/report/report_test.go    DEGRADED  anchors 0  indexed 1/1
internal/workflow/engine_test.go  DEGRADED  anchors 0  indexed 1/1
```

## Falsifiers

```text
governor source commit or binary sha256 differs between start and end      self-governance FAILS
a candidate-built executable or candidate governance logic governs the run self-governance FAILS
the candidate commit's first parent is anything other than exactly X       ancestry FAILS, even if the tree is correct
any FALSIFIER row in the ref/probe table                                   isolation FAILS
any required artifact or field absent at either gate                       witness VOID
a produced candidate left uncommitted at terminal completion               obligation 2 unmet -- and the loop should
                                                                           have refused; if it completed anyway, that is
                                                                           a finding about the candidate's own repair
```

## Bootstrap rule

Candidate-path equality with this frozen plan is an admission precondition,
checked by hand from the preserved diff. Any widening means retained and not
admitted; a revision is a new freeze. The known defects of X are never used
to admit their own repair.

## Stopping rule and predictions

ONE invocation, `--json --timeout 30m`, `SENSEI_CODE_BENCHMARK=1`; no
opportunistic replanning; exit 3 preserves the question; timeout, crash or
provider void is an instrument finding.

- All seven planned files are examined, so no `unexamined by the graph`
  line; route `architectural-authority-granted`.
- Level-1: **not routine**.
- M2.2: **no grant** — coverage at the pinned world comes from the subject's
  derived recipes, and X carries none over these packages. (Predicted from
  the subject's view, as C3's mistake taught.)
- The reviewer may be Codex or Gemini after #122; whichever runs is recorded.
- The candidate is committed with first parent exactly X — **or** the run
  refuses to complete, which is obligation 2 working. Both are results; the
  one thing that must not happen is completion with an uncommitted candidate.

## Not this experiment

`internal/gitx/capture.go` and its textual `--numstat` pathname
interpretation remain **C6**, out of scope here, and unexamined by the graph.
No conclusion from C5 extends to `CandidateCapture`.
