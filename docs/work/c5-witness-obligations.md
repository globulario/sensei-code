# C5 — the witness obligations, governed by X (brief)

C4 was VOID. Its instrument was incomplete against its own frozen contract,
and the defects that produced that verdict were the adjudicator's: a run
script that did not emit the fields its own freeze required, a setup step
outside the frozen materialisation, a plan that prescribed the whitespace
normalisation it was meant to forbid, and corrections that reached some
renderings of the record and not others.

So C5's first job is not to test the loop. It is to make the witness prove
itself:

> **An instrument must prove its own completeness before its contents are
> allowed to prove anything else** — mechanically, as a gate that refuses,
> because every one of C4's defects was something verified by reading.

Seven frozen obligations: instrument self-closure before adjudication; refs
classified by reachability and ownership rather than shape; candidate commit
mandatory before terminal completion; exact Git pathname identity; the
materialisation verified **and captured** before the governor runs; a missing
required field taking the hard refusal/VOID path rather than degraded
continuation; and the actual reviewer identity measured from the run — with
the rule that **reviewer identity is evidence about who reviewed, not
authority to admit**, and that no bounded verdict means UNREVIEWED, never
clean by exhaustion.

Two of those are repairs to the loop and form the supplied plan
(`990090fd50446fedcdf60f11e3256ed91a22fac1670cc4d9333e86f9e638d554`): path identity compared exactly as Git reports it, and terminal
completion refusing an uncommitted candidate. The other five are repairs to
this harness.

Identities, the ref-classification table, required artifacts and fields, the
falsifiers and the predictions are in
`experiments/c5-witness-obligations/manifest.md`. `capture.go` remains C6.

## Pre-run amendment

The freeze was reviewed before it was run, and its prose turned out to be
stronger than its harness: the capture printed `FALSIFIER` and exited 0, the
candidate ref's ownership and ancestry were printed rather than enforced, two
frozen-REQUIRED artifacts (`C5.closure.txt`, `C5.candidate.diff`) were never
checked, fields were checked by label rather than by value, and nothing
committed bound capture → gate → governor together. That last one is how C4
acquired the out-of-procedure step that voided it.

One pre-run correction commit answers all of it: `c5-run.sh` is now the
frozen runner and the only entry point; both gates exit non-zero on every
frozen falsifier; the expected candidate ref is read from the governor's own
log and then measured against the subject's refs by **exact first parent**;
and a third `closed` phase gates the gate's own receipt. ~~Fourteen validation
cases, seven for the capture and eight for the closure gate, are recorded in
`experiments/c5-witness-obligations/prerun-validation.txt`.~~ **WITHDRAWN**:
two of those cases did not produce their stated exit status, and seven plus
eight is fifteen. A hand-written validation transcript is prose.

Measuring rather than asserting immediately cost something: the process
actually answering awareness is `awareness-graph`, not the `sensei-f3` file
that C3 and C4 recorded as the producer. Both are now recorded, and C5 does
not claim to repair the two runs that came before it.


## Second pre-run amendment

Reviewing the first amendment found four more defects, every one of them
found by the apparatus rather than by a run: the validation transcript
reported two failed expectations as passes and miscounted itself; the capture
still had one fail-open path (`HEAD != X` printed `FALSIFIER` and incremented
nothing); the newly measured serving producer was informational, so a run
could still record `unknown`; and the runner recorded its own digest rather
than refusing a drifted harness.

The validation is now `c5-validate.sh`, a gate that materialises a fresh
subject per case, asserts every expected exit status, prints its own count
and exits non-zero on any disagreement: **25 cases, 25 passed**. The capture
exits non-zero on every falsifier it prints. The measured serving process is
required, may not read `unknown`, and must be the same executable at both
ends. `c5-run.sh` pins the SHA256 of the capture, closure and extractor
scripts and refuses to run a harness that differs from its committed state —
recording drift is weaker than refusing it. And `c5-extract.py` keeps the
whole reviewer trail, so a run where one provider fails and another delivers
records both; the frozen `sensei-f3` is relabelled a reference file identity,
not the producer, because nothing has shown it executing.
