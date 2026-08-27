# COLD_WAVE — halted as a measurement-integrity failure

**The COLD column is void. It measured the harness, not the product.**

Every arm ran to completion and the schedule was followed exactly. Then the
evidence was read, and it does not say what it appears to say.

## What was recorded

| | RAW | COLD (as recorded) |
|---|---|---|
| engineering correctness | 8/11 | 0/4 |
| end-to-end success | 8/11 | 0/11 |
| NOT_EVALUATED | 0 | 7 |
| terminals | COMPLETED 11 | COMPLETED 4, REFUSED 5, TIMEOUT 2 |
| wall, median | 264s | 385s |
| wall, total | 51 min | 105 min |
| human technical interventions | — | 0 |

Read at face value this is interpretation **D**, the most serious one:
governance delivering less and getting it wrong.

## Why it is not that

**Every one of the eleven COLD arms recorded the same candidate diff hash:**

```
sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855
```

That is the hash of the empty tree. Not one governed arm appeared to write any
code — which is not a plausible product failure, and is a very plausible
measurement failure.

It was. **A governed run does its work in its own candidate worktree**, created
by the engine beside the arm's checkout. `proofbench` ran the oracle against the
**arm** worktree, which never receives that work.

Six candidate worktrees survive on disk and contain real work:

```
internal-gitx-a4fa351   M internal/gitx/git.go
                        A internal/gitx/git_test.go
                        M internal/workflow/engine.go
internal-assist-5dfffad M internal/assist/packet.go
                        A internal/assist/packet_test.go
internal-decision-…     M internal/decision/record.go
                        A internal/decision/record_test.go
```

The `internal-gitx-a4fa351` candidate contains the required `CandidateDiff`
method. Running the **frozen contract probe** against that candidate:

```
ok  github.com/globulario/sensei-code/internal/gitx  0.065s
```

**It passes.** The arm was recorded `INCORRECT`.

## What this voids, and what it does not

Void: the COLD correctness column, the transition matrix, the false-grant count,
and the RAW-vs-COLD comparison. All of it was computed from code the run did not
put where the harness looked.

Not void: **the RAW wave.** A RAW arm works directly in its own checkout, so the
oracle judged the right tree, and all eleven RAW diffs are non-empty. RAW's
8/11 stands.

Also not void, and worth keeping: the **delivery** observations. Five arms
reached `awaiting_authority` and two exhausted the 22-minute budget. Those are
terminal statuses reported by the product itself, not derived from the diff, and
they are real. Whether they would have been correct had they delivered is
exactly what this wave failed to measure.

## Why the campaign halted

The frozen halt rule permits stopping only for measurement-integrity failure,
and names **attribution failure** among them. This is one: results were
attributed to code the run did not produce in the place the harness looked.

A bad score is a result and must not stop the campaign. A score computed from
the wrong directory is not a score.

## What happens next

Every attempt stays in the ledger exactly as written. Nothing is re-run, edited
or deleted, and no interpretation A–E is applied.

The repair is to the harness: judge the candidate a governed run actually
produced. That is a new benchmark version with its own manifest hash, because
the evaluation changed. This slice is preserved as the evidence for why, and the
COLD wave is re-run under it.

The eleventh instrument defect, and the most expensive: 105 minutes of provider
time producing a number that meant nothing. It was caught by an implausible
uniformity — eleven identical empty hashes — rather than by any test, which is
worth remembering when designing the next check.
