# Slice 2 — the terminal-path survey, and what emitting a receipt revealed

The governor now emits one `run.receipt` at every terminal path. This is the
survey that produced the wiring, and the findings the wiring forced into the
open. **No schema requirement was relaxed to make a path complete.**

## Every terminal path

```text
lane / function          terminal              Outcome              CandidateState   complete today?
------------------------------------------------------------------------------------------------
execute (fail)           WorkflowFailed        FAILED               measured         depends how far it got
execute (fail, ctx done) WorkflowStopped       STOPPED              measured         yes
execute (architect reply)WorkflowCompleted     UNREVIEWED           NONE (plan NONE) yes
implement (inspect)      WorkflowCompleted     from the review      NONE             yes
implement (publish open) WorkflowStopped       STOPPED              measured         yes
implement (terminal)     Completed / Failed    from the review,     PRESENT          NO  <-- F1
                                               FAILED if publishing
                                               did not complete
runCandidate             CandidateNotAuditable (not a run terminal: it returns an error that
                                               reaches execute's fail, which emits the receipt)
resumeAuthority x4       WorkflowFailed        FAILED               measured         no (resume measures little)
Resume                   WorkflowFailed        FAILED               measured         no
runAssisted x2           Completed / Failed    UNREVIEWED / FAILED  NONE (plan NONE) yes
awaitChoice              AwaitingAuthority     not terminal: the run is resumable and settles later
```

**`emitRunTerminal` is the only way a run ends.** Receipt and terminal event are
emitted together, in that order, so they cannot come apart.
`TestOnlyEmitRunTerminalEndsARun` fails if a run-terminal event is constructed
anywhere else. An earlier guard asked only whether a function contained *both* a
terminal kind and *some* receipt call, which a function with three terminal
exits and one receipt would have passed — convention guarded by an approximate
test is how the pairing would have drifted. Its remaining limit is stated in the
test: it inspects direct arguments, so a kind stashed in a variable would evade
it. That is deliberate evasion, not accidental drift.

Outcome and CandidateState stay call-site parameters: centralising the mechanism
must not centralise the judgement.

## What the governor measures, and when

Facts are recorded **at the moment of measurement**, never derived at the end:

```text
beginReceipt          before anything can fail -- so a run that dies at step one
                      still emits a record saying what it never reached
noteServingProducer   /proc/<pid>/exe of the process that ANSWERED this run's
                      awareness initialize -- measured after the launch, never
                      the image that was merely intended to serve
noteWorld             the base commit read ONCE at the certified start, and the
                      LIVE GRAPH DIGEST (not the graph build commit: they are
                      different facts and neither stands in for the other)
notePlan /            the plan axis is asserted, never implied: a supplied plan
notePlanAbsent        is recorded before anything can fail, a lane that never
                      plans CLAIMS NONE, and a run that cannot say says UNKNOWN
notePlan              the identity of the bound this run carried
noteCandidateCreated  the worktree exists (measured: the engine created it)
noteCandidateDigest   sha256 of the candidate diff, as the review binding uses
noteReviewerAssigned  one attempt opens; delivery UNKNOWN until a verdict lands
noteReviewDelivered   the verdict binds to the attempt that gave it
emitRunTerminal       the ONE way a run ends: receipt then terminal event,
                      inseparable. Outcome and CandidateState are PARAMETERS,
                      so a new terminal path decides both; Terminal records the
                      terminal EVENT, never the outcome again under a second
                      name
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

### F2 — RESOLVED: `STOPPED` is now an outcome

The engine refuses to record a human stop as a failure, and it is right to:
recording it as FAILED would teach the behavioural record that the task shape
breaks. The vocabulary had no member for it, so the run was recorded as UNKNOWN
and was INCOMPLETE.

The gap was a **missing vocabulary member**, not a missing measurement, and the
decision was to add it. `STOPPED` is sufficient for `COMPLETE`: a human chose to
end the run, and the instrument recorded that fact completely. It is never
mapped to `FAILED`, and a known outcome is no longer left as `UNKNOWN`.

### F3 — RESOLVED: a `PlanState` axis

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
serving_producer         sha256 of /proc/<pid>/exe for the       NOW MEASURED
                         process that answered this run's MCP
                         initialize -- the PROCESS, not the image
                         that was intended to serve. Measuring the
                         resolvable image before launch would have
                         read KNOWN even when the process failed to
                         start: the C5 sensei-f3 mistake one level
                         down. A platform that cannot name a running
                         process's image says so.
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

## Four binding fixes found by reviewing the wiring

```text
GraphDigest carried the graph BUILD COMMIT      one measured fact, a different claim.
                                                The live digest is on the wire and is now
                                                decoded; the build commit does not stand in
                                                for it, and a start without a digest yields
                                                UNKNOWN and an incomplete receipt.
ServingProducer measured the intended IMAGE     it read KNOWN even if the launch then failed.
before launch                                   It is now the process that ANSWERED.
candidateExists bool                            the raw-zero evidentiary pattern, back one
                                                slice after being removed from
                                                Attempt.Delivered. Now an explicit
                                                CandidateState: a fresh run opens at NONE as
                                                a positive claim, no record reads UNKNOWN.
the terminal guard was approximate              emitRunTerminal is now the single funnel.
```

## Schema version

The receipt is **v2**. `PlanState` is a new required axis and `STOPPED` extends
the outcome vocabulary: both change what COMPLETE means, so a v1 reader would
misread a v2 record and a v2 reader would find v1 records missing an axis.
Leaving the version at v1 through that change would have been the fabricated
specimen the version comment warns about.

## One base, read once

The repository head was read twice around the start boundary -- once entering
the gate, once for the receipt -- while `candidate.Establish` pinned the
candidate's base independently. If the repository moved in between, the receipt
could name one base while the candidate was rooted at another. The head is now
read once, carried through the start, and the run **refuses** if the candidate's
base differs from it.

## What this slice did not do

- No admission. The receipt reports evidence and grants nothing.
- No isolation. It stays external.
- No schema relaxation. Three terminals are INCOMPLETE and say why.
