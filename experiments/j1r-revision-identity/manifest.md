# J1-R — successor to J1, frozen before execution

J1 at `36cfeaa` remains **FROZEN / NOT EXECUTED**. It is not edited, extended or
reinterpreted. J1-R is a new experiment.

## Why J1 itself is not run

Task A, executed after J1 was frozen but before it ran, naturally produced a
Codex REVISE and then exhausted J1's 25-minute budget during the revision turn.
That execution demonstrated two facts not known at J1's freeze:

```text
1  a natural revision cycle can exceed 25 minutes
2  at that time, a timeout ended the invocation without a terminal receipt
```

Running J1 unchanged would knowingly spend the experiment reproducing a
newly-demonstrated accounting boundary instead of testing the conditional
T1 -> T2 properties it exists to observe.

## Inherited from J1, unchanged

* the exact behavioural task text, byte-identical
* the reviewer's suggested implementation remains WITHHELD from the worker
* all twelve falsifiers
* criteria 8-12 remain conditional on a naturally occurring REVISE; if none
  occurs they are UNTESTED, not PASS
* no desired reviewer verdict; no manufactured revision
* ONE invocation; no opportunistic replanning
* no post-result weakening or reinterpretation of any falsifier
* ACCEPT, REVISE, REFUSE, TIMED_OUT, DEFERRED or failure are all recordable
  outcomes rather than desired or undesired answers

## Schema inheritance note

**J1-R runs under receipt schema v6 as a consequence of the pre-registered
instrumentation and accounting repairs made after J1 was frozen. Any inherited
schema-consistency falsifier is evaluated against v6 and its defined
vocabulary, not against the schema version that existed when J1 was frozen.
This is not a relaxation of the falsifier.**

## Governor deltas permitted, and the one refused

Permitted, each driven by evidence obtained before this freeze:

```text
1  timeout is its own TIMED_OUT outcome
2  a timed-out invocation waits boundedly for its own terminal account
3  a timeout receipt records the execution budget and preserves prior attempts
4  formatter mutation is mechanically observable as a sourced tri-state
5  terminal draining waits for an invocation-final terminal, not an
   interruption boundary
```

Refused: **P1 is NOT repaired.** Verified in the governor being used —
`engine.go` still pairs the post-validation `candidateRevision(diff)` with the
pre-validation `capture.Tree`. J1-R asks the governed worker to solve it.

## Pinned identities

```text
J1 original             36cfeaa                     FROZEN / NOT EXECUTED
governor source         2d5b31c0082e22fa0b2de43dcf61367eca8d6992
governor binary sha256  a741dde4e8b4e3ce961bada2daca5527a735fc4f676f54cddfdc6c8dcda0561a
                        built from a clean tree, vcs.modified=false
receipt schema          sensei-code.governed-run-receipt/v6
governed base           2d5b31c0082e22fa0b2de43dcf61367eca8d6992
graph build commit      fac399f8225f
seed digest             42e6e12cd5737530c4c8d054f8178cde849b72cae7c4845b6613f07a714d2b64
serving producer        pid 2375957, sha256 1070ee8dedeec323a47ebdaa360c390fd9e7d0e68826f7e7679216960f8b74eb
budget                  50m
terminal grace          15s, separate, NOT work budget
runs allowed            ONE
```

The governor was built **before** this manifest was committed, so recording the
experiment cannot perturb the identity it records. The manifest's own commit is
therefore later than `governor source`, deliberately: the experiment's
documentation is not part of the mechanism under test.

## Execution budget, and its denominator

```text
25m empirically reached a first real review and a REVISE
J1-R needs the opportunity for a SECOND implementation/validation/review cycle
50m = 2 x the measured original budget
no extension if 50m proves insufficient
```

This is not an extension chosen to obtain success. **TIMED_OUT at 50 minutes is
a result, not an invitation to try 75.**

## P1 activation is measured, never inferred

Never from filenames, and never from the fact that formatting is configured.

```text
1  a mint refusal mechanically naming incompatible reviewed/current identities
2  a minted canonical candidate with verified MATCH -- establishing the
   stale-binding defect did NOT activate
3  the run's sourced formatter-mutation fact, when neither terminal exists
```

If none can establish it, activation is **UNKNOWN** rather than guessed.

## Pre-registered bootstrap outcome

```text
worker repairs P1
-> validation establishes the repair
-> an independent reviewer accepts the repaired state
-> the OLD governor's P1 causes its mint to refuse that replacement
```

If and only if those facts are actually observed, classify as a **bootstrap
boundary**:

```text
X contains F -> X' repairs F -> X must certify X' -> F in X prevents it
```

That is not credited as successful minting, is not classified as reviewer
failure, and does not permit the candidate to certify itself.

## The task, byte-identical to J1

```text
Fix the candidate-identity behaviour when validation mutates candidate bytes,
such as when a configured formatter changes a file.

The exact candidate tree bound to review must represent the post-validation
content the reviewer actually receives. If that exact reviewed state is accepted
and does not subsequently move, canonical candidate minting must succeed for
that state. A candidate that changes after the state bound to the verdict must
still be refused rather than silently rebound.

Add a regression test using a validation formatter that actually mutates
candidate content and proves the end-to-end identity behaviour. Preserve the
existing fail-closed behaviour for candidate movement and do not weaken
review-to-tree or base-to-candidate identity checks.

Choose the implementation that best preserves the existing authority and
identity model; the task does not prescribe where candidate state should be
re-measured.
```

Nothing in this manifest changes after the invocation begins.
