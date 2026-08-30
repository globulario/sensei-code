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
subject:    git checkout -B main X          <-- AMENDED PRE-RUN, see "Pre-run amendment"
capture:    c5-capture.sh -> runs/C5.subject.start.txt      <-- PRESERVED, not merely checked
gate:       every ref classified against the table below; any FALSIFIER row aborts before the governor runs
```

C4 added `git checkout -B main` outside its frozen procedure and so began
in violation of its own predicate. ~~Here the clone's own `refs/heads/main`
is **predeclared**~~ — **FALSE, corrected pre-run**: measurement shows
`clone --branch <tag>` leaves a DETACHED HEAD and creates no
`refs/heads/main` at all (`prerun-validation.txt`). The step is now inside
the procedure, executed by `c5-run.sh` before the start capture, so the
capture measures its result instead of a human typing it afterwards. The
start capture remains the artifact that proves what the subject was.

## Ref classification (obligation 1) — by what a ref names, not by its shape

```text
refs/heads/main          naming exactly X          PERMITTED   (created by the procedure's checkout -B, not by the clone)
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
runs/C5.candidate.diff          REQUIRED IFF a candidate EXISTS -- a commit, a candidate-shaped
                                          ref, or a live worktree; "exists" is not "was committed".
                                          It must be PRESENT; a zero-byte diff is evidence only when
                                          C5.run records `candidate diff empty because ...`
runs/C5.closure.txt             REQUIRED  the gate's own output, both phases
runs/C5.materialise.txt         REQUIRED  the materialisation transcript (added pre-run)
runs/C5.extract.txt             REQUIRED  the mechanical extraction from the log (added pre-run)
                                          C5.run also carries, at START: harness pinned,
                                          producer serving at start (MEASURED, never "unknown");
                                          at END: producer serving at end, producer serving stable,
                                          reviewer attempts, reviewer providers attempted
                                          IFF a candidate is committed, C5.run additionally carries
                                          candidate ref, candidate head, candidate parent,
                                          candidate ancestry X->candidate  (all MEASURED)
                                          and, at END always: candidate state exists
```

`c5-closure.sh` checks each artifact and each named field and **exits
non-zero** when one is missing. It runs at start — before the governor — and
at end, before any verdict is read. A non-zero exit is the void path, and it
judges **completeness only**; see "Fourth pre-run amendment" for the order
that keeps FAIL from collapsing into VOID:

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


## Pre-run amendment (appended to the `ddbf956` freeze; nothing removed)

`ddbf956` froze prose stronger than its own harness. Reviewed before any run,
six defects, all in the instrument:

```text
1  c5-capture.sh printed FALSIFIER and exited 0        the abort was left to whoever read the output
2  candidate-ref ownership was asserted, not measured  no check that the ref is the one the run reported
3  ancestry was printed, never enforced                "descends from X: no" did not fail anything
4  C5.closure.txt was REQUIRED and unchecked           the gate did not gate its own receipt
5  C5.candidate.diff was REQUIRED-IFF and unchecked    the conditional artifact had no condition in code
6  fields were checked by LABEL, not by VALUE          `subject tree ` with no value passed
plus: no committed runner bound capture -> gate -> governor; that binding
      was going to be an after-freeze shell sequence, which is exactly how
      C4 acquired the out-of-procedure `checkout -B main` that voided it.
```

Repairs, all pre-run, none of them a change to what C5 must prove:

- **`c5-run.sh` is the runner** and is itself hashed into `C5.run`. It refuses
  to overwrite an existing record (ONE invocation), verifies the pinned
  governor, producer and plan digests before materialising anything, and
  aborts *before invoking the governor* if the start capture or start gate
  fails. An abort before the governor is an instrument refusal; an abort
  after it is VOID.
- **The capture is the gate.** Every falsifier row increments a counter and
  the script exits non-zero. It classifies a candidate ref by its **exact
  first parent**, not by `merge-base --is-ancestor`: a ref whose first parent
  is X is the candidate; a ref naming X *itself* is permitted but annotated
  `no candidate commit exists`; anything else is a FALSIFIER.
- **The expected candidate ref comes from the governor's own log** —
  `refs/heads/sensei-code/<task_id>` read from `task.created` — and the
  capture refuses any candidate-shaped ref that is not it. The run reports;
  the capture measures; they must agree.
- **The closure gate reads the captures** and refuses any `FALSIFIER` row, and
  requires the `ISOLATION GATE: PASS` line to be present.
- **A field counts only with a non-empty value**, and a third phase
  (`closed`) checks `C5.closure.txt` for both receipts, closing the loop the
  frozen table opened.

### One measurement the amendment forced into the open

The binary answering awareness on `:10122` during this validation was
`awareness-graph` sha256 `1070ee8dedeec323a47ebdaa360c390fd9e7d0e68826f7e7679216960f8b74eb`
— **not** the frozen producer `sensei-f3` `13d4bfad…`, which is a file on
disk that no run has been shown to execute. C3's and C4's `producer binary
sha256` therefore recorded a **file**, not the **process that answered**:
the same asserted-not-measured class this chain has spent nine rounds on.
The frozen field is still recorded literally, but **relabelled for what it
is**: `sensei-f3 13d4bfad…` is a *frozen reference file identity, not a
demonstrated serving process*, and `C5.run` says so on the line itself. The
producer of C5's evidence is the measured serving process, recorded in
`producer serving at start` / `at end`, **required to be non-`unknown` at
both ends**, and required to be **the same executable at both ends**
(`producer serving stable`); the closure gate refuses otherwise. C5 does not
retroactively repair C3 or C4 — it stops repeating them.

### Pre-run validation, recorded

~~`prerun-validation.txt` is the transcript: seven capture cases ... and
eight gate cases ... Every case produced the intended exit status.~~
**WITHDRAWN — the claim was false when written.** That transcript contained
two cases whose stated expectation did not match their actual exit status
(`T4` expected 1 and returned 0; `T4b` expected 0 and returned 1, because a
ref left behind by an earlier case was still in the subject), and its own
arithmetic was wrong: seven plus eight is fifteen, not fourteen. A
hand-written transcript of a validation is prose, and prose let both errors
sit in the record looking like passes.

`prerun-validation.txt` is now **generated by `c5-validate.sh`, which is a
gate**: every case declares its expected exit status, the script compares,
counts, and itself exits non-zero if any case disagrees. The count in the
file is printed by the script, not asserted by me. Each capture case gets its
own freshly materialised subject, so no case can inherit another's refs.
**This is instrument validation, not a witness**: it proves the harness
refuses, not that the loop works.


## Second pre-run amendment (`ddbf956` and `9a70ae7` both preserved)

The first amendment's own validation falsified its readiness claim. Four
further defects, all found before any run:

```text
1  the validation transcript contained two failed expectations reported as   the enumeration was prose
   passes, and miscounted its own cases (fourteen for fifteen)
2  c5-capture.sh still had one fail-open counter: HEAD != X printed          the capture's own claim to exit
   [FALSIFIER] and never incremented the counter                            non-zero on every falsifier was false
3  the measured serving producer was informational only: not required,       the newly discovered problem was
   "unknown" accepted, identity never compared across the run                recorded but not closed
4  the runner recorded its own digest instead of refusing drift              recording drift is weaker than
                                                                            refusing it
```

Repairs:

- **`HEAD != X` now calls `fal`**, so the capture exits non-zero on every
  falsifier it prints, as it always claimed to.
- **`c5-validate.sh` is the validation, and it is a gate.** Fresh subject per
  capture case; every case asserts its expected exit status; the script exits
  non-zero if any case disagrees and prints its own case count.
- **The serving producer is required evidence**: `producer serving at
  start`/`at end` must be present and must not read `unknown`, and
  `producer serving stable` must be `yes`. `sensei-f3` is relabelled a frozen
  reference file identity, not the producer.
- **The harness pins itself before it touches the subject.** `c5-run.sh`
  carries the SHA256 of `c5-capture.sh`, `c5-closure.sh` and `c5-extract.py`
  and refuses any mismatch; it also refuses if any of the four harness files
  differs from its committed state, and records the pinning commit and its
  own blob. A runner cannot pin its own digest inside itself; that boundary
  is stated rather than papered over, and the blob is verifiable in git.
- **Obligation 6 now has the fallback case it lacked.** `c5-extract.py` is a
  separate, tested file that keeps an ordered trail of every `agent.*` /
  `review.*` event and every provider named by one, so a run in which Codex
  fails and Gemini delivers records **both**, not just the one that
  answered. Case `E1` proves it; `E2` proves a missing verdict reads
  `UNREVIEWED`.

`prerun-validation.txt`: **32 cases, 32 passed**, counted by the script —
ten capture, nineteen closure, two extractor, one runner-pinning.


## Third pre-run amendment (all of `ddbf956`, `9a70ae7`, `22ff7b6` preserved)

Two wires were missing between this manifest and its gate. Both mechanical,
both found by reading the freeze against the code rather than by a run:

```text
1  C5.materialise.txt and C5.extract.txt were declared REQUIRED and the      the manifest's claim that the gate
   gate checked neither                                                     checks each required artifact was false
2  the manifest said REQUIRED IFF A CANDIDATE EXISTS; the gate required      an uncommitted candidate state --
   the diff only when `candidate committed yes`                             C4's actual end state -- passed with
                                                                            its diff absent
```

- `C5.materialise.txt` is required at the **start** gate, `C5.extract.txt` at
  **end** and **closed**.
- The candidate-diff rule now means what the table says. The runner measures
  `candidate state exists` from three independent signals — a candidate
  commit, a candidate-shaped ref, or a live worktree — and preserves the diff
  for whichever holds. The gate requires the artifact whenever that field is
  `yes`, requires a recorded reason when the diff is zero bytes, and
  **cross-checks the field against the measured ref count**: refs present
  with `candidate state exists no` is a misreport, not a pass. The stronger
  reading was taken deliberately: C4 had to reconstruct an uncommitted
  candidate's diff after the fact, and a witness should never rebuild its own
  evidence.

Seven new cases prove it: `G13` (materialisation transcript deleted), `G14`
(extraction record deleted), `G15`/`G16` (uncommitted candidate state without
and with its diff), `G17`/`G18` (empty diff without and with a recorded
reason), `G19` (candidate refs present while the run reports no candidate
state).


## Fourth pre-run amendment — classification order

The gate rejected `FALSIFIER` lines in the **end** capture as well as the
start one. So a run that completed perfectly, recorded every artifact and
every field, and then truthfully measured

```text
candidate first parent != X   [FALSIFIER]
```

would have been reported `WITNESS VOID (end closure)`, and the branch meant
to report `isolation FAILS` was **unreachable for exactly the cases it
exists to classify**. That is C4's mistake in miniature — a real measurement
disappearing into an instrument verdict — caught before execution instead of
after.

The frozen distinction is an ORDER, and it is now the code's shape:

```text
instrument complete?
    NO  -> VOID           (nothing may be adjudicated)
    YES -> read the held capture status
              CAP_RC = 0  -> the isolation predicate held
              CAP_RC != 0 -> VALID WITNESS, isolation FAIL   <- adjudicate it
```

- **START** keeps rejecting falsifiers: a subject that is not what the freeze
  says is a precondition refusal, before anything is governed.
- **END / CLOSED** check that `C5.subject.end.txt` is *complete* — it has a
  `falsifiers fired: N` count, a terminal `ISOLATION GATE: PASS|FAILED` line,
  and its ref-classification section — and say nothing about what it found.
  A truncated capture is VOID; a complete one reporting a falsifier is a
  valid witness.
- The runner **holds** the end capture's exit status until both gates pass,
  then classifies. Its exit codes are three-valued: `0` complete and the
  predicate held, `1` VOID, `2` valid witness with isolation FAIL.

`G20` supplies a complete end capture containing a real falsifier and
requires closure to **pass** (so FAIL survives as FAIL); `G21` supplies a
truncated one and requires VOID; `R2` asserts structurally that the runner
interprets the held status only after both gates and that FAIL has its own
exit code. **35 cases, 35 passed.**

## OUTCOME: VOID (2026-08-30)

```text
falsifiers fired: 0
ISOLATION GATE: PASS (start)
instrument closure OK (start)
AttributeError: 'str' object has no attribute 'get'   c5-extract.py:33
C5 ABORT: extraction failed; the reviewer identity is unrecorded
RUNNER EXIT: 1
```

The governor executed. The witness never reached an end capture, never
recorded a reviewer identity, and never closed its instrument. Applying the
frozen rule literally — **missing expected evidence makes the witness VOID,
not failed** — C5 is VOID.

**Nothing about the governed loop may be read from this run.** Obligation 2
(candidate commitment), obligation 3 (exact Git pathname identity),
obligation 6 (reviewer identity) and the terminal state are **UNADJUDICATED,
not failed**. The extractor was **not** repaired and re-run over the preserved
log to complete the record, and the log's semantic content was not read.
Diagnosis was bounded to the payload shape that broke the parser.

### The defect, and where it came from

`c5-extract.py` guarded the outer payload with `isinstance(p, dict)` and then
assumed the nested `provenance` was a dict. One event in 4425 —
`kind=mode.selected` — carries `payload.provenance` as a **string**.

The four pre-run review rounds hardened every gate in the harness. The run
died in the one component whose test fixtures its author invented:
`c5-extract.py` was validated against two synthetic logs written from Claude's
model of the event stream, and never once against `C4.log`, which was already
in this repository. **C4.log contains the identical crashing shape.** A single
execution against the real log would have caught it before the freeze.

A parser validated only against specimens its author invented is a claim
standing where a measurement was available — the class this whole chain has
been repairing, one level below where anyone was looking.

### Shape census (parser corpus, never semantic evidence)

Over `C4.log` + `C5.log`: **9123 events, 21 distinct kinds, 0 unparseable
lines, and exactly one polymorphic field name in the entire corpus** —
`payload.provenance` (`str` ×2, `dict` ×2). The crash class is one field wide.
Using preserved logs to ask *what shapes exist in this protocol* tests an
instrument against historical bytes; it does not adjudicate what those bytes
say about the runs that produced them.

### Two design faults this exposes in the witness itself

1. **An unrelated physical measurement was made to depend on a fragile
   semantic one.** The end isolation capture needed the candidate ref from the
   extractor, so an extractor defect destroyed the end-state measurement too.
   The end capture must be **unconditional and raw** — HEAD, tree, every ref
   and target, first parents, remotes, alternates, worktrees — with binding to
   the run-reported identity performed **afterwards**.
2. **Recovering the expected ref from the subject is rejected as circular.**
   A capture that chooses its expectation from the subject then proves the ref
   it just chose, collapsing the run-reported versus subject-contained
   distinction the witness exists to maintain.

### What follows

Not a larger C5b around the same event-log reconstruction. The 673-line
external witness failed at a 62-line parser, which is evidence *from the
instrument* — independent of anything the run contained — that identities the
governor already knows should be emitted through a product-owned, versioned,
**total-parse** receipt schema instead of being reconstructed afterwards from
a general-purpose event stream. The next witness is smaller by design.

`C6` remains reserved for `capture.go` / `--numstat -z` and is not consumed
by this.
