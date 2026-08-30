# Slice 2 — the terminal-path survey, and what emitting a receipt revealed

The governor now emits one `run.receipt` at every terminal path. This is the
survey that produced the wiring, and the findings the wiring forced into the
open. **No schema requirement was relaxed to make a path complete.**

## Every terminal path

```text
lane / function          terminal              Outcome              CandidateState   complete today?
------------------------------------------------------------------------------------------------
execute (fail)           WorkflowFailed        FAILED               measured         depends how far it got
execute (fail, ctx done) WorkflowStopped       UNKNOWN  <-- F2      measured         NO
execute (architect reply)WorkflowCompleted     UNREVIEWED           NONE             NO  <-- F3
implement (inspect)      WorkflowCompleted     from the review      NONE             yes
implement (publish open) WorkflowStopped       UNKNOWN  <-- F2      measured         NO
implement (terminal)     Completed / Failed    from the review,     PRESENT          NO  <-- F1
                                               FAILED if publishing
                                               did not complete
runCandidate             CandidateNotAuditable (not a run terminal: it returns an error that
                                               reaches execute's fail, which emits the receipt)
resumeAuthority x4       WorkflowFailed        FAILED               measured         no (resume measures little)
Resume                   WorkflowFailed        FAILED               measured         no
runAssisted x2           Completed / Failed    UNREVIEWED / FAILED  NONE             NO  <-- F3
awaitChoice              AwaitingAuthority     not terminal: the run is resumable and settles later
```

`TestEveryTerminalPathEmitsAReceipt` parses the package and fails if any
function emitting a run terminal does not also emit a receipt. The wiring is
enforced, not described.

## What the governor measures, and when

Facts are recorded **at the moment of measurement**, never derived at the end:

```text
beginReceipt          before anything can fail -- so a run that dies at step one
                      still emits a record saying what it never reached
noteAwarenessProducer the executable this run launches for awareness
noteWorld             base commit + graph build commit, at the certified start
notePlan              the identity of the bound this run carried
noteCandidateCreated  the worktree exists (measured: the engine created it)
noteCandidateDigest   sha256 of the candidate diff, as the review binding uses
noteReviewerAssigned  one attempt opens; delivery UNKNOWN until a verdict lands
noteReviewDelivered   the verdict binds to the attempt that gave it
emitReceipt           Outcome and CandidateState are PARAMETERS, so a new
                      terminal path must decide both
```

## Findings

### F1 — the main success path cannot produce a complete receipt

An accepted candidate run reports `CandidateState = PRESENT`, and the schema
then requires the candidate's commit, tree and first parent. **The loop never
commits its candidate**, so all three are UNKNOWN and the receipt is INCOMPLETE.

This is C5's obligation 2, rediscovered at the production boundary without an
experiment. `noteCandidateCommit` exists and is called nowhere; the test proves
that the same run becomes COMPLETE the moment a committed candidate can be
measured. That is the Exact Candidate Admission slice, and this is its driver.

### F2 — a stopped run has no outcome in this vocabulary

The engine refuses to record a human stop as a failure, and it is right to:
recording it as FAILED would teach the behavioural record that the task shape
breaks. The schema's outcomes are ACCEPTED / REFUSED / FAILED / UNREVIEWED /
UNKNOWN, so a stopped run is recorded as UNKNOWN and is INCOMPLETE.

The gap is a **missing vocabulary member**, not a missing measurement. It is
left visible rather than papered over. A `STOPPED` outcome is a schema decision,
and the test records the gap so that adding it is a deliberate act.

### F3 — conversational terminals carry no plan

`plan_digest` is unconditionally required, but an architect's conversational
reply and the assisted lane produce no plan at all. Unlike F1 this is not a
missing measurement: **the artifact does not exist**. Making `plan_digest`
conditional on a plan axis would be the same conditional-evidence law already
applied to candidates and reviews, but that is a schema decision and is not
taken here.

### F4 — three identities the governor could not state, two now measured

The schema required `governor_commit`, `governor_binary_sha256` and
`serving_producer`, and the engine could state none of them. Two were
measurable from inside the process all along:

```text
governor_binary_sha256   sha256 of os.Executable()               NOW MEASURED
serving_producer         sha256 of the awareness executable      NOW MEASURED
                         this run launched (an IMAGE, and the
                         source says so -- C5 found a "producer"
                         field naming a file nobody had shown to
                         be executing)
governor_commit          runtime/debug vcs.revision              NOW MEASURED,
                                                                 and refused when
                                                                 vcs.modified
```

`go version -m` on a built `sensei-code` shows `vcs.revision` embedded by the
toolchain, so a binary built from a clean checkout states its commit. A binary
built from a **modified** tree refuses to name it: a governor claiming a
revision it is not would be the strongest kind of false precision.

### F5 — an architect's plan had no identity

`planDigest` returned `""` for every architect-authored plan; only supplied
plans had a digest. The bound that governs a run is an artifact, and an artifact
a run cannot name is one no later reader can check a candidate against. The
receipt now digests the architect's plan text, and `Source` distinguishes the
two rather than one field silently meaning two things.

## What this slice did not do

- No admission. The receipt reports evidence and grants nothing.
- No isolation. It stays external.
- No schema relaxation. Three terminals are INCOMPLETE and say why.
